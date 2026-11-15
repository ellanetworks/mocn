package ng

import (
	"errors"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/uetable"
)

const (
	maxAMFUENGAPID ngap.AMFUENGAPID = 1<<40 - 1
	maxRANUENGAPID ngap.RANUENGAPID = 1<<32 - 1
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
	RadioRANUENGAPID *uint32
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
	Tunnels          []gtpu.TunnelInfo
}

type ueInfo struct {
	radio     db.GlobalGNBID
	tai       *TAI
	nrcgi     *NRCGI
	told      bool
	guami     *GUAMI
	sets      []amfSet
	unbound   bool
	backup    amfName
	switching bool
	source    ngap.AMFUENGAPID
	tunnels   gtpu.Session
	path      *gtpu.Path
}

type (
	ueContext = uetable.UE[*ranAssoc, *amfAssoc, ngap.RANUENGAPID, ngap.AMFUENGAPID, ueInfo]
	ueTable   = uetable.Table[*ranAssoc, *amfAssoc, ngap.RANUENGAPID, ngap.AMFUENGAPID, ueInfo]
)

func newUETable() *ueTable {
	return uetable.New[*ranAssoc, *amfAssoc, ngap.RANUENGAPID, ngap.AMFUENGAPID, ueInfo](
		maxRANUENGAPID, maxAMFUENGAPID, errNoUEID)
}

func snapshotUE(u *ueContext) UE {
	s := UE{
		RadioGlobalGNBID: u.Ext.radio,
		RadioAMFUENGAPID: uint64(u.RANCoreID),
		AMFTNLAddresses:  u.Core.TNLAddresses(),
		AMFRANUENGAPID:   uint32(u.CoreRANID),
		OperatorID:       u.OperatorID,
		PLMN:             u.PLMN,
		Since:            u.Since,
	}

	if u.RANIDKnown {
		id := uint32(u.RANID)
		s.RadioRANUENGAPID = &id
	}

	if u.CoreIDKnown {
		id := uint64(u.CoreID)
		s.AMFUENGAPID = &id
	}

	if u.Ext.tai != nil {
		tai := *u.Ext.tai
		s.TAI = &tai
	}

	if u.Ext.nrcgi != nil {
		cgi := *u.Ext.nrcgi
		s.NRCGI = &cgi
	}

	return s
}
