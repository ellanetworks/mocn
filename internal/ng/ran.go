package ng

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gw"
	"github.com/ellanetworks/mocn/internal/tracing"
)

const nonUEStream = 0

var (
	causeUnknownPLMN            = ngap.Cause{Group: ngap.CauseGroupMisc, Value: ngap.CauseMiscUnknownPLMNOrSNPN}
	causeMiscUnspecified        = ngap.Cause{Group: ngap.CauseGroupMisc, Value: ngap.CauseMiscUnspecified}
	causeSemanticError          = ngap.Cause{Group: ngap.CauseGroupProtocol, Value: ngap.CauseProtocolSemanticError}
	causeNotCompatibleWithState = ngap.Cause{Group: ngap.CauseGroupProtocol, Value: ngap.CauseProtocolMessageNotCompatible}
	causeTransportUnavailable   = ngap.Cause{Group: ngap.CauseGroupTransport, Value: ngap.CauseTransportResourceUnavailable}
)

const noAMFTimeToWait = ngap.TimeToWaitV5s

type RANAssociation struct {
	g           *Gateway
	conn        *sctp.SCTPConn
	addrs       []string
	connectedAt time.Time
	setup       *ranSetup
	up          bool
	amfConfig   amfConfigPush
}

type amfConfigPush struct {
	acked   amfConfig
	pending *amfConfig
	timer   *time.Timer
	retryAt time.Time
}

func (n *RANAssociation) Send(ctx context.Context, msg []byte, stream uint16) error {
	return n.g.send(ctx, sideRadio, n.conn, "", msg, stream)
}

type operatorRAN struct {
	n        *RANAssociation
	operator string
}

func (r operatorRAN) Send(ctx context.Context, msg []byte, stream uint16) error {
	return r.n.g.send(ctx, sideRadio, r.n.conn, r.operator, msg, stream)
}

func (n *RANAssociation) snapshotLocked() RANNode {
	r := RANNode{
		State:          RANNodeConnected,
		ConnectedSince: n.connectedAt,
		TNLAddresses:   slices.Clone(n.addrs),
		SupportedTAs:   []TA{},
	}

	if n.up {
		r.State = RANNodeUp
	}

	if s := n.setup; s != nil {
		id := s.globalGNBID
		r.GlobalGNBID = &id
		r.Name = s.name
		r.SupportedTAs = slices.Clone(s.tas)
		r.DefaultPagingDRX = pagingDRXFrames(s.drx)
	}

	return r
}

func (g *Gateway) ranConnected(conn *sctp.SCTPConn) {
	n := &RANAssociation{g: g, conn: conn, addrs: gw.Addresses(conn.RemoteAddr()), connectedAt: g.cfg.Now()}

	g.mu.Lock()
	g.ranNodes[conn] = n
	g.mu.Unlock()

	g.log.Info("radio connected", slog.Any("tnl_addresses", n.addrs))
}

func (g *Gateway) ranDisconnected(conn *sctp.SCTPConn) {
	g.mu.Lock()

	n, ok := g.ranNodes[conn]
	if ok {
		delete(g.ranNodes, conn)

		if n.amfConfig.timer != nil {
			n.amfConfig.timer.Stop()
		}

		if n.setup != nil {
			g.kickAMFsLocked()
		}
	}

	g.mu.Unlock()

	if !ok {
		return
	}

	g.log.Info("radio disconnected", slog.Any("tnl_addresses", n.addrs))
	g.relay.ranLost(context.TODO(), n, causeTransportUnavailable)
}

