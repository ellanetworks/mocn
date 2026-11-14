package ng

import (
	"time"

	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
)

type AMFState = assoc.State

const (
	AMFDisconnected = assoc.Disconnected
	AMFConnected    = assoc.Connected
	AMFUp           = assoc.Up
	AMFSetupFailed  = assoc.SetupFailed
)

type RANNodeState = assoc.RadioState

const (
	RANNodeConnected = assoc.RadioConnected
	RANNodeUp        = assoc.RadioUp
)

// TS 38.413 clause 9.3.3.3
type GUAMI struct {
	PLMN        db.PLMN
	AMFRegionID uint8
	AMFSetID    uint16
	AMFPointer  uint8
}

// TS 38.413 clause 9.3.1.24
type SNSSAI struct {
	SST uint8
	SD  string
}

// TS 38.413 clause 9.2.6.2
type PLMNSupport struct {
	PLMN   db.PLMN
	Slices []SNSSAI
}

// TS 38.413 clause 9.2.6.1
type BroadcastPLMN struct {
	PLMN   db.PLMN
	Slices []SNSSAI
}

// TS 38.413 clause 9.2.6.1
type TA struct {
	TAC            uint32
	BroadcastPLMNs []BroadcastPLMN
}

// TS 38.413 clause 9.3.1.105
type OverloadAction string

const (
	OverloadRejectNonEmergencyMODT      OverloadAction = "reject_non_emergency_mo_dt"
	OverloadRejectRRCCRSignalling       OverloadAction = "reject_rrc_cr_signalling"
	OverloadPermitEmergencyAndMTOnly    OverloadAction = "permit_emergency_sessions_and_mobile_terminated_services_only"
	OverloadPermitHighPriorityAndMTOnly OverloadAction = "permit_high_priority_sessions_and_mobile_terminated_services_only"
)

// TS 38.413 clause 9.2.6.14
type SliceOverload struct {
	SNSSAIs                        []SNSSAI
	Action                         *OverloadAction
	TrafficLoadReductionIndication *uint8
}

// TS 38.413 clause 9.2.6.14
type Overload struct {
	GUAMIs                         []GUAMI
	Action                         *OverloadAction
	TrafficLoadReductionIndication *uint8
	Slices                         []SliceOverload
}

type AMFStatus struct {
	State               AMFState
	Since               time.Time
	PrimaryTNLAddress   string
	AMFName             string
	ServedGUAMIs        []GUAMI
	PLMNSupport         []PLMNSupport
	RelativeAMFCapacity *uint8
	UnavailableGUAMIs   []GUAMI
	Overload            []Overload
}

type RANNode struct {
	GlobalGNBID      *db.GlobalGNBID
	Name             string
	SupportedTAs     []TA
	DefaultPagingDRX int
	State            RANNodeState
	ConnectedSince   time.Time
	TNLAddresses     []string
}
