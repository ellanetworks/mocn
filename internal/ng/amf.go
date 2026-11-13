package ng

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync/atomic"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gw"
	"github.com/ellanetworks/mocn/internal/tracing"
)

const dialTimeout = 10 * time.Second

type amfClient struct {
	g          *Gateway
	operatorID int64
	opName     atomic.Pointer[string]
	gnbID      db.GlobalGNBID
	addrs      []string
	cancel     context.CancelFunc
	kicks      chan struct{}
	done       chan struct{}

	status         AMFStatus
	lastCause      *ngap.Cause
	lastTimeToWait *ngap.TimeToWait
	assoc          *AMFAssociation
}

type AMFAssociation struct {
	client *amfClient
	conn   *sctp.SCTPConn
	lost   bool
}

func (a *AMFAssociation) Send(ctx context.Context, msg []byte, stream uint16) error {
	return a.client.g.send(ctx, sideCore, a.conn, a.client.operator(), msg, stream)
}

func (a *AMFAssociation) TNLAddresses() []string {
	return slices.Clone(a.client.addrs)
}

type procedure int

const (
	procNone procedure = iota
	procSetup
	procUpdate
)

type amfSession struct {
	assoc     *AMFAssociation
	setupDone bool
	acked     gnbConfig
	pending   procedure
	sent      gnbConfig
	sentMsg   []byte
	deadline  time.Time
	retryAt   time.Time
}

func newAMFClient(g *Gateway, operatorID int64, operator string, gnbID db.GlobalGNBID, addrs []string) *amfClient {
	c := &amfClient{
		g: g, operatorID: operatorID, gnbID: gnbID, addrs: slices.Clone(addrs), kicks: make(chan struct{}, 1),
		done:   make(chan struct{}),
		status: AMFStatus{State: AMFDisconnected, Since: g.cfg.Now()},
	}

	c.setOperatorName(operator)

	return c
}

func (c *amfClient) setOperatorName(name string) {
	c.opName.Store(&name)
}

func (c *amfClient) operator() string {
	if name := c.opName.Load(); name != nil {
		return *name
	}

	return ""
}

func (c *amfClient) kick() {
	select {
	case c.kicks <- struct{}{}:
	default:
	}
}

func (c *amfClient) snapshotLocked() AMFStatus {
	s := c.status
	s.ServedGUAMIs = slices.Clone(s.ServedGUAMIs)
	s.PLMNSupport = slices.Clone(s.PLMNSupport)
	s.UnavailableGUAMIs = slices.Clone(s.UnavailableGUAMIs)

	if s.Overload != nil {
		o := *s.Overload
		o.Slices = slices.Clone(o.Slices)
		s.Overload = &o
	}

	if c.assoc != nil {
		if addr, err := c.assoc.conn.PrimaryPeerAddr(); err == nil {
			s.PrimaryTNLAddress = addr.String()
		}
	}

	return s
}

func (c *amfClient) logger() *slog.Logger {
	return c.g.log.With(slog.Int64("operator_id", c.operatorID), slog.Any("amf", c.addrs))
}

func (c *amfClient) setState(state AMFState) {
	if c.status.State != state {
		c.status.State = state
		c.status.Since = c.g.cfg.Now()
	}
}

func (c *amfClient) run(ctx context.Context) {
	log := c.logger()
	backoff := c.g.cfg.ReconnectMin

	for {
		conn, err := c.dial(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}

			log.WarnContext(ctx, "AMF association failed", slog.Any("error", err))

			if !gw.Sleep(ctx, backoff) {
				return
			}

			backoff = min(2*backoff, c.g.cfg.ReconnectMax)

			continue
		}

		assoc := &AMFAssociation{client: c, conn: conn}

		c.g.mu.Lock()
		c.assoc = assoc
		c.setState(AMFConnected)
		c.g.mu.Unlock()

		log.InfoContext(ctx, "AMF association up")

		start := c.g.cfg.Now()
		err = c.session(ctx, assoc)

		if ctx.Err() != nil {
			_ = conn.Close()
		} else {
			_ = conn.Abort()
		}

		c.g.mu.Lock()

		wasUp := c.status.State == AMFUp
		c.assoc = nil
		c.status = AMFStatus{State: AMFDisconnected, Since: c.g.cfg.Now()}

		if wasUp {
			c.g.amfChangedLocked(ctx)
		}

		c.g.mu.Unlock()

		c.g.relay.amfLost(context.WithoutCancel(ctx), assoc)

		if ctx.Err() != nil {
			return
		}

		log.WarnContext(ctx, "AMF association lost", slog.Any("error", err))

		if c.g.cfg.Now().Sub(start) > c.g.cfg.ReconnectMax {
			backoff = c.g.cfg.ReconnectMin
		}

		if !gw.Sleep(ctx, backoff) {
			return
		}

		backoff = min(2*backoff, c.g.cfg.ReconnectMax)
	}
}

