package s1

import (
	"time"

	"github.com/ellanetworks/mocn/internal/db"
)

type MMEState string

const (
	MMEDisconnected MMEState = "disconnected"
	MMEConnected    MMEState = "connected"
	MMEUp           MMEState = "up"
	MMESetupFailed  MMEState = "setup_failed"
)

var MMEStates = []MMEState{MMEDisconnected, MMEConnected, MMEUp, MMESetupFailed}

type RANNodeState string

const (
	RANNodeConnected RANNodeState = "connected"
	RANNodeUp        RANNodeState = "up"
)

var RANNodeStates = []RANNodeState{RANNodeConnected, RANNodeUp}

const ENBIDHome db.ENBIDType = "home"

// TS 36.413 clause 9.1.8.5
type ServedGUMMEIs struct {
	PLMNs    []db.PLMN
	GroupIDs []uint16
	MMECodes []uint8
}

// TS 36.413 clause 9.1.8.4
type TA struct {
	TAC            uint16
	BroadcastPLMNs []db.PLMN
}

// TS 36.413 clause 9.2.3.9
type GUMMEI struct {
	PLMN       db.PLMN
	MMEGroupID uint16
	MMECode    uint8
}

// TS 36.413 clause 9.2.3.20
type OverloadAction string

const (
	OverloadRejectNonEmergencyMODT                   OverloadAction = "reject_non_emergency_mo_dt"
	OverloadRejectRRCCRSignalling                    OverloadAction = "reject_rrc_cr_signalling"
	OverloadPermitEmergencyAndMTOnly                 OverloadAction = "permit_emergency_sessions_and_mobile_terminated_services_only"
	OverloadPermitHighPriorityAndMTOnly              OverloadAction = "permit_high_priority_sessions_and_mobile_terminated_services_only"
	OverloadRejectDelayTolerantAccess                OverloadAction = "reject_delay_tolerant_access"
	OverloadPermitHighPriorityExceptionReportAndMT   OverloadAction = "permit_high_priority_sessions_and_exception_reporting_and_mobile_terminated_services_only"
	OverloadNotAcceptMODataOrDelayTolerantFromCPCIoT OverloadAction = "not_accept_mo_data_or_delay_tolerant_access_from_cp_ciot"
)

// TS 36.413 clause 9.1.8.13
type Overload struct {
	GUMMEI                         GUMMEI
	Action                         OverloadAction
	TrafficLoadReductionIndication *uint8
}

type MMEStatus struct {
	State               MMEState
	Since               time.Time
	PrimaryTNLAddress   string
	MMEName             string
	ServedGUMMEIs       []ServedGUMMEIs
	RelativeMMECapacity *uint8
	Overload            []Overload
}

type RANNode struct {
	GlobalENBID      *db.GlobalENBID
	Name             string
	SupportedTAs     []TA
	DefaultPagingDRX int
	State            RANNodeState
	ConnectedSince   time.Time
	TNLAddresses     []string
}
