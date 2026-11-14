package s1

import (
	"errors"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/uetable"
)

const (
	maxENBUES1APID s1ap.ENBUES1APID = 1<<24 - 1
	maxMMEUES1APID s1ap.MMEUES1APID = 1<<32 - 1
)

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

type ueInfo struct {
	radio db.GlobalENBID
	tai   *TAI
	ecgi  *ECGI
	told  bool
}

type (
	ueContext = uetable.UE[*ranAssoc, *mmeAssoc, s1ap.ENBUES1APID, s1ap.MMEUES1APID, ueInfo]
	ueTable   = uetable.Table[*ranAssoc, *mmeAssoc, s1ap.ENBUES1APID, s1ap.MMEUES1APID, ueInfo]
)

func newUETable() *ueTable {
	return uetable.New[*ranAssoc, *mmeAssoc, s1ap.ENBUES1APID, s1ap.MMEUES1APID, ueInfo](
		maxENBUES1APID, maxMMEUES1APID, errNoUEID)
}

func snapshotUE(u *ueContext) UE {
	s := UE{
		RadioGlobalENBID: u.Ext.radio,
		RadioENBUES1APID: uint32(u.RANID),
		RadioMMEUES1APID: uint32(u.RANCoreID),
		MMETNLAddresses:  u.Core.TNLAddresses(),
		MMEENBUES1APID:   uint32(u.CoreRANID),
		OperatorID:       u.OperatorID,
		PLMN:             u.PLMN,
		Since:            u.Since,
	}

	if u.CoreIDKnown {
		id := uint32(u.CoreID)
		s.MMEUES1APID = &id
	}

	if u.Ext.tai != nil {
		tai := *u.Ext.tai
		s.TAI = &tai
	}

	if u.Ext.ecgi != nil {
		ecgi := *u.Ext.ecgi
		s.ECGI = &ecgi
	}

	return s
}