func (g *Gateway) dispatchRAN(ctx context.Context, conn *sctp.SCTPConn, msg []byte) {
	g.mu.Lock()
	n, ok := g.ranNodes[conn]
	g.mu.Unlock()

	if !ok {
		return
	}

	pdu, err := ngap.Unmarshal(msg)

	ctx, span := g.receiveFromRAN(ctx, n, pdu, msg)
	defer span.End()

	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		g.log.WarnContext(ctx, "undecodable NGAP message from radio", slog.Any("error", err),
			slog.Any("tnl_addresses", n.addrs))

		return
	}

	switch p := pdu.(type) {
	case *ngap.InitiatingMessage:
		switch p.ProcedureCode {
		case ngap.ProcNGSetup:
			g.handleNGSetup(ctx, n, p.Value)
			return
		case ngap.ProcRANConfigurationUpdate:
			g.handleRANConfigurationUpdate(ctx, n, p.Value)
			return
		}
	case *ngap.SuccessfulOutcome:
		if p.ProcedureCode == ngap.ProcAMFConfigurationUpdate {
			g.handleAMFConfigurationUpdateAcknowledge(ctx, n)
			return
		}
	case *ngap.UnsuccessfulOutcome:
		if p.ProcedureCode == ngap.ProcAMFConfigurationUpdate {
			g.handleAMFConfigurationUpdateFailure(ctx, n, p.Value)
			return
		}
	}

	g.mu.Lock()
	up := n.up
	g.mu.Unlock()

	if !up {
		tracing.SpanFailed(ctx, errTypeNoSetup)
		g.log.DebugContext(ctx, "dropped NGAP message before NG Setup", slog.String("procedure", procedureOf(pdu).String()),
			slog.Any("tnl_addresses", n.addrs))

		return
	}

	g.relay.fromRAN(ctx, n, pdu, msg)
}

func ranSetupFrom(req *ngap.NGSetupRequest) (ranSetup, error) {
	id, err := globalGNBIDFrom(req.GlobalRANNodeID)
	if err != nil {
		return ranSetup{}, err
	}

	tas, err := tasFrom(req.SupportedTAList)
	if err != nil {
		return ranSetup{}, err
	}

	if req.DefaultPagingDRX == nil {
		return ranSetup{}, errors.New("missing Default Paging DRX")
	}

	s := ranSetup{globalGNBID: id, tas: tas, drx: *req.DefaultPagingDRX}
	if req.RANNodeName != nil {
		s.name = *req.RANNodeName
	}

	return s, nil
}

// TS 38.413 clause 8.7.1
// TS 38.300 clause 4.10.2.3
func (g *Gateway) handleNGSetup(ctx context.Context, n *RANAssociation, value []byte) {
	g.mu.Lock()
	wasUp := n.up
	n.up = false
	g.mu.Unlock()

	if wasUp {
		g.relay.ranLost(ctx, n, causeNGRANRelease)
	}

	req, err := ngap.ParseNGSetupRequest(value)
	if err != nil {
		if ase, ok := errors.AsType[*ngap.AbstractSyntaxError](err); ok {
			diag := ase.OutcomeDiagnostics()
			g.rejectNGSetup(ctx, n, setupInvalid, ase.Cause, nil, &diag, err.Error())

			return
		}

		g.rejectNGSetup(ctx, n, setupInvalid, causeMiscUnspecified, nil, nil, err.Error())

		return
	}

	setup, err := ranSetupFrom(req)
	if err != nil {
		g.rejectNGSetup(ctx, n, setupInvalid, causeSemanticError, nil, nil, err.Error())
		return
	}

	g.mu.Lock()

	serving := servingOperators(setup.tas, g.operators)
	if len(serving) == 0 {
		g.mu.Unlock()
		g.rejectNGSetup(ctx, n, setupUnknownPLMN, causeUnknownPLMN, nil, nil, "no operator serves a broadcast PLMN")

		return
	}

	if n.amfConfig.timer != nil {
		n.amfConfig.timer.Stop()
	}

	n.setup, n.up, n.amfConfig = &setup, false, amfConfigPush{}

	g.kickAMFsLocked()
	g.mu.Unlock()

	g.log.InfoContext(ctx, "NG Setup Request", slog.String("ran_node_name", setup.name),
		slog.String("global_gnb_id", formatGlobalGNBID(setup.globalGNBID)), slog.Any("tnl_addresses", n.addrs))

	start := g.cfg.Now()
	timer := time.NewTimer(g.cfg.SetupWait)

	defer timer.Stop()

	for {
		g.mu.Lock()

		if g.ranNodes[n.conn] != n {
			g.mu.Unlock()
			g.countSetup(sideRadio, setupError)
			tracing.SpanFailed(ctx, setupError)

			return
		}

		if cfg := g.amfConfigLocked(); !cfg.empty() && g.anyAMFUpLocked(serving) {
			g.acceptNGSetupLocked(ctx, n, cfg, req.Diagnostics())
			g.mu.Unlock()

			return
		}

		cause, ttw, failed := g.amfSetupFailureLocked(serving, start)
		changed := g.changed
		g.mu.Unlock()

		if failed {
			g.rejectNGSetup(ctx, n, setupAMFFailure, cause, ttw, nil, "NG Setup toward the operator's AMFs failed: "+cause.String())
			return
		}

		select {
		case <-changed:
		case <-timer.C:
			g.mu.Lock()
			cause, ttw, failed := g.amfSetupFailureLocked(serving, time.Time{})
			g.mu.Unlock()

			if failed {
				g.rejectNGSetup(ctx, n, setupAMFFailure, cause, ttw, nil, "NG Setup toward the operator's AMFs failed: "+cause.String())
				return
			}

			g.rejectNGSetup(ctx, n, setupNoAMF, causeTransportUnavailable, ngap.Ptr(noAMFTimeToWait), nil,
				"no AMF of the serving operators is up")

			return
		case <-ctx.Done():
			g.countSetup(sideRadio, setupError)
			tracing.SpanFailed(ctx, setupError)

			return
		}
	}
}

