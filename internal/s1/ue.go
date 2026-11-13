package s1

import (
	"errors"
	"slices"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
)

const maxENBUES1APID = 1<<24 - 1

var errNoUEID = errors.New("no free UE S1AP ID")

// TS 36.413 clause 9.2.3.16
type TAI struct {
	PLMN db.PLMN
	TAC  uint16
}

// TS 36.413 clause 9.2.1.38
type ECGI struct {
	PLMN   db.PLMN
	CellID uint32
}

type UE struct {
	RadioGlobalENBID db.GlobalENBID
	RadioENBUES1APID uint32
	RadioMMEUES1APID uint32
	MMETNLAddresses  []string
	MMEENBUES1APID   uint32
	MMEUES1APID      *uint32
	OperatorID       int64
	OperatorName     string
	PLMN             db.PLMN
	Since            time.Time
	TAI              *TAI
	ECGI             *ECGI
}

type ueContext struct {
	ran        *RANAssociation
	enbID      s1ap.ENBUES1APID
	ranMMEID   s1ap.MMEUES1APID
	mme        *MMEAssociation
	mmeENBID   s1ap.ENBUES1APID
	mmeID      s1ap.MMEUES1APID
	mmeIDKnown bool
	operatorID int64
	plmn       db.PLMN
	radio      db.GlobalENBID
	since      time.Time
	tai        *TAI
	ecgi       *ECGI
	release    *time.Timer
}

type ranUEKey struct {
	ran *RANAssociation
	id  s1ap.ENBUES1APID
}

type mmeUEKey struct {
	mme *MMEAssociation
	id  s1ap.MMEUES1APID
}

type ueTable struct {
	byRANMMEID map[s1ap.MMEUES1APID]*ueContext
	byRANENBID map[ranUEKey]*ueContext
	byMMEENBID map[s1ap.ENBUES1APID]*ueContext
	byMMEID    map[mmeUEKey]*ueContext
	nextMMEID  s1ap.MMEUES1APID
	nextENBID  s1ap.ENBUES1APID
}

func newUETable() ueTable {
	return ueTable{
		byRANMMEID: map[s1ap.MMEUES1APID]*ueContext{},
		byRANENBID: map[ranUEKey]*ueContext{},
		byMMEENBID: map[s1ap.ENBUES1APID]*ueContext{},
		byMMEID:    map[mmeUEKey]*ueContext{},
	}
}

func (t *ueTable) add(u *ueContext) error {
	if u.mme != nil && len(t.byMMEENBID) > maxENBUES1APID {
		return errNoUEID
	}

	for {
		t.nextMMEID++
		if _, used := t.byRANMMEID[t.nextMMEID]; !used {
			break
		}
	}

	u.ranMMEID = t.nextMMEID
	t.byRANMMEID[u.ranMMEID] = u
	t.byRANENBID[ranUEKey{u.ran, u.enbID}] = u

	if u.mme == nil {
		return nil
	}

	for {
		t.nextENBID = (t.nextENBID + 1) & maxENBUES1APID
		if _, used := t.byMMEENBID[t.nextENBID]; !used {
			break
		}
	}

	u.mmeENBID = t.nextENBID
	t.byMMEENBID[u.mmeENBID] = u

	return nil
}

func (t *ueTable) setMMEID(u *ueContext, id s1ap.MMEUES1APID) {
	u.mmeID, u.mmeIDKnown = id, true
	t.byMMEID[mmeUEKey{u.mme, id}] = u
}

func (t *ueTable) remove(us ...*ueContext) []*ueContext {
	var out []*ueContext

	for _, u := range us {
		if u == nil || t.byRANMMEID[u.ranMMEID] != u || slices.Contains(out, u) {
			continue
		}

		delete(t.byRANMMEID, u.ranMMEID)
		delete(t.byRANENBID, ranUEKey{u.ran, u.enbID})

		if u.mme != nil {
			delete(t.byMMEENBID, u.mmeENBID)
		}

		if u.release != nil {
			u.release.Stop()
		}

		if u.mmeIDKnown {
			delete(t.byMMEID, mmeUEKey{u.mme, u.mmeID})
		}

		out = append(out, u)
	}

	return out
}

func (t *ueTable) where(keep func(*ueContext) bool) []*ueContext {
	var out []*ueContext

	for _, u := range t.byRANMMEID {
		if keep(u) {
			out = append(out, u)
		}
	}

	slices.SortFunc(out, func(a, b *ueContext) int { return int(a.ranMMEID) - int(b.ranMMEID) })

	return out
}

func (t *ueTable) onRAN(n *RANAssociation, mmeID *s1ap.MMEUES1APID, enbID *s1ap.ENBUES1APID) []*ueContext {
	var out []*ueContext

	if mmeID != nil {
		if u := t.byRANMMEID[*mmeID]; u != nil && u.ran == n {
			out = append(out, u)
		}
	}

	if enbID != nil {
		if u := t.byRANENBID[ranUEKey{n, *enbID}]; u != nil && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}

	return out
}

func (t *ueTable) onMME(a *MMEAssociation, mmeID *s1ap.MMEUES1APID, enbID *s1ap.ENBUES1APID) []*ueContext {
	var out []*ueContext

	if enbID != nil {
		if u := t.byMMEENBID[*enbID]; u != nil && u.mme == a {
			out = append(out, u)
		}
	}

	if mmeID != nil {
		if u := t.byMMEID[mmeUEKey{a, *mmeID}]; u != nil && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}

	return out
}

func (u *ueContext) snapshot() UE {
	s := UE{
		RadioGlobalENBID: u.radio,
		RadioENBUES1APID: uint32(u.enbID),
		RadioMMEUES1APID: uint32(u.ranMMEID),
		MMETNLAddresses:  u.mme.TNLAddresses(),
		MMEENBUES1APID:   uint32(u.mmeENBID),
		OperatorID:       u.operatorID,
		PLMN:             u.plmn,
		Since:            u.since,
	}

	if u.mmeIDKnown {
		id := uint32(u.mmeID)
		s.MMEUES1APID = &id
	}

	if u.tai != nil {
		tai := *u.tai
		s.TAI = &tai
	}

	if u.ecgi != nil {
		ecgi := *u.ecgi
		s.ECGI = &ecgi
	}

	return s
}