// TS 38.412 clause 7
func (c *amfClient) dial(ctx context.Context) (*sctp.SCTPConn, error) {
	raddr := &sctp.SCTPAddr{Port: c.g.cfg.AMFPort}

	for _, a := range c.addrs {
		ip, err := netip.ParseAddr(a)
		if err != nil {
			return nil, fmt.Errorf("invalid TNL address %q", a)
		}

		raddr.IPAddrs = append(raddr.IPAddrs, net.IPAddr{IP: ip.AsSlice()})
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	d := sctp.Dialer{InitMsg: sctp.InitMsg{NumOstreams: amfStreams, MaxInstreams: amfStreams}, Logger: c.g.log}

	return d.Dial(dialCtx, raddr)
}

func (c *amfClient) session(ctx context.Context, assoc *AMFAssociation) error {
	msgs := make(chan []byte)
	readErr := make(chan error, 1)

	readCtx, stopReading := context.WithCancel(ctx)
	defer stopReading()

	go func() {
		buf := make([]byte, sctp.MaxMessageSize)

		for {
			n, info, err := assoc.conn.ReadMsg(buf)
			if err != nil {
				readErr <- err
				return
			}

			if info == nil || sctp.PPIDWireOrder(info.PPID) != PPID {
				continue
			}

			select {
			case msgs <- slices.Clone(buf[:n]):
			case <-readCtx.Done():
				return
			}
		}
	}()

	s := &amfSession{assoc: assoc}
	timer := time.NewTimer(time.Hour)

	defer timer.Stop()
	defer func() {
		if s.pending == procSetup {
			c.endSetup(s, setupError)
		}
	}()

	for {
		if err := c.step(ctx, s); err != nil {
			return err
		}

		timer.Reset(c.nextWake(s))

		select {
		case <-ctx.Done():
			return nil
		case err := <-readErr:
			return fmt.Errorf("SCTP: %w", err)
		case msg := <-msgs:
			if err := c.handle(ctx, s, msg); err != nil {
				return err
			}
		case <-c.kicks:
		case <-timer.C:
		}
	}
}

func (c *amfClient) nextWake(s *amfSession) time.Duration {
	now := c.g.cfg.Now()

	switch {
	case s.pending != procNone:
		return max(s.deadline.Sub(now), time.Millisecond)
	case now.Before(s.retryAt):
		return s.retryAt.Sub(now)
	default:
		return time.Hour
	}
}

func (c *amfClient) step(ctx context.Context, s *amfSession) error {
	now := c.g.cfg.Now()

	if s.pending != procNone {
		if now.Before(s.deadline) {
			return nil
		}

		if s.pending == procSetup {
			c.endSetup(s, setupTimeout)
			return errors.New("no response to NG Setup Request")
		}

		return c.resendRANConfigurationUpdate(ctx, s)
	}

	if now.Before(s.retryAt) {
		return nil
	}

	c.g.mu.Lock()
	want, ok := c.g.gnbConfigLocked(c.operatorID)
	c.g.mu.Unlock()

	if !ok || (s.setupDone && want.equal(s.acked)) {
		return nil
	}

	if !s.setupDone {
		return c.sendNGSetup(ctx, s, want)
	}

	return c.sendRANConfigurationUpdate(ctx, s, want)
}

// TS 38.413 clause 8.7.1
// TS 38.413 clause 9.2.6.1
func (c *amfClient) sendNGSetup(ctx context.Context, s *amfSession, want gnbConfig) error {
	out, err := ngSetupRequest(c.gnbID, want)
	if err != nil {
		c.g.countSetup(sideCore, setupError)
		return err
	}

	if err := s.assoc.Send(ctx, out, nonUEStream); err != nil {
		c.g.countSetup(sideCore, setupError)
		return fmt.Errorf("SCTP: %w", err)
	}

	c.logger().InfoContext(ctx, "NG Setup Request sent to AMF", slog.Int("supported_tas", len(want.tas)),
		slog.Int("default_paging_drx", pagingDRXFrames(want.drx)))

	s.pending, s.sent, s.deadline = procSetup, want, c.g.cfg.Now().Add(c.g.cfg.ResponseTimeout)

	return nil
}

// TS 38.413 clause 9.2.6.1
func ngSetupRequest(gnbID db.GlobalGNBID, want gnbConfig) ([]byte, error) {
	id, err := globalGNBIDTo(gnbID)
	if err != nil {
		return nil, err
	}

	tas, err := tasTo(want.tas)
	if err != nil {
		return nil, err
	}

	out, err := (&ngap.NGSetupRequest{
		GlobalRANNodeID: id, RANNodeName: ngap.Ptr(gw.NodeName), SupportedTAList: tas, DefaultPagingDRX: ngap.Ptr(want.drx),
	}).Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal NG Setup Request: %w", err)
	}

	return out, nil
}

