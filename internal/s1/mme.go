package s1

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/db"
)

const (
	sideRAN        = "ran"
	sideMME        = "mme"
	outcomeSuccess = "success"
	outcomeFailure = "failure"
	dialTimeout    = 10 * time.Second
)

type mmeClient struct {
	g          *Gateway
	operatorID int64
	enbID      db.GlobalENBID
	addrs      []string
	cancel     context.CancelFunc
	kicks      chan struct{}

	status         MMEStatus
	lastCause      *s1ap.Cause
	lastTimeToWait *s1ap.TimeToWait
	assoc          *MMEAssociation
}

type MMEAssociation struct {
	client *mmeClient
	conn   *sctp.SCTPConn
}

func (a *MMEAssociation) Send(msg []byte, stream uint16) error {
	return send(a.conn, msg, stream)
}

func (a *MMEAssociation) OperatorID() int64 {
	return a.client.operatorID
}

func (a *MMEAssociation) TNLAddresses() []string {
	return slices.Clone(a.client.addrs)
}

type procedure int

const (
	procNone procedure = iota
	procSetup
	procUpdate
)

type mmeSession struct {
	assoc     *MMEAssociation
	setupDone bool
	acked     enbConfig
	pending   procedure
	sent      enbConfig
	deadline  time.Time
	retryAt   time.Time
}

func newMMEClient(g *Gateway, operatorID int64, enbID db.GlobalENBID, addrs []string) *mmeClient {
	return &mmeClient{
		g: g, operatorID: operatorID, enbID: enbID, addrs: slices.Clone(addrs), kicks: make(chan struct{}, 1),
		status: MMEStatus{State: MMEDisconnected, Since: g.cfg.Now()},
	}
}

func (c *mmeClient) kick() {
	select {
	case c.kicks <- struct{}{}:
	default:
	}
}

func (c *mmeClient) snapshotLocked() MMEStatus {
	s := c.status
	s.ServedGUMMEIs = slices.Clone(s.ServedGUMMEIs)

	if c.assoc != nil {
		if addr, err := c.assoc.conn.PrimaryPeerAddr(); err == nil {
			s.PrimaryTNLAddress = addr.String()
		}
	}

	return s
}

func (c *mmeClient) logger() *slog.Logger {
	return c.g.log.With(slog.Int64("operator_id", c.operatorID), slog.Any("mme", c.addrs))
}

func (c *mmeClient) setState(state MMEState) {
	if c.status.State != state {
		c.status.State = state
		c.status.Since = c.g.cfg.Now()
	}
}