func (g *Gateway) anyAMFUpLocked(operators []int64) bool {
	for _, c := range g.amfs {
		if c.status.State == AMFUp && slices.Contains(operators, c.operatorID) {
			return true
		}
	}

	return false
}

func (g *Gateway) amfSetupFailureLocked(operators []int64, since time.Time) (ngap.Cause, *ngap.TimeToWait, bool) {
	var (
		cause ngap.Cause
		ttw   *ngap.TimeToWait
		found bool
	)

	for _, key := range slices.Sorted(maps.Keys(g.amfs)) {
		c := g.amfs[key]
		if !slices.Contains(operators, c.operatorID) {
			continue
		}

		if c.status.State != AMFSetupFailed || c.lastCause == nil || c.status.Since.Before(since) {
			return ngap.Cause{}, nil, false
		}

		if !found {
			cause, found = *c.lastCause, true
		}

		if c.lastTimeToWait != nil && (ttw == nil || *c.lastTimeToWait > *ttw) {
			ttw = c.lastTimeToWait
		}
	}

	return cause, ttw, found
}

func (g *Gateway) acceptNGSetupLocked(ctx context.Context, n *RANAssociation, cfg amfConfig, d ngap.Diagnostics) {
	out, err := ngSetupResponse(cfg, responseDiagnostics(ngap.ProcNGSetup, d))
	if err != nil {
		g.countSetup(sideRadio, setupError)
		tracing.SpanFailed(ctx, setupError)
		g.log.ErrorContext(ctx, "failed to build NG Setup Response", slog.Any("error", err))

		return
	}

	if err := n.Send(ctx, out, nonUEStream); err != nil {
		g.countSetup(sideRadio, setupError)
		tracing.SpanFailed(ctx, setupError)
		g.log.WarnContext(ctx, "failed to send NG Setup Response", slog.Any("error", err))

		return
	}

	n.up = true
	n.amfConfig.acked = cfg

	g.countSetup(sideRadio, setupSuccess)

	g.log.InfoContext(ctx, "radio NG Setup accepted", slog.String("ran_node_name", n.setup.name),
		slog.String("global_gnb_id", formatGlobalGNBID(n.setup.globalGNBID)))
}

// TS 38.413 clause 9.2.6.2
func ngSetupResponse(cfg amfConfig, diag *ngap.CriticalityDiagnostics) ([]byte, error) {
	served, err := servedGUAMIsTo(cfg.served)
	if err != nil {
		return nil, err
	}

	support, err := plmnSupportTo(cfg.support)
	if err != nil {
		return nil, err
	}

	out, err := (&ngap.NGSetupResponse{
		AMFName:                gw.NodeName,
		ServedGUAMIList:        served,
		RelativeAMFCapacity:    ngap.Ptr(relativeAMFCapacity),
		PLMNSupportList:        support,
		CriticalityDiagnostics: diag,
	}).Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal NG Setup Response: %w", err)
	}

	return out, nil
}

