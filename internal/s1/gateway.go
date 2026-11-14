package s1

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/ports"
)

// TS 36.412 clause 7
const PPID = 18

var names = assoc.Names{
	Interface:   metrics.InterfaceS1,
	Server:      "S1-MME",
	Protocol:    protocolName,
	Tracer:      tracerName,
	PPID:        PPID,
	CorePort:    ports.S1,
	CoreStreams: 8,
	Core:        "MME",
	CoreAttr:    "mme",
	Setup:       "S1 Setup",
	Update:      "eNB Configuration Update",
	RadioUpdate: "MME Configuration Update",
	NoCore:      "no_mme",
	CoreFailure: "mme_failure",
	Kill:        "kill",
}

type (
	gateway    = assoc.Gateway[ranSetup, servedGUMMEIs, mmeInfo, enbConfig]
	ranAssoc   = assoc.Radio[ranSetup, servedGUMMEIs]
	mmeAssoc   = assoc.Core
	mmeClient  = assoc.Client[mmeInfo, enbConfig]
	mmeSession = assoc.Session[ranSetup, servedGUMMEIs, mmeInfo, enbConfig]
)

type Config = assoc.Config

type Gateway struct {
	core   *gateway
	cfg    assoc.Config
	log    *slog.Logger
	relay  *relay
	writes *writes
	kills  *kills
}

type protocol struct {
	*Gateway
}

func New(cfg Config) *Gateway {
	g := &Gateway{}
	g.core = assoc.New(cfg, names, spanInfo, protocol{g})
	g.cfg = g.core.Config()
	g.log = g.cfg.Logger
	g.relay = newRelay(g)
	g.writes = assoc.NewWarnings[warningKey, *s1ap.BroadcastCompletedAreaList](g.cfg.ResponseTimeout)
	g.kills = assoc.NewWarnings[warningKey, *s1ap.BroadcastCancelledAreaList](g.cfg.ResponseTimeout)

	return g
}

func (g *Gateway) Run(ctx context.Context) {
	g.core.Run(ctx)
}

func (g *Gateway) Reconcile(ctx context.Context) error {
	return g.core.Reconcile(ctx)
}

func (g *Gateway) MetricsState() metrics.State {
	return g.core.MetricsState()
}

func (g *Gateway) MMEStatus(operatorID int64, tnlAddresses []string) (st MMEStatus, ok bool) {
	g.core.View(func() {
		var c *mmeClient
		if c, ok = g.core.ClientLocked(operatorID, tnlAddresses); ok {
			st = mmeStatusLocked(c)
		}
	})

	return st, ok
}

func (g *Gateway) UEs() []UE {
	ues := g.relay.snapshot()
	names := g.core.OperatorNames()

	for i := range ues {
		ues[i].OperatorName = names[ues[i].OperatorID]
	}

	slices.SortStableFunc(ues, func(a, b UE) int { return a.Since.Compare(b.Since) })

	return ues
}

func (g *Gateway) RANNodes() (out []RANNode) {
	g.core.View(func() {
		radios := g.core.RadiosLocked()

		out = make([]RANNode, 0, len(radios))
		for _, n := range radios {
			out = append(out, ranNodeLocked(n))
		}
	})

	return out
}

func (protocol) Nodes(o db.Operator) []assoc.Node[enbConfig] {
	if o.GlobalENBID == nil || len(o.MMEs) == 0 {
		return nil
	}

	enbID := *o.GlobalENBID
	nodes := make([]assoc.Node[enbConfig], 0, len(o.MMEs))

	for _, m := range o.MMEs {
		nodes = append(nodes, assoc.Node[enbConfig]{
			Key:   fmt.Sprintf("%d|%s|%s|%d|%s", o.ID, enbID.PLMN, enbID.Type, enbID.ID, strings.Join(m.TNLAddresses, ",")),
			Addrs: m.TNLAddresses,
			SetupRequest: func(want enbConfig) ([]byte, error) {
				return s1SetupRequest(enbID, want)
			},
		})
	}

	return nodes
}

func (p protocol) UEsByOperator() map[int64]int {
	return p.relay.countByOperator()
}

func responseDiagnostics(proc s1ap.ProcedureCode, d s1ap.Diagnostics) *s1ap.CriticalityDiagnostics {
	if !d.ReportRequired() {
		return nil
	}

	return &s1ap.CriticalityDiagnostics{
		ProcedureCriticality:      s1ap.Ptr(s1ap.ProcedureCriticality(proc)),
		IEsCriticalityDiagnostics: d.Report(),
	}
}
