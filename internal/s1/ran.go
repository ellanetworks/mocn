package s1

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
)

const nonUEStream = 0

var (
	causeUnknownPLMN            = s1ap.Cause{Group: s1ap.CauseGroupMisc, Value: s1ap.CauseMiscUnknownPLMN}
	causeMiscUnspecified        = s1ap.Cause{Group: s1ap.CauseGroupMisc, Value: s1ap.CauseMiscUnspecified}
	causeSemanticError          = s1ap.Cause{Group: s1ap.CauseGroupProtocol, Value: s1ap.CauseProtocolSemanticError}
	causeNotCompatibleWithState = s1ap.Cause{Group: s1ap.CauseGroupProtocol, Value: 3}
	causeTransportUnavailable   = s1ap.Cause{Group: s1ap.CauseGroupTransport, Value: s1ap.CauseTransportResourceUnavailable}
)

const noMMETimeToWait = s1ap.TimeToWaitV5s

type RANAssociation struct {
	conn        *sctp.SCTPConn
	addrs       []string
	connectedAt time.Time
	setup       *ranSetup
	up          bool
	mmeConfig   mmeConfigPush
}

type mmeConfigPush struct {
	acked   []ServedGUMMEIs
	pending []ServedGUMMEIs
	timer   *time.Timer
	retryAt time.Time
}

func (n *RANAssociation) Send(msg []byte, stream uint16) error {
	return send(n.conn, msg, stream)
}

func (n *RANAssociation) TNLAddresses() []string {
	return slices.Clone(n.addrs)
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
		id := s.globalENBID
		r.GlobalENBID = &id
		r.Name = s.name
		r.SupportedTAs = slices.Clone(s.tas)
		r.DefaultPagingDRX = pagingDRXFrames(s.drx)
	}

	return r
}

func addrStrings(a any) []string {
	addr, ok := a.(*sctp.SCTPAddr)
	if !ok || addr == nil {
		return nil
	}

	out := make([]string, 0, len(addr.IPAddrs))

	for _, ip := range addr.IPAddrs {
		if a, ok := netip.AddrFromSlice(ip.IP); ok {
			out = append(out, a.Unmap().String())
		}
	}

	return out
}

func (g *Gateway) ranConnected(conn *sctp.SCTPConn) {
	n := &RANAssociation{conn: conn, addrs: addrStrings(conn.RemoteAddr()), connectedAt: g.cfg.Now()}

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

		if n.mmeConfig.timer != nil {
			n.mmeConfig.timer.Stop()
		}

		if n.setup != nil {
			g.kickMMEsLocked()
		}
	}

	g.mu.Unlock()

	if !ok {
		return
	}

	g.log.Info("radio disconnected", slog.Any("tnl_addresses", n.addrs))
	g.cfg.Relay.RANLost(n, causeTransportUnavailable)
}

func (g *Gateway) dispatchRAN(ctx context.Context, conn *sctp.SCTPConn, msg []byte) {
	g.mu.Lock()
	n, ok := g.ranNodes[conn]
	g.mu.Unlock()

	if !ok {
		return
	}

	pdu, err := s1ap.Unmarshal(msg)
	if err != nil {
		g.log.WarnContext(ctx, "undecodable S1AP message from radio", slog.Any("error", err),
			slog.Any("tnl_addresses", n.addrs))

		return
	}

	switch p := pdu.(type) {
	case *s1ap.InitiatingMessage:
		switch p.ProcedureCode {
		case s1ap.ProcS1Setup:
			g.handleS1Setup(ctx, n, p.Value)
			return
		case s1ap.ProcENBConfigurationUpdate:
			g.handleENBConfigurationUpdate(ctx, n, p.Value)
			return
		}
	case *s1ap.SuccessfulOutcome:
		if p.ProcedureCode == s1ap.ProcMMEConfigurationUpdate {
			g.handleMMEConfigurationUpdateAcknowledge(n)
			return
		}
	case *s1ap.UnsuccessfulOutcome:
		if p.ProcedureCode == s1ap.ProcMMEConfigurationUpdate {
			g.handleMMEConfigurationUpdateFailure(ctx, n, p.Value)
			return
		}
	}

	g.mu.Lock()
	up := n.up
	g.mu.Unlock()

	if !up {
		g.log.DebugContext(ctx, "dropped S1AP message before S1 Setup", slog.String("procedure", procedureOf(pdu).String()),
			slog.Any("tnl_addresses", n.addrs))

		return
	}

	g.cfg.Relay.FromRAN(ctx, n, pdu, msg)
}