func (g *Gateway) rejectNGSetup(ctx context.Context, n *RANAssociation, result string, cause ngap.Cause,
	ttw *ngap.TimeToWait, diag *ngap.CriticalityDiagnostics, reason string,
) {
	g.mu.Lock()

	if n.setup != nil {
		g.kickAMFsLocked()
	}

	n.setup, n.up = nil, false
	g.mu.Unlock()

	g.countSetup(sideRadio, result)
	tracing.SpanFailed(ctx, result)

	g.log.WarnContext(ctx, "radio NG Setup rejected", slog.String("cause", cause.String()), slog.String("reason", reason),
		slog.Any("tnl_addresses", n.addrs))

	out, err := (&ngap.NGSetupFailure{Cause: &cause, TimeToWait: ttw, CriticalityDiagnostics: diag}).Marshal()
	if err != nil {
		g.log.ErrorContext(ctx, "failed to marshal NG Setup Failure", slog.Any("error", err))
		return
	}

	if err := n.Send(ctx, out, nonUEStream); err != nil {
		g.log.WarnContext(ctx, "failed to send NG Setup Failure", slog.Any("error", err))
	}
}

// TS 38.413 clause 8.7.2
func (g *Gateway) handleRANConfigurationUpdate(ctx context.Context, n *RANAssociation, value []byte) {
	g.mu.Lock()
	up := n.up
	g.mu.Unlock()

	if !up {
		g.rejectRANConfigurationUpdate(ctx, n, errTypeNoSetup, causeNotCompatibleWithState, nil, "no NG Setup")
		return
	}

	upd, err := ngap.ParseRANConfigurationUpdate(value)
	if err != nil {
		if ase, ok := errors.AsType[*ngap.AbstractSyntaxError](err); ok {
			diag := ase.OutcomeDiagnostics()
			g.rejectRANConfigurationUpdate(ctx, n, errTypeInvalid, ase.Cause, &diag, err.Error())

			return
		}

		g.rejectRANConfigurationUpdate(ctx, n, errTypeInvalid, causeMiscUnspecified, nil, err.Error())

		return
	}

	var tas []TA

	if len(upd.SupportedTAList) > 0 {
		if tas, err = tasFrom(upd.SupportedTAList); err != nil {
			g.rejectRANConfigurationUpdate(ctx, n, errTypeInvalid, causeSemanticError, nil, err.Error())
			return
		}
	}

	out, err := (&ngap.RANConfigurationUpdateAcknowledge{
		CriticalityDiagnostics: responseDiagnostics(ngap.ProcRANConfigurationUpdate, upd.Diagnostics()),
	}).Marshal()
	if err != nil {
		g.log.ErrorContext(ctx, "failed to marshal RAN Configuration Update Acknowledge", slog.Any("error", err))
		return
	}

	g.mu.Lock()

	if tas != nil && len(servingOperators(tas, g.operators)) == 0 {
		g.mu.Unlock()
		g.rejectRANConfigurationUpdate(ctx, n, setupUnknownPLMN, causeUnknownPLMN, nil, "no operator serves a broadcast PLMN")

		return
	}

	if n.setup == nil {
		g.mu.Unlock()
		return
	}

	s := *n.setup

	if upd.RANNodeName != nil {
		s.name = *upd.RANNodeName
	}

	if tas != nil {
		s.tas = tas
	}

	if upd.DefaultPagingDRX != nil {
		s.drx = *upd.DefaultPagingDRX
	}

	n.setup = &s

	if err := n.Send(ctx, out, nonUEStream); err != nil {
		g.log.WarnContext(ctx, "failed to send RAN Configuration Update Acknowledge", slog.Any("error", err))
	}

	g.kickAMFsLocked()
	g.mu.Unlock()

	g.log.InfoContext(ctx, "radio configuration updated", slog.String("ran_node_name", s.name),
		slog.String("global_gnb_id", formatGlobalGNBID(s.globalGNBID)))
}

func (g *Gateway) rejectRANConfigurationUpdate(ctx context.Context, n *RANAssociation, errType string,
	cause ngap.Cause, diag *ngap.CriticalityDiagnostics, reason string,
) {
	tracing.SpanFailed(ctx, errType)

	g.log.WarnContext(ctx, "radio RAN Configuration Update rejected", slog.String("cause", cause.String()),
		slog.String("reason", reason), slog.Any("tnl_addresses", n.addrs))

	out, err := (&ngap.RANConfigurationUpdateFailure{Cause: &cause, CriticalityDiagnostics: diag}).Marshal()
	if err != nil {
		g.log.ErrorContext(ctx, "failed to marshal RAN Configuration Update Failure", slog.Any("error", err))
		return
	}

	if err := n.Send(ctx, out, nonUEStream); err != nil {
		g.log.WarnContext(ctx, "failed to send RAN Configuration Update Failure", slog.Any("error", err))
	}
}

