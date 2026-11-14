package s1

import (
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
)

const relativeMMECapacity uint8 = 255

type ranSetup struct {
	globalENBID db.GlobalENBID
	name        string
	tas         []TA
	drx         s1ap.PagingDRX
}

type mmePool struct {
	plmns  []db.PLMN
	served []ServedGUMMEIs
}

type enbConfig struct {
	tas []TA
	drx s1ap.PagingDRX
}

type servedGUMMEIs []ServedGUMMEIs

func (c enbConfig) Equal(o enbConfig) bool {
	return c.drx == o.drx && tasEqual(c.tas, o.tas)
}

func tasEqual(a, b []TA) bool {
	return slices.EqualFunc(a, b, func(x, y TA) bool {
		return x.TAC == y.TAC && slices.Equal(x.BroadcastPLMNs, y.BroadcastPLMNs)
	})
}

func (s servedGUMMEIs) Equal(o servedGUMMEIs) bool {
	return slices.EqualFunc(s, o, func(x, y ServedGUMMEIs) bool {
		return slices.Equal(x.PLMNs, y.PLMNs) && slices.Equal(x.GroupIDs, y.GroupIDs) &&
			slices.Equal(x.MMECodes, y.MMECodes)
	})
}

func (s servedGUMMEIs) Pushed() servedGUMMEIs {
	return s
}

func (s servedGUMMEIs) Empty() bool {
	return len(s) == 0
}

// TS 36.300 clause 4.6.2
func (s ranSetup) Broadcasts(plmns []db.PLMN) bool {
	for _, ta := range s.tas {
		for _, p := range ta.BroadcastPLMNs {
			if slices.Contains(plmns, p) {
				return true
			}
		}
	}

	return false
}

// TS 36.413 clause 8.5.2
// TS 36.300 clause 4.7.4
func (s ranSetup) Serves(tai assoc.TAI) bool {
	for _, ta := range s.tas {
		if uint32(ta.TAC) == tai.TAC && slices.Contains(ta.BroadcastPLMNs, tai.PLMN) {
			return true
		}
	}

	return false
}

func (protocol) CoreConfig(plmns []db.PLMN, setups []ranSetup) (enbConfig, bool) {
	return enbConfigFor(plmns, setups)
}

func (protocol) ConfigAttrs(cfg enbConfig) []slog.Attr {
	return []slog.Attr{
		slog.Int("supported_tas", len(cfg.tas)), slog.Int("default_paging_drx", pagingDRXFrames(cfg.drx)),
	}
}

func (p protocol) RadioConfigLocked() servedGUMMEIs {
	pools := []mmePool{}

	for c := range p.core.ClientsLocked() {
		if c.State != assoc.Up {
			continue
		}

		if o, ok := p.core.OperatorLocked(c.OperatorID()); ok {
			pools = append(pools, mmePool{plmns: o.PLMNs, served: c.Info.served})
		}
	}

	return mergeServedGUMMEIs(pools)
}

// TS 36.413 clause 9.1.8.4
func enbConfigFor(plmns []db.PLMN, nodes []ranSetup) (enbConfig, bool) {
	byTAC := map[uint16][]db.PLMN{}

	var (
		cfg   enbConfig
		found bool
	)

	for _, n := range nodes {
		contributes := false

		for _, ta := range n.tas {
			for _, p := range plmns {
				if !slices.Contains(ta.BroadcastPLMNs, p) {
					continue
				}

				contributes = true

				if !slices.Contains(byTAC[ta.TAC], p) {
					byTAC[ta.TAC] = append(byTAC[ta.TAC], p)
				}
			}
		}

		if contributes && (!found || n.drx > cfg.drx) {
			cfg.drx = n.drx
			found = true
		}
	}

	if !found {
		return enbConfig{}, false
	}

	tacs := make([]uint16, 0, len(byTAC))
	for tac := range byTAC {
		tacs = append(tacs, tac)
	}

	slices.Sort(tacs)

	for _, tac := range tacs[:min(len(tacs), maxnoofTACs)] {
		got := byTAC[tac]

		ordered := make([]db.PLMN, 0, len(got))
		for _, p := range plmns {
			if slices.Contains(got, p) {
				ordered = append(ordered, p)
			}
		}

		cfg.tas = append(cfg.tas, TA{TAC: tac, BroadcastPLMNs: ordered[:min(len(ordered), maxnoofBPLMNs)]})
	}

	return cfg, true
}

// TS 36.413 clause 9.1.8.5
func mergeServedGUMMEIs(pools []mmePool) servedGUMMEIs {
	var (
		merged ServedGUMMEIs
		found  bool
	)

	for _, pool := range pools {
		if len(pool.served) == 0 {
			continue
		}

		lte := pool.served[0]

		var plmns []db.PLMN

		for _, p := range lte.PLMNs {
			if slices.Contains(pool.plmns, p) {
				plmns = append(plmns, p)
			}
		}

		if len(plmns) == 0 || len(lte.GroupIDs) == 0 || len(lte.MMECodes) == 0 {
			continue
		}

		found = true

		for _, p := range plmns {
			if !slices.Contains(merged.PLMNs, p) && len(merged.PLMNs) < maxnoofPLMNsPerMME {
				merged.PLMNs = append(merged.PLMNs, p)
			}
		}

		merged.GroupIDs = append(merged.GroupIDs, lte.GroupIDs...)
		merged.MMECodes = append(merged.MMECodes, lte.MMECodes...)
	}

	if !found {
		return nil
	}

	slices.Sort(merged.GroupIDs)
	merged.GroupIDs = slices.Compact(merged.GroupIDs)
	merged.GroupIDs = merged.GroupIDs[:min(len(merged.GroupIDs), maxnoofGroupIDs)]

	slices.Sort(merged.MMECodes)
	merged.MMECodes = slices.Compact(merged.MMECodes)

	return servedGUMMEIs{merged}
}

func (s ranSetup) Ref() string {
	return s.globalENBID.Ref()
}