func (c *amfClient) endSetup(s *amfSession, result string) {
	s.pending = procNone

	c.g.countSetup(sideCore, result)
}

// TS 38.413 clause 8.7.2
// TS 38.413 clause 9.2.6.4
func (c *amfClient) sendRANConfigurationUpdate(ctx context.Context, s *amfSession, want gnbConfig) error {
	upd := &ngap.RANConfigurationUpdate{RANNodeName: ngap.Ptr(gw.NodeName)}

	if !tasEqual(want.tas, s.acked.tas) {
		tas, err := tasTo(want.tas)
		if err != nil {
			return err
		}

		upd.SupportedTAList = tas
	}

	if want.drx != s.acked.drx {
		upd.DefaultPagingDRX = ngap.Ptr(want.drx)
	}

	out, err := upd.Marshal()
	if err != nil {
		return fmt.Errorf("marshal RAN Configuration Update: %w", err)
	}

	if err := s.assoc.Send(ctx, out, nonUEStream); err != nil {
		return fmt.Errorf("SCTP: %w", err)
	}

	c.logger().InfoContext(ctx, "RAN Configuration Update sent to AMF", slog.Int("supported_tas", len(want.tas)),
		slog.Int("default_paging_drx", pagingDRXFrames(want.drx)))

	s.pending, s.sent, s.sentMsg, s.deadline = procUpdate, want, out, c.g.cfg.Now().Add(c.g.cfg.ResponseTimeout)

	return nil
}

// TS 38.413 clause 8.7.2.4
func (c *amfClient) resendRANConfigurationUpdate(ctx context.Context, s *amfSession) error {
	if err := s.assoc.Send(ctx, s.sentMsg, nonUEStream); err != nil {
		return fmt.Errorf("SCTP: %w", err)
	}

	c.logger().WarnContext(ctx, "no response to RAN Configuration Update, sent it again")

	s.deadline = c.g.cfg.Now().Add(c.g.cfg.ResponseTimeout)

	return nil
}