func ranSetupFrom(req *s1ap.S1SetupRequest) (ranSetup, error) {
	id, err := globalENBIDFrom(req.GlobalENBID)
	if err != nil {
		return ranSetup{}, err
	}

	tas, err := tasFrom(req.SupportedTAs)
	if err != nil {
		return ranSetup{}, err
	}

	if req.DefaultPagingDRX == nil {
		return ranSetup{}, errors.New("missing Default Paging DRX")
	}

	s := ranSetup{globalENBID: id, tas: tas, drx: *req.DefaultPagingDRX}
	if req.ENBName != nil {
		s.name = *req.ENBName
	}

	return s, nil
}

// TS 36.413 clause 8.7.3
func (g *Gateway) handleS1Setup(ctx context.Context, n *RANAssociation, value []byte) {
	g.mu.Lock()
	wasUp := n.up
	n.up = false
	g.mu.Unlock()

	if wasUp {
		g.cfg.Relay.RANLost(n, causeEUTRANRelease)
	}

	req, err := s1ap.ParseS1SetupRequest(value)
	if err != nil {
		if ase, ok := errors.AsType[*s1ap.AbstractSyntaxError](err); ok {
			diag := ase.OutcomeDiagnostics()
			g.rejectS1Setup(ctx, n, setupInvalid, ase.Cause, nil, &diag, err.Error())

			return
		}

		g.rejectS1Setup(ctx, n, setupInvalid, causeMiscUnspecified, nil, nil, err.Error())

		return
	}

	setup, err := ranSetupFrom(req)
	if err != nil {
		g.rejectS1Setup(ctx, n, setupInvalid, causeSemanticError, nil, nil, err.Error())
		return
	}

	g.mu.Lock()

	serving := servingOperators(setup.tas, g.operators)
	if len(serving) == 0 {
		g.mu.Unlock()
		g.rejectS1Setup(ctx, n, setupUnknownPLMN, causeUnknownPLMN, nil, nil, "no operator serves a broadcast PLMN")

		return
	}

	if n.mmeConfig.timer != nil {
		n.mmeConfig.timer.Stop()
	}

	n.setup, n.up, n.mmeConfig = &setup, false, mmeConfigPush{}

	g.kickMMEsLocked()
	g.mu.Unlock()

	g.log.InfoContext(ctx, "S1 Setup Request", slog.String("enb_name", setup.name),
		slog.String("global_enb_id", formatGlobalENBID(setup.globalENBID)), slog.Any("tnl_addresses", n.addrs))

	start := g.cfg.Now()
	timer := time.NewTimer(g.cfg.SetupWait)

	defer timer.Stop()

	for {
		g.mu.Lock()

		if g.ranNodes[n.conn] != n {
			g.mu.Unlock()
			g.countSetup(sideRadio, setupError)

			return
		}

		if served := g.servedGUMMEIsLocked(); len(served) > 0 && g.anyMMEUpLocked(serving) {
			g.acceptS1SetupLocked(ctx, n, served, req.Diagnostics())
			g.mu.Unlock()

			return
		}

		cause, ttw, failed := g.mmeSetupFailureLocked(serving, start)
		changed := g.changed
		g.mu.Unlock()

		if failed {
			g.rejectS1Setup(ctx, n, setupMMEFailure, cause, ttw, nil, "S1 Setup toward the operator's MMEs failed: "+cause.String())
			return
		}

		select {
		case <-changed:
		case <-timer.C:
			g.mu.Lock()
			cause, ttw, failed := g.mmeSetupFailureLocked(serving, time.Time{})
			g.mu.Unlock()

			if failed {
				g.rejectS1Setup(ctx, n, setupMMEFailure, cause, ttw, nil, "S1 Setup toward the operator's MMEs failed: "+cause.String())
				return
			}

			g.rejectS1Setup(ctx, n, setupNoMME, causeTransportUnavailable, s1ap.Ptr(noMMETimeToWait), nil,
				"no MME of the serving operators is up")

			return
		case <-ctx.Done():
			g.countSetup(sideRadio, setupError)
			return
		}
	}
}

func (g *Gateway) anyMMEUpLocked(operators []int64) bool {
	for _, c := range g.mmes {
		if c.status.State == MMEUp && slices.Contains(operators, c.operatorID) {
			return true
		}
	}

	return false
}

