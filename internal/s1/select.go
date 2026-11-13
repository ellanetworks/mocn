package s1

import (
	"slices"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
)

const (
	selectedByGUMMEI   = "gummei"
	selectedBySTMSI    = "s_tmsi"
	selectedByCapacity = "capacity"
)

type mmeCandidate struct {
	assoc    *MMEAssociation
	served   []ServedGUMMEIs
	capacity uint8
}

type ueIdentity struct {
	plmn   db.PLMN
	gummei *s1ap.GUMMEI
	stmsi  *s1ap.STMSI
}

func (g *Gateway) selectionLocked(plmn db.PLMN) (operatorView, []mmeCandidate, bool) {
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

	var cands []mmeCandidate

	for _, key := range sortedKeys(g.mmes) {
		c := g.mmes[key]
		if c.operatorID != op.id || c.status.State != MMEUp || c.assoc == nil {
			continue
		}

		cand := mmeCandidate{assoc: c.assoc, served: c.status.ServedGUMMEIs}
		if c.status.RelativeMMECapacity != nil {
			cand.capacity = *c.status.RelativeMMECapacity
		}

		cands = append(cands, cand)
	}

	return op, cands, true
}

// TS 36.300 clause 4.6.2
// TS 36.300 clause 19.2.1.7
// TS 23.401 clause 4.3.8.3
// TS 23.401 clause 4.3.7.2
// TS 23.401 clause 4.3.7.3
func selectMME(cands []mmeCandidate, id ueIdentity, intN func(int) int) (*MMEAssociation, string) {
	if len(cands) == 0 {
		return nil, ""
	}

	if g := id.gummei; g != nil {
		if plmn, err := decodePLMN(g.PLMNIdentity); err == nil {
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

	total := 0
	for _, c := range cands {
		total += int(c.capacity)
	}

	if total == 0 {
		return nil, ""
	}

	pick := intN(total)

	for _, c := range cands {
		if pick < int(c.capacity) {
			return c.assoc, selectedByCapacity
		}

		pick -= int(c.capacity)
	}

	return cands[len(cands)-1].assoc, selectedByCapacity
}

func findServing(cands []mmeCandidate, plmn db.PLMN, group *uint16, code uint8) *MMEAssociation {
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