func (c *mmeClient) run(ctx context.Context) {
	log := c.logger()
	backoff := c.g.cfg.ReconnectMin

	for {
		conn, err := c.dial(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}

			c.g.mu.Lock()
			c.status.LastFailureCause = "SCTP: " + err.Error()
			c.g.mu.Unlock()

			log.DebugContext(ctx, "MME association failed", slog.Any("error", err))

			if !sleep(ctx, backoff) {
				return
			}

			backoff = min(2*backoff, c.g.cfg.ReconnectMax)

			continue
		}

		assoc := &MMEAssociation{client: c, conn: conn}

		c.g.mu.Lock()
		c.assoc = assoc
		c.setState(MMEConnected)
		c.g.mu.Unlock()

		log.InfoContext(ctx, "MME association up")

		start := c.g.cfg.Now()
		err = c.session(ctx, assoc)

		if ctx.Err() != nil {
			_ = conn.Close()
		} else {
			_ = conn.Abort()
		}

		c.g.mu.Lock()

		wasUp := c.status.State == MMEUp
		c.assoc = nil
		c.status = MMEStatus{State: MMEDisconnected, Since: c.g.cfg.Now(), LastFailureCause: c.status.LastFailureCause}

		if err != nil {
			c.status.LastFailureCause = err.Error()
		}

		if wasUp {
			c.g.mmeChangedLocked()
		}

		c.g.mu.Unlock()

		c.g.cfg.Relay.MMELost(assoc)

		if ctx.Err() != nil {
			return
		}

		log.WarnContext(ctx, "MME association lost", slog.Any("error", err))

		if c.g.cfg.Now().Sub(start) > c.g.cfg.ReconnectMax {
			backoff = c.g.cfg.ReconnectMin
		}

		if !sleep(ctx, backoff) {
			return
		}

		backoff = min(2*backoff, c.g.cfg.ReconnectMax)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// TS 36.412 clause 7
func (c *mmeClient) dial(ctx context.Context) (*sctp.SCTPConn, error) {
	raddr := &sctp.SCTPAddr{Port: c.g.cfg.MMEPort}

	for _, a := range c.addrs {
		ip, err := netip.ParseAddr(a)
		if err != nil {
			return nil, fmt.Errorf("invalid TNL address %q", a)
		}

		raddr.IPAddrs = append(raddr.IPAddrs, net.IPAddr{IP: ip.AsSlice()})
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	d := sctp.Dialer{InitMsg: sctp.InitMsg{NumOstreams: mmeStreams, MaxInstreams: mmeStreams}, Logger: c.g.log}

	return d.Dial(dialCtx, raddr)
}

func (c *mmeClient) session(ctx context.Context, assoc *MMEAssociation) error {
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

	s := &mmeSession{assoc: assoc}
	timer := time.NewTimer(time.Hour)

	defer timer.Stop()

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

func (c *mmeClient) nextWake(s *mmeSession) time.Duration {
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

func (c *mmeClient) step(ctx context.Context, s *mmeSession) error {
	now := c.g.cfg.Now()

	if s.pending != procNone {
		if now.Before(s.deadline) {
			return nil
		}

		if s.pending == procSetup {
			return errors.New("no response to S1 Setup Request")
		}

		return errors.New("no response to eNB Configuration Update")
	}

	if now.Before(s.retryAt) {
		return nil
	}

	c.g.mu.Lock()
	want, ok := c.g.enbConfigLocked(c.operatorID)
	c.g.mu.Unlock()

	if !ok || (s.setupDone && want.equal(s.acked)) {
		return nil
	}

	if !s.setupDone {
		return c.sendS1Setup(ctx, s, want)
	}

	return c.sendENBConfigurationUpdate(ctx, s, want)
}

// TS 36.413 clause 8.7.3
func (c *mmeClient) sendS1Setup(ctx context.Context, s *mmeSession, want enbConfig) error {
	id, err := globalENBIDTo(c.enbID)
	if err != nil {
		return err
	}

	tas, err := tasTo(want.tas)
	if err != nil {
		return err
	}

	out, err := (&s1ap.S1SetupRequest{GlobalENBID: id, SupportedTAs: tas, DefaultPagingDRX: s1ap.Ptr(want.drx)}).Marshal()
	if err != nil {
		return fmt.Errorf("marshal S1 Setup Request: %w", err)
	}

	if err := s.assoc.Send(out, nonUEStream); err != nil {
		return fmt.Errorf("SCTP: %w", err)
	}

	c.logger().InfoContext(ctx, "S1 Setup Request sent to MME", slog.Int("supported_tas", len(want.tas)),
		slog.Int("default_paging_drx", pagingDRXFrames(want.drx)))

	s.pending, s.sent, s.deadline = procSetup, want, c.g.cfg.Now().Add(c.g.cfg.ResponseTimeout)

	return nil
}

// TS 36.413 clause 8.7.4
func (c *mmeClient) sendENBConfigurationUpdate(ctx context.Context, s *mmeSession, want enbConfig) error {
	upd := &s1ap.ENBConfigurationUpdate{}

	if !tasEqual(want.tas, s.acked.tas) {
		tas, err := tasTo(want.tas)
		if err != nil {
			return err
		}

		upd.SupportedTAs = tas
	}

	if want.drx != s.acked.drx {
		upd.DefaultPagingDRX = s1ap.Ptr(want.drx)
	}

	out, err := upd.Marshal()
	if err != nil {
		return fmt.Errorf("marshal eNB Configuration Update: %w", err)
	}

	if err := s.assoc.Send(out, nonUEStream); err != nil {
		return fmt.Errorf("SCTP: %w", err)
	}

	c.logger().InfoContext(ctx, "eNB Configuration Update sent to MME", slog.Int("supported_tas", len(want.tas)),
		slog.Int("default_paging_drx", pagingDRXFrames(want.drx)))

	s.pending, s.sent, s.deadline = procUpdate, want, c.g.cfg.Now().Add(c.g.cfg.ResponseTimeout)

	return nil
}

func (c *mmeClient) handle(ctx context.Context, s *mmeSession, msg []byte) error {
	pdu, err := s1ap.Unmarshal(msg)
	if err != nil {
		c.logger().WarnContext(ctx, "undecodable S1AP message from MME", slog.Any("error", err))
		return nil
	}

	switch p := pdu.(type) {
	case *s1ap.SuccessfulOutcome:
		switch p.ProcedureCode {
		case s1ap.ProcS1Setup:
			return c.handleS1SetupResponse(ctx, s, p.Value)
		case s1ap.ProcENBConfigurationUpdate:
			if s.pending == procUpdate {
				s.pending, s.acked = procNone, s.sent
			}

			return nil
		}
	case *s1ap.UnsuccessfulOutcome:
		switch p.ProcedureCode {
		case s1ap.ProcS1Setup:
			return c.handleS1SetupFailure(ctx, s, p.Value)
		case s1ap.ProcENBConfigurationUpdate:
			c.handleENBConfigurationUpdateFailure(ctx, s, p.Value)
			return nil
		}
	case *s1ap.InitiatingMessage:
		if s.setupDone && p.ProcedureCode == s1ap.ProcMMEConfigurationUpdate {
			c.handleMMEConfigurationUpdate(ctx, s, p.Value)
			return nil
		}
	}

	if !s.setupDone {
		c.logger().DebugContext(ctx, "dropped S1AP message before S1 Setup",
			slog.String("procedure", procedureOf(pdu).String()))

		return nil
	}

	c.g.cfg.Relay.FromMME(ctx, s.assoc, pdu)

	return nil
}

func (c *mmeClient) handleS1SetupResponse(ctx context.Context, s *mmeSession, value []byte) error {
	if s.pending != procSetup {
		return nil
	}

	resp, err := s1ap.ParseS1SetupResponse(value)
	if err != nil {
		c.g.setups.WithLabelValues(sideMME, outcomeFailure).Inc()
		return fmt.Errorf("S1 Setup Response: %w", err)
	}

	served, err := gummeisFrom(resp.ServedGUMMEIs)
	if err != nil {
		c.g.setups.WithLabelValues(sideMME, outcomeFailure).Inc()
		return fmt.Errorf("S1 Setup Response: %w", err)
	}

	s.pending, s.setupDone, s.acked = procNone, true, s.sent

	c.g.mu.Lock()

	c.status.MMEName = ""
	if resp.MMEName != nil {
		c.status.MMEName = *resp.MMEName
	}

	c.status.ServedGUMMEIs = served
	c.status.RelativeMMECapacity = resp.RelativeMMECapacity
	c.status.LastFailureCause, c.status.TimeToWait = "", 0
	c.lastCause, c.lastTimeToWait = nil, nil
	c.setState(MMEUp)
	c.g.mmeChangedLocked()
	c.g.mu.Unlock()

	c.g.setups.WithLabelValues(sideMME, outcomeSuccess).Inc()
	c.logger().InfoContext(ctx, "MME S1 Setup succeeded", slog.String("mme_name", c.status.MMEName))

	return nil
}

func (c *mmeClient) handleS1SetupFailure(ctx context.Context, s *mmeSession, value []byte) error {
	if s.pending != procSetup {
		return nil
	}

	s.pending = procNone

	c.g.setups.WithLabelValues(sideMME, outcomeFailure).Inc()

	fail, err := s1ap.ParseS1SetupFailure(value)
	if err != nil {
		return fmt.Errorf("S1 Setup Failure: %w", err)
	}

	retry := c.g.cfg.RetryInterval
	if fail.TimeToWait != nil {
		retry = timeToWait[*fail.TimeToWait]
	}

	s.retryAt = c.g.cfg.Now().Add(retry)

	c.g.mu.Lock()

	c.lastCause, c.lastTimeToWait = fail.Cause, fail.TimeToWait
	c.status.LastFailureCause = causeText(fail.Cause)
	c.status.TimeToWait = 0

	if fail.TimeToWait != nil {
		c.status.TimeToWait = retry
	}

	c.status.State, c.status.Since = MMESetupFailed, c.g.cfg.Now()
	c.g.mmeChangedLocked()
	c.g.mu.Unlock()

	c.logger().WarnContext(ctx, "MME rejected S1 Setup", slog.String("cause", causeText(fail.Cause)),
		slog.Duration("retry_in", retry))

	return nil
}

func (c *mmeClient) handleENBConfigurationUpdateFailure(ctx context.Context, s *mmeSession, value []byte) {
	if s.pending != procUpdate {
		return
	}

	s.pending = procNone
	retry := c.g.cfg.RetryInterval
	cause := "undecodable"

	if fail, err := s1ap.ParseENBConfigurationUpdateFailure(value); err == nil {
		cause = causeText(fail.Cause)

		if fail.TimeToWait != nil {
			retry = timeToWait[*fail.TimeToWait]
		}
	}

	s.retryAt = c.g.cfg.Now().Add(retry)

	c.g.mu.Lock()
	c.status.LastFailureCause = cause
	c.g.mu.Unlock()

	c.logger().WarnContext(ctx, "MME rejected eNB Configuration Update", slog.String("cause", cause),
		slog.Duration("retry_in", retry))
}

// TS 36.413 clause 8.7.5
func (c *mmeClient) handleMMEConfigurationUpdate(ctx context.Context, s *mmeSession, value []byte) {
	upd, err := s1ap.ParseMMEConfigurationUpdate(value)

	var served []ServedGUMMEIs

	if err == nil && len(upd.ServedGUMMEIs) > 0 {
		served, err = gummeisFrom(upd.ServedGUMMEIs)
	}

	if err != nil {
		cause := causeSemanticError

		var diag *s1ap.CriticalityDiagnostics

		if ase, ok := errors.AsType[*s1ap.AbstractSyntaxError](err); ok {
			cause = ase.Cause
			diag = s1ap.Ptr(ase.OutcomeDiagnostics())
		}

		c.logger().WarnContext(ctx, "rejected MME Configuration Update", slog.Any("error", err))

		if out, err := (&s1ap.MMEConfigurationUpdateFailure{Cause: &cause, CriticalityDiagnostics: diag}).Marshal(); err == nil {
			_ = s.assoc.Send(out, nonUEStream)
		}

		return
	}

	out, err := (&s1ap.MMEConfigurationUpdateAcknowledge{
		CriticalityDiagnostics: responseDiagnostics(s1ap.ProcMMEConfigurationUpdate, upd.Diagnostics()),
	}).Marshal()
	if err != nil {
		c.logger().ErrorContext(ctx, "failed to marshal MME Configuration Update Acknowledge", slog.Any("error", err))
		return
	}

	c.g.mu.Lock()

	if upd.MMEName != nil {
		c.status.MMEName = *upd.MMEName
	}

	if served != nil {
		c.status.ServedGUMMEIs = served
	}

	if upd.RelativeMMECapacity != nil {
		c.status.RelativeMMECapacity = upd.RelativeMMECapacity
	}

	c.g.mmeChangedLocked()
	c.g.mu.Unlock()

	if err := s.assoc.Send(out, nonUEStream); err != nil {
		c.logger().WarnContext(ctx, "failed to send MME Configuration Update Acknowledge", slog.Any("error", err))
	}

	c.logger().InfoContext(ctx, "MME configuration updated")
}
