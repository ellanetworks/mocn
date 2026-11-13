package ng

import (
	"slices"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/db"
)

const relativeAMFCapacity uint8 = 255

type ranSetup struct {
	globalGNBID db.GlobalGNBID
	name        string
	tas         []TA
	drx         ngap.PagingDRX
}

type operatorView struct {
	id    int64
	name  string
	plmns []db.PLMN
}

type amfPool struct {
	plmns   []db.PLMN
	served  []GUAMI
	support []PLMNSupport
}

type gnbConfig struct {
	tas []TA
	drx ngap.PagingDRX
}

type amfConfig struct {
	served  []GUAMI
	support []PLMNSupport
}

func (c gnbConfig) equal(o gnbConfig) bool {
	return c.drx == o.drx && tasEqual(c.tas, o.tas)
}

func (c amfConfig) equal(o amfConfig) bool {
	return slices.Equal(c.served, o.served) && slices.EqualFunc(c.support, o.support, func(x, y PLMNSupport) bool {
		return x.PLMN == y.PLMN && slices.Equal(x.Slices, y.Slices)
	})
}

func (c amfConfig) empty() bool {
	return len(c.served) == 0 || len(c.support) == 0
}

func tasEqual(a, b []TA) bool {
	return slices.EqualFunc(a, b, func(x, y TA) bool {
		return x.TAC == y.TAC && slices.EqualFunc(x.BroadcastPLMNs, y.BroadcastPLMNs, func(p, q BroadcastPLMN) bool {
			return p.PLMN == q.PLMN && slices.Equal(p.Slices, q.Slices)
		})
	})
}

func broadcasts(tas []TA, plmns []db.PLMN) bool {
	for _, ta := range tas {
		for _, b := range ta.BroadcastPLMNs {
			if slices.Contains(plmns, b.PLMN) {
				return true
			}
		}
	}

	return false
}

// TS 38.300 clause 4.10.2.3
func servingOperators(tas []TA, ops []operatorView) []int64 {
	var ids []int64

	for _, o := range ops {
		if broadcasts(tas, o.plmns) {
			ids = append(ids, o.id)
		}
	}

	return ids
}

func addSlices(dst []SNSSAI, src []SNSSAI) []SNSSAI {
	for _, s := range src {
		if !slices.Contains(dst, s) && len(dst) < maxnoofSliceItems {
			dst = append(dst, s)
		}
	}

	return dst
}

// TS 38.413 clause 9.2.6.1
func gnbConfigFor(plmns []db.PLMN, nodes []ranSetup) (gnbConfig, bool) {
	type key struct {
		tac  uint32
		plmn db.PLMN
	}

	byTA := map[key][]SNSSAI{}
	tacs := map[uint32]bool{}

	var (
		cfg   gnbConfig
		found bool
	)

	for _, n := range nodes {
		contributes := false

		for _, ta := range n.tas {
			for _, b := range ta.BroadcastPLMNs {
				if !slices.Contains(plmns, b.PLMN) {
					continue
				}

				contributes = true
				tacs[ta.TAC] = true
				k := key{ta.TAC, b.PLMN}
				byTA[k] = addSlices(byTA[k], b.Slices)
			}
		}

		if contributes && (!found || n.drx > cfg.drx) {
			cfg.drx = n.drx
			found = true
		}
	}

	if !found {
		return gnbConfig{}, false
	}

	sorted := make([]uint32, 0, len(tacs))
	for tac := range tacs {
		sorted = append(sorted, tac)
	}

	slices.Sort(sorted)

	for _, tac := range sorted[:min(len(sorted), maxnoofTACs)] {
		ta := TA{TAC: tac}

		for _, p := range plmns {
			if s, ok := byTA[key{tac, p}]; ok && len(ta.BroadcastPLMNs) < maxnoofBPLMNs {
				ta.BroadcastPLMNs = append(ta.BroadcastPLMNs, BroadcastPLMN{PLMN: p, Slices: s})
			}
		}

		cfg.tas = append(cfg.tas, ta)
	}

	return cfg, true
}

// TS 38.413 clause 9.2.6.2
// TS 38.300 clause 4.10.2.3
func mergeAMFConfig(pools []amfPool) amfConfig {
	var cfg amfConfig

	for _, pool := range pools {
		for _, g := range pool.served {
			if slices.Contains(pool.plmns, g.PLMN) && !slices.Contains(cfg.served, g) && len(cfg.served) < maxnoofServedGUAMIs {
				cfg.served = append(cfg.served, g)
			}
		}

		for _, s := range pool.support {
			if !slices.Contains(pool.plmns, s.PLMN) {
				continue
			}

			i := slices.IndexFunc(cfg.support, func(p PLMNSupport) bool { return p.PLMN == s.PLMN })
			if i < 0 {
				if len(cfg.support) == maxnoofPLMNs {
					continue
				}

				cfg.support = append(cfg.support, PLMNSupport{PLMN: s.PLMN})
				i = len(cfg.support) - 1
			}

			cfg.support[i].Slices = addSlices(cfg.support[i].Slices, s.Slices)
		}
	}

	return cfg
}