func (c *amfClient) handle(ctx context.Context, s *amfSession, msg []byte) error {
	pdu, err := ngap.Unmarshal(msg)

	ctx, span := c.g.receiveFromAMF(ctx, s.assoc, pdu, msg)
	defer span.End()

	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		c.logger().WarnContext(ctx, "undecodable NGAP message from AMF", slog.Any("error", err))

		return nil
	}

	switch p := pdu.(type) {
	case *ngap.SuccessfulOutcome:
		switch p.ProcedureCode {
		case ngap.ProcNGSetup:
			return c.handleNGSetupResponse(ctx, s, p.Value)
		case ngap.ProcRANConfigurationUpdate:
			if s.pending == procUpdate {
				s.pending, s.acked = procNone, s.sent
			}

			return nil
		}
	case *ngap.UnsuccessfulOutcome:
		switch p.ProcedureCode {
		case ngap.ProcNGSetup:
			return c.handleNGSetupFailure(ctx, s, p.Value)
		case ngap.ProcRANConfigurationUpdate:
			c.handleRANConfigurationUpdateFailure(ctx, s, p.Value)
			return nil
		}
	case *ngap.InitiatingMessage:
		if s.setupDone && p.ProcedureCode == ngap.ProcAMFConfigurationUpdate {
			c.handleAMFConfigurationUpdate(ctx, s, msg)
			return nil
		}
	}

	if !s.setupDone {
		tracing.SpanFailed(ctx, errTypeNoSetup)
		c.logger().DebugContext(ctx, "dropped NGAP message before NG Setup",
			slog.String("procedure", procedureOf(pdu).String()))

		return nil
	}

	c.g.relay.fromAMF(ctx, s.assoc, pdu, msg)

	return nil
}

// TS 38.413 clause 8.7.1.1
func (c *amfClient) handleNGSetupResponse(ctx context.Context, s *amfSession, value []byte) error {
	if s.pending != procSetup {
		return nil
	}

	resp, err := ngap.ParseNGSetupResponse(value)
	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		c.endSetup(s, setupFailure)

		return fmt.Errorf("NG Setup Response: %w", err)
	}

	served, err := servedGUAMIsFrom(resp.ServedGUAMIList)
	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		c.endSetup(s, setupFailure)

		return fmt.Errorf("NG Setup Response: %w", err)
	}

	support, err := plmnSupportFrom(resp.PLMNSupportList)
	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		c.endSetup(s, setupFailure)

		return fmt.Errorf("NG Setup Response: %w", err)
	}

	s.pending, s.setupDone, s.acked = procNone, true, s.sent

	c.g.mu.Lock()

	c.status.AMFName = resp.AMFName
	c.status.ServedGUAMIs = served
	c.status.PLMNSupport = support
	c.status.RelativeAMFCapacity = resp.RelativeAMFCapacity
	c.status.UnavailableGUAMIs = nil
	c.status.Overload = nil
	c.lastCause, c.lastTimeToWait = nil, nil
	c.setState(AMFUp)
	c.g.amfChangedLocked(ctx)
	c.g.mu.Unlock()

	c.g.countSetup(sideCore, setupSuccess)
	c.logger().InfoContext(ctx, "AMF NG Setup succeeded", slog.String("amf_name", resp.AMFName))

	return nil
}

func (c *amfClient) handleNGSetupFailure(ctx context.Context, s *amfSession, value []byte) error {
	if s.pending != procSetup {
		return nil
	}

	c.endSetup(s, setupFailure)

	fail, err := ngap.ParseNGSetupFailure(value)
	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		return fmt.Errorf("NG Setup Failure: %w", err)
	}

	retry := c.g.cfg.RetryInterval
	if fail.TimeToWait != nil {
		retry = timeToWait[*fail.TimeToWait]
	}

	s.retryAt = c.g.cfg.Now().Add(retry)

	c.g.mu.Lock()

	c.lastCause, c.lastTimeToWait = fail.Cause, fail.TimeToWait
	c.status.State, c.status.Since = AMFSetupFailed, c.g.cfg.Now()
	c.g.amfChangedLocked(ctx)
	c.g.mu.Unlock()

	c.logger().WarnContext(ctx, "AMF rejected NG Setup", slog.String("cause", causeText(fail.Cause)),
		slog.Duration("retry_in", retry))

	return nil
}

func (c *amfClient) handleRANConfigurationUpdateFailure(ctx context.Context, s *amfSession, value []byte) {
	if s.pending != procUpdate {
		return
	}

	s.pending = procNone
	retry := c.g.cfg.RetryInterval
	cause := "undecodable"

	if fail, err := ngap.ParseRANConfigurationUpdateFailure(value); err == nil {
		cause = causeText(fail.Cause)

		if fail.TimeToWait != nil {
			retry = timeToWait[*fail.TimeToWait]
		}
	} else {
		tracing.SpanFailed(ctx, errTypeInvalid)
	}

	s.retryAt = c.g.cfg.Now().Add(retry)

	c.logger().WarnContext(ctx, "AMF rejected RAN Configuration Update", slog.String("cause", cause),
		slog.Duration("retry_in", retry))
}

