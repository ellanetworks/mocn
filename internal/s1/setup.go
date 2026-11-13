package s1

import (
	"slices"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
)

const relativeMMECapacity uint8 = 255

type ranSetup struct {
	globalENBID db.GlobalENBID
	name        string
	tas         []TA
	drx         s1ap.PagingDRX
}

type operatorView struct {
	id    int64
	name  string
	plmns []db.PLMN
}

type mmePool struct {
	plmns  []db.PLMN
	served []ServedGUMMEIs
}

type enbConfig struct {
	tas []TA
	drx s1ap.PagingDRX
}

func (c enbConfig) equal(o enbConfig) bool {
	return c.drx == o.drx && tasEqual(c.tas, o.tas)
}

func tasEqual(a, b []TA) bool {
	return slices.EqualFunc(a, b, func(x, y TA) bool {
		return x.TAC == y.TAC && slices.Equal(x.BroadcastPLMNs, y.BroadcastPLMNs)
	})
}

func gummeisEqual(a, b []ServedGUMMEIs) bool {
	return slices.EqualFunc(a, b, func(x, y ServedGUMMEIs) bool {
		return slices.Equal(x.PLMNs, y.PLMNs) && slices.Equal(x.GroupIDs, y.GroupIDs) &&
			slices.Equal(x.MMECodes, y.MMECodes)
	})
}

func broadcasts(tas []TA, plmns []db.PLMN) bool {
	for _, ta := range tas {
		for _, p := range ta.BroadcastPLMNs {
			if slices.Contains(plmns, p) {
				return true
			}
		}
	}

	return false
}

// TS 36.300 clause 4.6.2
func servingOperators(tas []TA, ops []operatorView) []int64 {
	var ids []int64

	for _, o := range ops {
		if broadcasts(tas, o.plmns) {
			ids = append(ids, o.id)
		}
	}

	return ids
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
func mergeServedGUMMEIs(pools []mmePool) []ServedGUMMEIs {
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

	return []ServedGUMMEIs{merged}
}
