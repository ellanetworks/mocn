package api

import (
	"fmt"

	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/metrics"
)

// TS 23.003 clause 12.1
type PLMN struct {
	MCC string `json:"mcc"`
	MNC string `json:"mnc"`
}

// TS 36.413 clause 9.2.1.37
type GlobalENBID struct {
	PLMN PLMN         `json:"plmn"`
	Type db.ENBIDType `json:"enb_id_type"`
	ID   int64        `json:"enb_id"`
}

// TS 38.413 clause 9.3.1.6
type GlobalGNBID struct {
	PLMN      PLMN  `json:"plmn"`
	ID        int64 `json:"gnb_id"`
	BitLength int   `json:"bit_length"`
}

// TS 36.413 clause 9.2.3.9
type GUMMEI struct {
	PLMN       PLMN   `json:"plmn"`
	MMEGroupID uint16 `json:"mme_group_id"`
	MMECode    uint8  `json:"mme_code"`
}

// TS 38.413 clause 9.3.3.3
type GUAMI struct {
	PLMN        PLMN   `json:"plmn"`
	AMFRegionID uint8  `json:"amf_region_id"`
	AMFSetID    uint16 `json:"amf_set_id"`
	AMFPointer  uint8  `json:"amf_pointer"`
}

// TS 38.413 clause 9.3.1.24
type SNSSAI struct {
	SST uint8  `json:"sst"`
	SD  string `json:"sd,omitempty"`
}

// TS 36.413 clause 9.2.3.16
// TS 38.413 clause 9.3.3.11
type TAI struct {
	PLMN PLMN   `json:"plmn"`
	TAC  string `json:"tac"`
}

// TS 36.413 clause 9.2.1.38
type ECGI struct {
	PLMN   PLMN   `json:"plmn"`
	CellID uint32 `json:"cell_id"`
}

// TS 38.413 clause 9.3.1.7
type NRCGI struct {
	PLMN   PLMN   `json:"plmn"`
	CellID uint64 `json:"cell_id"`
}

const (
	ranNodeTypeENB = "eNB"
	ranNodeTypeGNB = "gNB"
)

var ranNodeTypes = map[string]string{
	metrics.InterfaceS1: ranNodeTypeENB,
	metrics.InterfaceNG: ranNodeTypeGNB,
}

func plmnOfDB(p db.PLMN) PLMN {
	return PLMN(p)
}

func plmnsOfDB(in []db.PLMN) []PLMN {
	out := make([]PLMN, 0, len(in))
	for _, p := range in {
		out = append(out, plmnOfDB(p))
	}

	return out
}

func globalENBIDOfDB(e db.GlobalENBID) *GlobalENBID {
	return &GlobalENBID{PLMN: plmnOfDB(e.PLMN), Type: e.Type, ID: int64(e.ID)}
}

func globalGNBIDOfDB(g db.GlobalGNBID) *GlobalGNBID {
	return &GlobalGNBID{PLMN: plmnOfDB(g.PLMN), ID: int64(g.ID), BitLength: g.Length}
}

func tacString(tac uint32) string {
	return fmt.Sprintf("%06x", tac)
}