// TS 38.413 clause 8.7.3
func (g *Gateway) syncRANLocked(ctx context.Context, n *RANAssociation) {
	p := &n.amfConfig
	if !n.up || p.pending != nil || g.cfg.Now().Before(p.retryAt) {
		return
	}

	cfg := g.amfConfigLocked()
	if cfg.empty() || cfg.equal(p.acked) {
		return
	}

	g.sendAMFConfigurationUpdateLocked(ctx, n, cfg)
}

// TS 38.413 clause 9.2.6.7
func buildAMFConfigurationUpdate(cfg amfConfig) ([]byte, error) {
	served, err := servedGUAMIsTo(cfg.served)
	if err != nil {
		return nil, err
	}

	support, err := plmnSupportTo(cfg.support)
	if err != nil {
		return nil, err
	}

	out, err := (&ngap.AMFConfigurationUpdate{AMFName: ngap.Ptr(gw.NodeName), ServedGUAMIList: served}).Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal AMF Configuration Update: %w", err)
	}

	value, err := per.Marshal(support, per.Aligned)
	if err != nil {
		return nil, fmt.Errorf("encode PLMN Support List: %w", err)
	}

	p, err := ngap.ParseRawPDU(out)
	if err != nil {
		return nil, err
	}

	if err := p.Insert(p.Len(), ngap.RawIE{ID: ngap.IDPLMNSupportList, Criticality: ngap.CriticalityReject, Value: value}); err != nil {
		return nil, err
	}

	return p.Marshal()
}

func (g *Gateway) sendAMFConfigurationUpdateLocked(ctx context.Context, n *RANAssociation, cfg amfConfig) {
	out, err := buildAMFConfigurationUpdate(cfg)
	if err != nil {
		g.log.ErrorContext(ctx, "failed to build AMF Configuration Update", slog.Any("error", err))
		return
	}

	if err := n.Send(ctx, out, nonUEStream); err != nil {
		g.log.WarnContext(ctx, "failed to send AMF Configuration Update", slog.Any("error", err))
		return
	}

	n.amfConfig.pending = &cfg
	retryCtx := tracing.DetachedRoot(ctx)
	n.amfConfig.timer = time.AfterFunc(g.cfg.ResponseTimeout, func() {
		g.mu.Lock()
		defer g.mu.Unlock()

		if g.ranNodes[n.conn] == n && n.up && n.amfConfig.pending != nil && n.amfConfig.pending.equal(cfg) {
			g.sendAMFConfigurationUpdateLocked(retryCtx, n, cfg)
		}
	})
}

func (g *Gateway) handleAMFConfigurationUpdateAcknowledge(ctx context.Context, n *RANAssociation) {
	g.mu.Lock()
	defer g.mu.Unlock()

	p := &n.amfConfig
	if p.pending == nil {
		return
	}

	if p.timer != nil {
		p.timer.Stop()
	}

	p.acked, p.pending = *p.pending, nil

	g.syncRANLocked(ctx, n)
}

func (g *Gateway) handleAMFConfigurationUpdateFailure(ctx context.Context, n *RANAssociation, value []byte) {
	fail, err := ngap.ParseAMFConfigurationUpdateFailure(value)

	retry := g.cfg.RetryInterval

	if err == nil && fail.TimeToWait != nil {
		retry = timeToWait[*fail.TimeToWait]
	}

	cause := "undecodable"
	if err == nil {
		cause = causeText(fail.Cause)
	} else {
		tracing.SpanFailed(ctx, errTypeInvalid)
	}

	g.log.WarnContext(ctx, "radio rejected AMF Configuration Update", slog.String("cause", cause),
		slog.Any("tnl_addresses", n.addrs))

	g.mu.Lock()
	defer g.mu.Unlock()

	p := &n.amfConfig
	if p.pending == nil {
		return
	}

	if p.timer != nil {
		p.timer.Stop()
	}

	p.pending = nil
	p.retryAt = g.cfg.Now().Add(retry)
	retryCtx := tracing.DetachedRoot(ctx)
	p.timer = time.AfterFunc(retry, func() {
		g.mu.Lock()
		defer g.mu.Unlock()

		if g.ranNodes[n.conn] == n {
			g.syncRANLocked(retryCtx, n)
		}
	})
}

func formatGlobalGNBID(id db.GlobalGNBID) string {
	return fmt.Sprintf("%s-%d-%d", id.PLMN, id.Length, id.ID)
}
