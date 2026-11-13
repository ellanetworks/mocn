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

type MMEStatus struct {
	State               MMEState
	Since               time.Time
	PrimaryTNLAddress   string
	MMEName             string
	ServedGUMMEIs       []ServedGUMMEIs
	RelativeMMECapacity *uint8
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
