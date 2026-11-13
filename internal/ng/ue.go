package ng

import (
	"errors"
	"slices"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/tracing"
)

const (
	maxAMFUENGAPID = 1<<40 - 1
	maxRANUENGAPID = 1<<32 - 1
)

var errNoUEID = errors.New("no free UE NGAP ID")

// TS 38.413 clause 9.3.3.11
type TAI struct {
	PLMN db.PLMN
	TAC  uint32
}

// TS 38.413 clause 9.3.1.7
type NRCGI struct {
	PLMN   db.PLMN
	CellID uint64
}

type UE struct {
	RadioGlobalGNBID db.GlobalGNBID
	RadioRANUENGAPID uint32
	RadioAMFUENGAPID uint64
	AMFTNLAddresses  []string
	AMFRANUENGAPID   uint32
	AMFUENGAPID      *uint64
	OperatorID       int64
	OperatorName     string
	PLMN             db.PLMN
	Since            time.Time
	TAI              *TAI
	NRCGI            *NRCGI
}

type ueContext struct {
	ran        *RANAssociation
	ranID      ngap.RANUENGAPID
	ranAMFID   ngap.AMFUENGAPID
	amf        *AMFAssociation
	amfRANID   ngap.RANUENGAPID
	amfID      ngap.AMFUENGAPID
	amfIDKnown bool
	told       bool
	operatorID int64
	plmn       db.PLMN
	radio      db.GlobalGNBID
	since      time.Time
	tai        *TAI
	nrcgi      *NRCGI
	release    *time.Timer
	traceID    tracing.RANTraceID
}

type ranUEKey struct {
	ran *RANAssociation
	id  ngap.RANUENGAPID
}

type amfUEKey struct {
	amf *AMFAssociation
	id  ngap.AMFUENGAPID
}

type ueTable struct {
	byRANAMFID map[ngap.AMFUENGAPID]*ueContext
	byRANID    map[ranUEKey]*ueContext
	byAMFRANID map[ngap.RANUENGAPID]*ueContext
	byAMFID    map[amfUEKey]*ueContext
	nextAMFID  ngap.AMFUENGAPID
	nextRANID  ngap.RANUENGAPID
}

func newUETable() ueTable {
	return ueTable{
		byRANAMFID: map[ngap.AMFUENGAPID]*ueContext{},
		byRANID:    map[ranUEKey]*ueContext{},
		byAMFRANID: map[ngap.RANUENGAPID]*ueContext{},
		byAMFID:    map[amfUEKey]*ueContext{},
	}
}

func (t *ueTable) add(u *ueContext) error {
	if u.amf != nil && len(t.byAMFRANID) > maxRANUENGAPID {
		return errNoUEID
	}

	for {
		t.nextAMFID = (t.nextAMFID + 1) & maxAMFUENGAPID
		if _, used := t.byRANAMFID[t.nextAMFID]; !used {
			break
		}
	}

	u.ranAMFID = t.nextAMFID
	t.byRANAMFID[u.ranAMFID] = u
	t.byRANID[ranUEKey{u.ran, u.ranID}] = u

	if u.amf == nil {
		return nil
	}

	for {
		t.nextRANID++
		if _, used := t.byAMFRANID[t.nextRANID]; !used {
			break
		}
	}

	u.amfRANID = t.nextRANID
	t.byAMFRANID[u.amfRANID] = u

	return nil
}

func (t *ueTable) setAMFID(u *ueContext, id ngap.AMFUENGAPID) {
	if u.amfIDKnown {
		delete(t.byAMFID, amfUEKey{u.amf, u.amfID})
	}

	u.amfID, u.amfIDKnown = id, true
	t.byAMFID[amfUEKey{u.amf, id}] = u
}

func (t *ueTable) rebind(u *ueContext, a *AMFAssociation) {
	if u.amfIDKnown {
		delete(t.byAMFID, amfUEKey{u.amf, u.amfID})
	}

	u.amf, u.amfIDKnown = a, false
}

func (t *ueTable) remove(us ...*ueContext) []*ueContext {
	var out []*ueContext

	for _, u := range us {
		if u == nil || t.byRANAMFID[u.ranAMFID] != u || slices.Contains(out, u) {
			continue
		}

		delete(t.byRANAMFID, u.ranAMFID)
		delete(t.byRANID, ranUEKey{u.ran, u.ranID})

		if u.amf != nil {
			delete(t.byAMFRANID, u.amfRANID)
		}

		if u.release != nil {
			u.release.Stop()
		}

		u.traceID = tracing.RANTraceID{}

		if u.amfIDKnown {
			delete(t.byAMFID, amfUEKey{u.amf, u.amfID})
		}

		out = append(out, u)
	}

	return out
}

func (t *ueTable) where(keep func(*ueContext) bool) []*ueContext {
	var out []*ueContext

	for _, u := range t.byRANAMFID {
		if keep(u) {
			out = append(out, u)
		}
	}

	slices.SortFunc(out, func(a, b *ueContext) int {
		switch {
		case a.ranAMFID < b.ranAMFID:
			return -1
		case a.ranAMFID > b.ranAMFID:
			return 1
		default:
			return 0
		}
	})

	return out
}

func (t *ueTable) onRAN(n *RANAssociation, amfID *ngap.AMFUENGAPID, ranID *ngap.RANUENGAPID) []*ueContext {
	var out []*ueContext

	if amfID != nil {
		if u := t.byRANAMFID[*amfID]; u != nil && u.ran == n {
			out = append(out, u)
		}
	}

	if ranID != nil {
		if u := t.byRANID[ranUEKey{n, *ranID}]; u != nil && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}

	return out
}

func (t *ueTable) onAMF(a *AMFAssociation, amfID *ngap.AMFUENGAPID, ranID *ngap.RANUENGAPID) []*ueContext {
	var out []*ueContext

	if ranID != nil {
		if u := t.byAMFRANID[*ranID]; u != nil && u.amf == a {
			out = append(out, u)
		}
	}

	if amfID != nil {
		if u := t.byAMFID[amfUEKey{a, *amfID}]; u != nil && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}

	return out
}

func (u *ueContext) snapshot() UE {
	s := UE{
		RadioGlobalGNBID: u.radio,
		RadioRANUENGAPID: uint32(u.ranID),
		RadioAMFUENGAPID: uint64(u.ranAMFID),
		AMFTNLAddresses:  u.amf.TNLAddresses(),
		AMFRANUENGAPID:   uint32(u.amfRANID),
		OperatorID:       u.operatorID,
		PLMN:             u.plmn,
		Since:            u.since,
	}

	if u.amfIDKnown {
		id := uint64(u.amfID)
		s.AMFUENGAPID = &id
	}

	if u.tai != nil {
		tai := *u.tai
		s.TAI = &tai
	}

	if u.nrcgi != nil {
		cgi := *u.nrcgi
		s.NRCGI = &cgi
	}

	return s
}
