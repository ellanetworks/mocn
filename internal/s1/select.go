package s1

import (
	"slices"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/plmnid"
)

const (
	selectedByGUMMEI   = "gummei"
	selectedBySTMSI    = "s_tmsi"
	selectedByCapacity = "capacity"
)

type mmeCandidate struct {
	assoc      *mmeAssoc
	served     []ServedGUMMEIs
	capacity   uint8
	overloaded bool
}

type ueIdentity struct {
	plmn   db.PLMN
	gummei *s1ap.GUMMEI
	stmsi  *s1ap.STMSI
}

func (g *Gateway) selectionLocked(plmn db.PLMN) (assoc.Operator, []mmeCandidate, bool) {
	i := slices.IndexFunc(g.core.OperatorsLocked(), func(o assoc.Operator) bool { return slices.Contains(o.PLMNs, plmn) })
	if i < 0 {
		return assoc.Operator{}, nil, false
	}

	op := g.core.OperatorsLocked()[i]

	return op, g.candidatesLocked(op.ID), true
}

func (g *Gateway) candidatesLocked(operatorID int64) []mmeCandidate {
	var cands []mmeCandidate

	for _, c := range g.core.UpLocked(operatorID) {
		cand := mmeCandidate{assoc: c.Assoc, served: c.Info.served, overloaded: len(c.Info.overload) > 0}
		if c.Info.capacity != nil {
			cand.capacity = *c.Info.capacity
		}

		cands = append(cands, cand)
	}

	return cands
}

// TS 36.300 clause 4.6.2
// TS 36.300 clause 19.2.1.7
// TS 23.401 clause 4.3.8.3
// TS 23.401 clause 4.3.7.2
// TS 23.401 clause 4.3.7.3
func selectMME(cands []mmeCandidate, id ueIdentity, intN func(int) int) (*mmeAssoc, string) {
	if len(cands) == 0 {
		return nil, ""
	}

	if g := id.gummei; g != nil {
		if plmn, err := plmnid.Decode(g.PLMNIdentity); err == nil {
			group := uint16(g.MMEGroupID[0])<<8 | uint16(g.MMEGroupID[1])
			if c := findServing(cands, plmn, &group, uint8(g.MMECode)); c != nil {
				return c, selectedByGUMMEI
			}
		}
	}

	if s := id.stmsi; s != nil {
		if c := findServing(cands, id.plmn, nil, uint8(s.MMEC)); c != nil {
			return c, selectedBySTMSI
		}
	}

	if a := weighted(cands, func(c mmeCandidate) bool { return !c.overloaded }, intN); a != nil {
		return a, selectedByCapacity
	}

	if a := weighted(cands, func(mmeCandidate) bool { return true }, intN); a != nil {
		return a, selectedByCapacity
	}

	return nil, ""
}

// TS 23.401 clause 4.3.7.2
// TS 23.401 clause 4.3.7.4.1
func weighted(cands []mmeCandidate, keep func(mmeCandidate) bool, intN func(int) int) *mmeAssoc {
	var (
		pool  []mmeCandidate
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

func findServing(cands []mmeCandidate, plmn db.PLMN, group *uint16, code uint8) *mmeAssoc {
	for _, c := range cands {
		for _, s := range c.served {
			if slices.Contains(s.PLMNs, plmn) && slices.Contains(s.MMECodes, code) &&
				(group == nil || slices.Contains(s.GroupIDs, *group)) {
				return c.assoc
			}
		}
	}

	return nil
}
