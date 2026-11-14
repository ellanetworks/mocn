package ng

import (
	"slices"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
)

const (
	selectedByGUAMI    = "guami"
	selectedBySTMSI    = "5g_s_tmsi"
	selectedByAMFSet   = "amf_set"
	selectedByCapacity = "capacity"
)

type amfCandidate struct {
	assoc       *amfAssoc
	name        amfName
	served      []GUAMI
	types       map[GUAMI]ngap.GUAMIType
	unavailable []GUAMI
	backups     map[GUAMI]amfName
	support     []PLMNSupport
	capacity    uint8
	overloaded  bool
}

// TS 23.501 clause 5.19.3
type amfSet struct {
	plmn   db.PLMN
	region uint8
	id     uint16
}

type setQuery struct {
	plmn   db.PLMN
	region *uint8
	id     uint16
}

type fiveGSTMSI struct {
	setID   uint16
	pointer uint8
}

type ueIdentity struct {
	plmn      db.PLMN
	guami     *GUAMI
	guamiType *ngap.GUAMIType
	stmsi     *fiveGSTMSI
	set       *uint16
	nssai     []SNSSAI
}

func setOf(g GUAMI) amfSet {
	return amfSet{plmn: g.PLMN, region: g.AMFRegionID, id: g.AMFSetID}
}

func setsOf(served []GUAMI) []amfSet {
	var out []amfSet

	for _, g := range served {
		if s := setOf(g); !slices.Contains(out, s) {
			out = append(out, s)
		}
	}

	return out
}

func sharesSet(a, b []amfSet) bool {
	return slices.ContainsFunc(a, func(s amfSet) bool { return slices.Contains(b, s) })
}

// TS 23.003 clause 2.10.1
func (q setQuery) matches(g GUAMI) bool {
	return g.AMFSetID == q.id && g.PLMN == q.plmn && (q.region == nil || g.AMFRegionID == *q.region)
}

func (g *Gateway) selectionLocked(plmn db.PLMN) (assoc.Operator, []amfCandidate, bool) {
	i := slices.IndexFunc(g.core.OperatorsLocked(), func(o assoc.Operator) bool { return slices.Contains(o.PLMNs, plmn) })
	if i < 0 {
		return assoc.Operator{}, nil, false
	}

	op := g.core.OperatorsLocked()[i]

	return op, g.candidatesLocked(op.ID), true
}

func (g *Gateway) candidatesLocked(operatorID int64) []amfCandidate {
	var cands []amfCandidate

	for _, c := range g.core.UpLocked(operatorID) {
		cand := amfCandidate{
			assoc: c.Assoc, name: c.Info.name, served: c.Info.served, types: c.Info.types,
			unavailable: c.Info.unavailable, backups: c.Info.backups, support: c.Info.support,
			overloaded: len(c.Info.overload) > 0,
		}
		if c.Info.capacity != nil {
			cand.capacity = *c.Info.capacity
		}

		cands = append(cands, cand)
	}

	return cands
}

func (c amfCandidate) available() bool {
	for _, g := range c.served {
		if !slices.Contains(c.unavailable, g) {
			return true
		}
	}

	return false
}

func (c amfCandidate) servesAvailable(match func(GUAMI) bool) bool {
	return slices.ContainsFunc(c.served, func(g GUAMI) bool { return match(g) && !slices.Contains(c.unavailable, g) })
}

func (c amfCandidate) inSet(q setQuery) bool {
	return c.servesAvailable(q.matches)
}

func (c amfCandidate) supports(plmn db.PLMN) bool {
	return slices.ContainsFunc(c.support, func(s PLMNSupport) bool { return s.PLMN == plmn })
}

// TS 38.413 clause 8.7.1.2
// TS 23.501 clause 6.3.5
func (c amfCandidate) identifies(g GUAMI, t *ngap.GUAMIType) bool {
	return c.servesAvailable(func(s GUAMI) bool {
		if s != g {
			return false
		}

		served, ok := c.types[s]

		return t == nil || !ok || served == *t
	})
}

// TS 23.501 clause 5.15.5.2.1
// TS 23.501 clause 6.3.5
func (c amfCandidate) covers(plmn db.PLMN, nssai []SNSSAI) bool {
	if len(nssai) == 0 {
		return true
	}

	i := slices.IndexFunc(c.support, func(s PLMNSupport) bool { return s.PLMN == plmn })

	return i >= 0 && !slices.ContainsFunc(nssai, func(s SNSSAI) bool { return !slices.Contains(c.support[i].Slices, s) })
}

func preferred(cands []amfCandidate, keep func(amfCandidate) bool, intN func(int) int,
	tiers ...func(amfCandidate) bool,
) *amfAssoc {
	for _, tier := range tiers {
		if a := weighted(cands, func(c amfCandidate) bool { return keep(c) && tier(c) }, intN); a != nil {
			return a
		}
	}

	return weighted(cands, keep, intN)
}

