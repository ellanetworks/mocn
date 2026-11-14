package ng

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/ports"
)

// TS 38.412 clause 7
const PPID = 60

var names = assoc.Names{
	Interface:   metrics.InterfaceNG,
	Server:      "NG-C",
	Protocol:    protocolName,
	Tracer:      tracerName,
	PPID:        PPID,
	CorePort:    ports.NG,
	CoreStreams: 8,
	Core:        "AMF",
	CoreAttr:    "amf",
	Setup:       "NG Setup",
	Update:      "RAN Configuration Update",
	RadioUpdate: "AMF Configuration Update",
	NoCore:      "no_amf",
	CoreFailure: "amf_failure",
	Kill:        "pws_cancel",
}

type (
	gateway    = assoc.Gateway[ranSetup, amfConfig, amfInfo, gnbConfig]
	ranAssoc   = assoc.Radio[ranSetup, amfConfig]
	amfAssoc   = assoc.Core
	amfClient  = assoc.Client[amfInfo, gnbConfig]
	amfSession = assoc.Session[ranSetup, amfConfig, amfInfo, gnbConfig]
)

type Config = assoc.Config

type Gateway struct {
	core    *gateway
	cfg     assoc.Config
	log     *slog.Logger
	relay   *relay
	writes  *writes
	cancels *cancels
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
	g.writes = assoc.NewWarnings[warningKey, *ngap.BroadcastCompletedAreaList](g.cfg.WarningWait)
	g.cancels = assoc.NewWarnings[warningKey, *ngap.BroadcastCancelledAreaList](g.cfg.WarningWait)

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

func (g *Gateway) AMFStatus(operatorID int64, tnlAddresses []string) (st AMFStatus, ok bool) {
	g.core.View(func() {
		var c *amfClient
		if c, ok = g.core.ClientLocked(operatorID, tnlAddresses); ok {
			st = amfStatusLocked(c)
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

func (protocol) Nodes(o db.Operator) []assoc.Node[gnbConfig] {
	if o.GlobalGNBID == nil || len(o.AMFs) == 0 {
		return nil
	}

	gnbID := *o.GlobalGNBID
	nodes := make([]assoc.Node[gnbConfig], 0, len(o.AMFs))

	for _, m := range o.AMFs {
		nodes = append(nodes, assoc.Node[gnbConfig]{
			Key:   fmt.Sprintf("%d|%s|%d|%d|%s", o.ID, gnbID.PLMN, gnbID.Length, gnbID.ID, strings.Join(m.TNLAddresses, ",")),
			Addrs: m.TNLAddresses,
			SetupRequest: func(want gnbConfig) ([]byte, error) {
				return ngSetupRequest(gnbID, want)
			},
		})
	}

	return nodes
}

func (p protocol) UEsByOperator() map[int64]int {
	return p.relay.countByOperator()
}

func responseDiagnostics(proc ngap.ProcedureCode, d ngap.Diagnostics) *ngap.CriticalityDiagnostics {
	if !d.ReportRequired() {
		return nil
	}

	return &ngap.CriticalityDiagnostics{
		ProcedureCriticality:      ngap.Ptr(ngap.ProcedureCriticality(proc)),
		IEsCriticalityDiagnostics: d.Report(),
	}
}