func (g *Gateway) mmeSetupFailureLocked(operators []int64, since time.Time) (s1ap.Cause, *s1ap.TimeToWait, bool) {
	var (
		cause s1ap.Cause
		ttw   *s1ap.TimeToWait
		found bool
	)

	for _, key := range sortedKeys(g.mmes) {
		c := g.mmes[key]
		if !slices.Contains(operators, c.operatorID) {
			continue
		}

		if c.status.State != MMESetupFailed || c.lastCause == nil || c.status.Since.Before(since) {
			return s1ap.Cause{}, nil, false
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

func (g *Gateway) acceptS1SetupLocked(ctx context.Context, n *RANAssociation, served []ServedGUMMEIs, d s1ap.Diagnostics) {
	gummeis, err := gummeisTo(served)
	if err != nil {
		g.countSetup(sideRadio, setupError)
		g.log.ErrorContext(ctx, "failed to encode Served GUMMEIs", slog.Any("error", err))

		return
	}

	out, err := (&s1ap.S1SetupResponse{
		ServedGUMMEIs:          gummeis,
		RelativeMMECapacity:    s1ap.Ptr(relativeMMECapacity),
		CriticalityDiagnostics: responseDiagnostics(s1ap.ProcS1Setup, d),
	}).Marshal()
	if err != nil {
		g.countSetup(sideRadio, setupError)
		g.log.ErrorContext(ctx, "failed to marshal S1 Setup Response", slog.Any("error", err))

		return
	}

	if err := n.Send(out, nonUEStream); err != nil {
		g.countSetup(sideRadio, setupError)
		g.log.WarnContext(ctx, "failed to send S1 Setup Response", slog.Any("error", err))

		return
	}

	n.up = true
	n.mmeConfig.acked = served

	g.countSetup(sideRadio, setupSuccess)

	g.log.InfoContext(ctx, "radio S1 Setup accepted", slog.String("enb_name", n.setup.name),
		slog.String("global_enb_id", formatGlobalENBID(n.setup.globalENBID)))
}

func (g *Gateway) rejectS1Setup(ctx context.Context, n *RANAssociation, result string, cause s1ap.Cause,
	ttw *s1ap.TimeToWait, diag *s1ap.CriticalityDiagnostics, reason string,
) {
	g.mu.Lock()

	if n.setup != nil {
		g.kickMMEsLocked()
	}

	n.setup, n.up = nil, false
	g.mu.Unlock()

	g.countSetup(sideRadio, result)

	g.log.WarnContext(ctx, "radio S1 Setup rejected", slog.String("cause", cause.String()), slog.String("reason", reason),
		slog.Any("tnl_addresses", n.addrs))

	out, err := (&s1ap.S1SetupFailure{Cause: &cause, TimeToWait: ttw, CriticalityDiagnostics: diag}).Marshal()
	if err != nil {
		g.log.ErrorContext(ctx, "failed to marshal S1 Setup Failure", slog.Any("error", err))
		return
	}

	if err := n.Send(out, nonUEStream); err != nil {
		g.log.WarnContext(ctx, "failed to send S1 Setup Failure", slog.Any("error", err))
	}
}

// TS 36.413 clause 8.7.4
func (g *Gateway) handleENBConfigurationUpdate(ctx context.Context, n *RANAssociation, value []byte) {
	g.mu.Lock()
	up := n.up
	g.mu.Unlock()

	if !up {
		g.rejectENBConfigurationUpdate(ctx, n, causeNotCompatibleWithState, nil, "no S1 Setup")
		return
	}

	upd, err := s1ap.ParseENBConfigurationUpdate(value)
	if err != nil {
		if ase, ok := errors.AsType[*s1ap.AbstractSyntaxError](err); ok {
			diag := ase.OutcomeDiagnostics()
			g.rejectENBConfigurationUpdate(ctx, n, ase.Cause, &diag, err.Error())

			return
		}

		g.rejectENBConfigurationUpdate(ctx, n, causeMiscUnspecified, nil, err.Error())

		return
	}

	var tas []TA

	if len(upd.SupportedTAs) > 0 {
		if tas, err = tasFrom(upd.SupportedTAs); err != nil {
			g.rejectENBConfigurationUpdate(ctx, n, causeSemanticError, nil, err.Error())
			return
		}
	}

	out, err := (&s1ap.ENBConfigurationUpdateAcknowledge{
		CriticalityDiagnostics: responseDiagnostics(s1ap.ProcENBConfigurationUpdate, upd.Diagnostics()),
	}).Marshal()
	if err != nil {
		g.log.ErrorContext(ctx, "failed to marshal eNB Configuration Update Acknowledge", slog.Any("error", err))
		return
	}

	g.mu.Lock()

	if tas != nil && len(servingOperators(tas, g.operators)) == 0 {
		g.mu.Unlock()
		g.rejectENBConfigurationUpdate(ctx, n, causeUnknownPLMN, nil, "no operator serves a broadcast PLMN")

		return
	}

	if n.setup == nil {
		g.mu.Unlock()
		return
	}

	s := *n.setup

	if upd.ENBName != nil {
		s.name = *upd.ENBName
	}

	if tas != nil {
		s.tas = tas
	}

	if upd.DefaultPagingDRX != nil {
		s.drx = *upd.DefaultPagingDRX
	}

	n.setup = &s

	if err := n.Send(out, nonUEStream); err != nil {
		g.log.WarnContext(ctx, "failed to send eNB Configuration Update Acknowledge", slog.Any("error", err))
	}

	g.kickMMEsLocked()
	g.mu.Unlock()

	g.log.InfoContext(ctx, "radio configuration updated", slog.String("enb_name", s.name),
		slog.String("global_enb_id", formatGlobalENBID(s.globalENBID)))
}

func (g *Gateway) rejectENBConfigurationUpdate(ctx context.Context, n *RANAssociation, cause s1ap.Cause,
	diag *s1ap.CriticalityDiagnostics, reason string,
) {
	g.log.WarnContext(ctx, "radio eNB Configuration Update rejected", slog.String("cause", cause.String()),
		slog.String("reason", reason), slog.Any("tnl_addresses", n.addrs))

	out, err := (&s1ap.ENBConfigurationUpdateFailure{Cause: &cause, CriticalityDiagnostics: diag}).Marshal()
	if err != nil {
		g.log.ErrorContext(ctx, "failed to marshal eNB Configuration Update Failure", slog.Any("error", err))
		return
	}

	if err := n.Send(out, nonUEStream); err != nil {
		g.log.WarnContext(ctx, "failed to send eNB Configuration Update Failure", slog.Any("error", err))
	}
}

// TS 36.413 clause 8.7.5
func (g *Gateway) syncRANLocked(n *RANAssociation) {
	p := &n.mmeConfig
	if !n.up || p.pending != nil || g.cfg.Now().Before(p.retryAt) {
		return
	}

	served := g.servedGUMMEIsLocked()
	if len(served) == 0 || gummeisEqual(served, p.acked) {
		return
	}

	g.sendMMEConfigurationUpdateLocked(n, served)
}

func (g *Gateway) sendMMEConfigurationUpdateLocked(n *RANAssociation, served []ServedGUMMEIs) {
	gummeis, err := gummeisTo(served)
	if err != nil {
		g.log.Error("failed to encode Served GUMMEIs", slog.Any("error", err))
		return
	}

	out, err := (&s1ap.MMEConfigurationUpdate{ServedGUMMEIs: gummeis}).Marshal()
	if err != nil {
		g.log.Error("failed to marshal MME Configuration Update", slog.Any("error", err))
		return
	}

	if err := n.Send(out, nonUEStream); err != nil {
		g.log.Warn("failed to send MME Configuration Update", slog.Any("error", err))
		return
	}

	n.mmeConfig.pending = served
	n.mmeConfig.timer = time.AfterFunc(g.cfg.ResponseTimeout, func() {
		g.mu.Lock()
		defer g.mu.Unlock()

		if g.ranNodes[n.conn] == n && n.up && gummeisEqual(n.mmeConfig.pending, served) {
			g.sendMMEConfigurationUpdateLocked(n, served)
		}
	})
}

func (g *Gateway) handleMMEConfigurationUpdateAcknowledge(n *RANAssociation) {
	g.mu.Lock()
	defer g.mu.Unlock()

	p := &n.mmeConfig
	if p.pending == nil {
		return
	}

	if p.timer != nil {
		p.timer.Stop()
	}

	p.acked, p.pending = p.pending, nil

	g.syncRANLocked(n)
}

func (g *Gateway) handleMMEConfigurationUpdateFailure(ctx context.Context, n *RANAssociation, value []byte) {
	fail, err := s1ap.ParseMMEConfigurationUpdateFailure(value)

	retry := g.cfg.RetryInterval

	if err == nil && fail.TimeToWait != nil {
		retry = timeToWait[*fail.TimeToWait]
	}

	cause := "undecodable"
	if err == nil {
		cause = causeText(fail.Cause)
	}

	g.log.WarnContext(ctx, "radio rejected MME Configuration Update", slog.String("cause", cause),
		slog.Any("tnl_addresses", n.addrs))

	g.mu.Lock()
	defer g.mu.Unlock()

	p := &n.mmeConfig
	if p.pending == nil {
		return
	}

	if p.timer != nil {
		p.timer.Stop()
	}

	p.pending = nil
	p.retryAt = g.cfg.Now().Add(retry)
	p.timer = time.AfterFunc(retry, func() {
		g.mu.Lock()
		defer g.mu.Unlock()

		if g.ranNodes[n.conn] == n {
			g.syncRANLocked(n)
		}
	})
}

func formatGlobalENBID(id db.GlobalENBID) string {
	return fmt.Sprintf("%s-%s-%d", id.PLMN, id.Type, id.ID)
}