// TS 38.300 clause 4.10.2.3
// TS 23.501 clause 6.3.5
// TS 23.501 clause 5.19.3
// TS 23.501 clause 5.15.5.2.1
// TS 23.501 clause 5.21.2.2.1
// TS 23.003 clause 2.10.1
func selectAMF(cands []amfCandidate, id ueIdentity, intN func(int) int) (*amfAssoc, string) {
	covers := func(c amfCandidate) bool { return c.covers(id.plmn, id.nssai) }

	if g := id.guami; g != nil {
		if a := identified(cands, func(c amfCandidate) bool { return c.identifies(*g, id.guamiType) }, intN); a != nil {
			return a, selectedByGUAMI
		}

		q := setQuery{plmn: g.PLMN, region: &g.AMFRegionID, id: g.AMFSetID}

		if a := preferred(cands, func(c amfCandidate) bool { return c.inSet(q) }, intN, covers); a != nil {
			return a, selectedByAMFSet
		}
	}

	if s := id.stmsi; s != nil {
		q := setQuery{plmn: id.plmn, id: s.setID}

		if a := identified(cands, func(c amfCandidate) bool {
			return c.servesAvailable(func(g GUAMI) bool { return q.matches(g) && g.AMFPointer == s.pointer })
		}, intN); a != nil {
			return a, selectedBySTMSI
		}

		if a := preferred(cands, func(c amfCandidate) bool { return c.inSet(q) }, intN, covers); a != nil {
			return a, selectedByAMFSet
		}
	}

	if s := id.set; s != nil {
		q := setQuery{plmn: id.plmn, id: *s}

		if a := preferred(cands, func(c amfCandidate) bool { return c.inSet(q) }, intN, covers); a != nil {
			return a, selectedByAMFSet
		}

		return nil, ""
	}

	if a := preferred(cands, func(c amfCandidate) bool { return c.supports(id.plmn) && c.available() }, intN,
		func(c amfCandidate) bool { return covers(c) && !c.overloaded }, covers,
		func(c amfCandidate) bool { return !c.overloaded },
	); a != nil {
		return a, selectedByCapacity
	}

	return nil, ""
}

type binding struct {
	current *amfAssoc
	guami   *GUAMI
	backup  amfName
	sets    []amfSet
}

// TS 23.502 clause 4.2.7.2.3
// TS 23.502 clause 4.2.7.2.5
// TS 23.501 clause 5.21.2.2.1
// TS 23.501 clause 5.21.2.3
func rebindAMF(cands []amfCandidate, b binding, intN func(int) int) *amfAssoc {
	if g := b.guami; g != nil {
		if a := identified(cands, func(c amfCandidate) bool {
			return c.servesAvailable(func(s GUAMI) bool { return s == *g })
		}, intN); a != nil {
			return a
		}

		backup := b.backup

		for _, c := range cands {
			if c.assoc == b.current && !c.backups[*g].empty() {
				backup = c.backups[*g]
			}
		}

		if !backup.empty() {
			if i := slices.IndexFunc(cands, func(c amfCandidate) bool { return c.name.matches(backup) && c.available() }); i >= 0 {
				return cands[i].assoc
			}
		}

		q := setQuery{plmn: g.PLMN, region: &g.AMFRegionID, id: g.AMFSetID}

		return weighted(cands, func(c amfCandidate) bool { return c.inSet(q) }, intN)
	}

	if i := slices.IndexFunc(cands, func(c amfCandidate) bool { return c.assoc == b.current && c.available() }); i >= 0 {
		return b.current
	}

	return weighted(cands, func(c amfCandidate) bool {
		return slices.ContainsFunc(b.sets, func(s amfSet) bool {
			return c.inSet(setQuery{plmn: s.plmn, region: &s.region, id: s.id})
		})
	}, intN)
}

// TS 23.501 clause 6.3.5
func identified(cands []amfCandidate, keep func(amfCandidate) bool, intN func(int) int) *amfAssoc {
	var pool []amfCandidate

	for _, c := range cands {
		if keep(c) {
			pool = append(pool, c)
		}
	}

	if len(pool) == 0 {
		return nil
	}

	if a := weighted(pool, func(amfCandidate) bool { return true }, intN); a != nil {
		return a
	}

	return pool[intN(len(pool))].assoc
}

// TS 23.501 clause 5.19.3
func weighted(cands []amfCandidate, keep func(amfCandidate) bool, intN func(int) int) *amfAssoc {
	var (
		pool  []amfCandidate
		total int
	)

	for _, c := range cands {
		if c.capacity > 0 && keep(c) {
			pool = append(pool, c)
			total += int(c.capacity)
		}
	}

	if total == 0 {
		return nil
	}

	pick := intN(total)

	for _, c := range pool {
		if pick < int(c.capacity) {
			return c.assoc
		}

		pick -= int(c.capacity)
	}

	return pool[len(pool)-1].assoc
}