type amfConfigurationUpdate struct {
	*ngap.AMFConfigurationUpdate
	plmnSupport ngap.PLMNSupportList
}

// TS 38.413 clause 9.2.6.7
func parseAMFConfigurationUpdate(msg []byte) (*amfConfigurationUpdate, error) {
	p, err := ngap.ParseRawPDU(msg)
	if err != nil {
		return nil, err
	}

	var out amfConfigurationUpdate

	if i := p.Index(ngap.IDPLMNSupportList); i >= 0 {
		if _, err := p.Get(ngap.IDPLMNSupportList, &out.plmnSupport); err != nil {
			return nil, err
		}

		if err := p.Remove(i); err != nil {
			return nil, err
		}

		if msg, err = p.Marshal(); err != nil {
			return nil, err
		}
	}

	pdu, err := ngap.Unmarshal(msg)
	if err != nil {
		return nil, err
	}

	m, ok := pdu.(*ngap.InitiatingMessage)
	if !ok {
		return nil, errors.New("not an initiating message")
	}

	if out.AMFConfigurationUpdate, err = ngap.ParseAMFConfigurationUpdate(m.Value); err != nil {
		return nil, err
	}

	return &out, nil
}

// TS 38.413 clause 8.7.3
// TS 23.501 clause 5.21.2.2.1
func (c *amfClient) handleAMFConfigurationUpdate(ctx context.Context, s *amfSession, msg []byte) {
	upd, err := parseAMFConfigurationUpdate(msg)

	var (
		served  []GUAMI
		support []PLMNSupport
	)

	if err == nil && len(upd.ServedGUAMIList) > 0 {
		served, err = servedGUAMIsFrom(upd.ServedGUAMIList)
	}

	if err == nil && len(upd.plmnSupport) > 0 {
		support, err = plmnSupportFrom(upd.plmnSupport)
	}

	if err != nil {
		cause := causeSemanticError

		var diag *ngap.CriticalityDiagnostics

		if ase, ok := errors.AsType[*ngap.AbstractSyntaxError](err); ok {
			cause = ase.Cause
			diag = ngap.Ptr(ase.OutcomeDiagnostics())
		}

		tracing.SpanFailed(ctx, errTypeInvalid)
		c.logger().WarnContext(ctx, "rejected AMF Configuration Update", slog.Any("error", err))

		if out, err := (&ngap.AMFConfigurationUpdateFailure{Cause: &cause, CriticalityDiagnostics: diag}).Marshal(); err == nil {
			_ = s.assoc.Send(ctx, out, nonUEStream)
		}

		return
	}

	out, err := (&ngap.AMFConfigurationUpdateAcknowledge{
		CriticalityDiagnostics: responseDiagnostics(ngap.ProcAMFConfigurationUpdate, upd.Diagnostics()),
	}).Marshal()
	if err != nil {
		c.logger().ErrorContext(ctx, "failed to marshal AMF Configuration Update Acknowledge", slog.Any("error", err))
		return
	}

	c.g.mu.Lock()

	if upd.AMFName != nil {
		c.status.AMFName = *upd.AMFName
	}

	if served != nil {
		c.status.ServedGUAMIs = served
		c.status.UnavailableGUAMIs = nil
	}

	if support != nil {
		c.status.PLMNSupport = support
	}

	if upd.RelativeAMFCapacity != nil {
		c.status.RelativeAMFCapacity = upd.RelativeAMFCapacity
	}

	c.g.amfChangedLocked(ctx)
	c.g.mu.Unlock()

	if err := s.assoc.Send(ctx, out, nonUEStream); err != nil {
		c.logger().WarnContext(ctx, "failed to send AMF Configuration Update Acknowledge", slog.Any("error", err))
	}

	c.logger().InfoContext(ctx, "AMF configuration updated")
}
