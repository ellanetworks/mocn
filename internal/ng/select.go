package ng

import (
	"maps"
	"slices"

	"github.com/ellanetworks/mocn/internal/db"
)

const (
	selectedByGUAMI    = "guami"
	selectedBySTMSI    = "5g_s_tmsi"
	selectedByAMFSet   = "amf_set"
	selectedByCapacity = "capacity"
)

type amfCandidate struct {
	assoc       *AMFAssociation
	served      []GUAMI
	unavailable []GUAMI
	support     []PLMNSupport
	capacity    uint8
}

type amfSet struct {
	plmn   *db.PLMN
	region *uint8
	id     uint16
}

type fiveGSTMSI struct {
	setID   uint16
	pointer uint8
}

type ueIdentity struct {
	plmn  db.PLMN
	guami *GUAMI
	stmsi *fiveGSTMSI
	set   *uint16
}

func (g *Gateway) selectionLocked(plmn db.PLMN) (operatorView, []amfCandidate, bool) {
	var op operatorView

	found := false

	for _, o := range g.operators {
		if slices.Contains(o.plmns, plmn) {
			op, found = o, true
			break
		}
	}

	if !found {
		return operatorView{}, nil, false
	}

	return op, g.candidatesLocked(op.id), true
}

func (g *Gateway) candidatesLocked(operatorID int64) []amfCandidate {
	var cands []amfCandidate

	for _, key := range slices.Sorted(maps.Keys(g.amfs)) {
		c := g.amfs[key]
		if c.operatorID != operatorID || c.status.State != AMFUp || c.assoc == nil {
			continue
		}

		cand := amfCandidate{
			assoc: c.assoc, served: c.status.ServedGUAMIs, unavailable: c.status.UnavailableGUAMIs,
			support: c.status.PLMNSupport,
		}
		if c.status.RelativeAMFCapacity != nil {
			cand.capacity = *c.status.RelativeAMFCapacity
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

func (c amfCandidate) inSet(s amfSet) bool {
	return slices.ContainsFunc(c.served, func(g GUAMI) bool {
		return g.AMFSetID == s.id && (s.plmn == nil || g.PLMN == *s.plmn) && (s.region == nil || g.AMFRegionID == *s.region) &&
			!slices.Contains(c.unavailable, g)
	})
}

func (c amfCandidate) supports(plmn db.PLMN) bool {
	return slices.ContainsFunc(c.support, func(s PLMNSupport) bool { return s.PLMN == plmn })
}

// TS 38.300 clause 4.10.2.3
// TS 23.501 clause 6.3.5
// TS 23.501 clause 5.19.3
// TS 23.501 clause 5.21.2.2.1
func selectAMF(cands []amfCandidate, id ueIdentity, intN func(int) int) (*AMFAssociation, string) {
	if g := id.guami; g != nil {
		for _, c := range cands {
			if slices.Contains(c.served, *g) && !slices.Contains(c.unavailable, *g) {
				return c.assoc, selectedByGUAMI
			}
		}

		if a := weighted(cands, func(c amfCandidate) bool {
			return c.inSet(amfSet{plmn: &g.PLMN, region: &g.AMFRegionID, id: g.AMFSetID})
		}, intN); a != nil {
			return a, selectedByAMFSet
		}
	}

	if s := id.stmsi; s != nil {
		for _, c := range cands {
			if slices.ContainsFunc(c.served, func(g GUAMI) bool {
				return g.AMFSetID == s.setID && g.AMFPointer == s.pointer && !slices.Contains(c.unavailable, g)
			}) {
				return c.assoc, selectedBySTMSI
			}
		}

		if a := weighted(cands, func(c amfCandidate) bool { return c.inSet(amfSet{id: s.setID}) }, intN); a != nil {
			return a, selectedByAMFSet
		}
	}

	if s := id.set; s != nil {
		if a := weighted(cands, func(c amfCandidate) bool { return c.inSet(amfSet{id: *s}) }, intN); a != nil {
			return a, selectedByAMFSet
		}

		return nil, ""
	}

	if a := weighted(cands, func(c amfCandidate) bool { return c.supports(id.plmn) && c.available() }, intN); a != nil {
		return a, selectedByCapacity
	}

	return nil, ""
}

// TS 23.501 clause 5.19.3
func weighted(cands []amfCandidate, keep func(amfCandidate) bool, intN func(int) int) *AMFAssociation {
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
