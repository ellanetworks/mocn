package ng

import (
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gw"
)

const (
	maxnoofTACs         = 256
	maxnoofBPLMNs       = 12
	maxnoofPLMNs        = 12
	maxnoofSliceItems   = 1024
	maxnoofServedGUAMIs = 256
)

var errNotGNB = errors.New("the Global RAN Node ID is not a Global gNB ID")

// TS 38.413 clause 9.3.1.6
func globalGNBIDFrom(id ngap.GlobalRANNodeID) (db.GlobalGNBID, error) {
	if id.Kind != ngap.RANNodeIDGNB {
		return db.GlobalGNBID{}, errNotGNB
	}

	plmn, err := gw.DecodePLMN(id.PLMNIdentity)
	if err != nil {
		return db.GlobalGNBID{}, err
	}

	return db.GlobalGNBID{PLMN: plmn, ID: id.Value, Length: id.Bits}, nil
}

// TS 38.413 clause 9.3.1.6
func globalGNBIDTo(id db.GlobalGNBID) (ngap.GlobalRANNodeID, error) {
	plmn, err := gw.EncodePLMN[ngap.PLMNIdentity](id.PLMN)
	if err != nil {
		return ngap.GlobalRANNodeID{}, err
	}

	return ngap.GlobalRANNodeID{Kind: ngap.RANNodeIDGNB, PLMNIdentity: plmn, Value: id.ID, Bits: id.Length}, nil
}

// TS 38.413 clause 9.3.1.24
func snssaiFrom(s ngap.SNSSAI) SNSSAI {
	out := SNSSAI{SST: uint8(s.SST)}
	if s.SD != nil {
		out.SD = hex.EncodeToString(s.SD[:])
	}

	return out
}

// TS 38.413 clause 9.3.1.24
func snssaiTo(s SNSSAI) (ngap.SNSSAI, error) {
	out := ngap.SNSSAI{SST: ngap.SST(s.SST)}

	if s.SD != "" {
		b, err := hex.DecodeString(s.SD)
		if err != nil || len(b) != 3 {
			return ngap.SNSSAI{}, fmt.Errorf("invalid SD %q", s.SD)
		}

		out.SD = &ngap.SD{b[0], b[1], b[2]}
	}

	return out, nil
}

func slicesFrom(in ngap.SliceSupportList) []SNSSAI {
	out := make([]SNSSAI, 0, len(in))
	for _, s := range in {
		out = append(out, snssaiFrom(s.SNSSAI))
	}

	return out
}

func slicesTo(in []SNSSAI) (ngap.SliceSupportList, error) {
	out := make(ngap.SliceSupportList, 0, len(in))

	for _, s := range in {
		v, err := snssaiTo(s)
		if err != nil {
			return nil, err
		}

		out = append(out, ngap.SliceSupportItem{SNSSAI: v})
	}

	return out, nil
}

// TS 38.413 clause 9.2.6.1
func tasFrom(in ngap.SupportedTAList) ([]TA, error) {
	out := make([]TA, 0, len(in))

	for _, ta := range in {
		item := TA{TAC: uint32(ta.TAC)}

		for _, b := range ta.BroadcastPLMNList {
			plmn, err := gw.DecodePLMN(b.PLMNIdentity)
			if err != nil {
				return nil, err
			}

			item.BroadcastPLMNs = append(item.BroadcastPLMNs, BroadcastPLMN{PLMN: plmn, Slices: slicesFrom(b.TAISliceSupportList)})
		}

		out = append(out, item)
	}

	return out, nil
}

// TS 38.413 clause 9.2.6.1
func tasTo(in []TA) (ngap.SupportedTAList, error) {
	out := make(ngap.SupportedTAList, 0, len(in))

	for _, ta := range in {
		item := ngap.SupportedTAItem{TAC: ngap.TAC(ta.TAC)}

		for _, b := range ta.BroadcastPLMNs {
			plmn, err := gw.EncodePLMN[ngap.PLMNIdentity](b.PLMN)
			if err != nil {
				return nil, err
			}

			slices, err := slicesTo(b.Slices)
			if err != nil {
				return nil, err
			}

			item.BroadcastPLMNList = append(item.BroadcastPLMNList, ngap.BroadcastPLMNItem{PLMNIdentity: plmn, TAISliceSupportList: slices})
		}

		out = append(out, item)
	}

	return out, nil
}

// TS 38.413 clause 9.3.3.3
func guamiFrom(g ngap.GUAMI) (GUAMI, error) {
	plmn, err := gw.DecodePLMN(g.PLMNIdentity)
	if err != nil {
		return GUAMI{}, err
	}

	return GUAMI{
		PLMN: plmn, AMFRegionID: uint8(g.AMFRegionID), AMFSetID: uint16(g.AMFSetID), AMFPointer: uint8(g.AMFPointer),
	}, nil
}

// TS 38.413 clause 9.3.3.3
func guamiTo(g GUAMI) (ngap.GUAMI, error) {
	plmn, err := gw.EncodePLMN[ngap.PLMNIdentity](g.PLMN)
	if err != nil {
		return ngap.GUAMI{}, err
	}

	return ngap.GUAMI{
		PLMNIdentity: plmn, AMFRegionID: ngap.AMFRegionID(g.AMFRegionID), AMFSetID: ngap.AMFSetID(g.AMFSetID),
		AMFPointer: ngap.AMFPointer(g.AMFPointer),
	}, nil
}

// TS 38.413 clause 9.2.6.2
func servedGUAMIsFrom(in ngap.ServedGUAMIList) ([]GUAMI, error) {
	out := make([]GUAMI, 0, len(in))

	for _, item := range in {
		g, err := guamiFrom(item.GUAMI)
		if err != nil {
			return nil, err
		}

		out = append(out, g)
	}

	return out, nil
}

// TS 38.413 clause 9.2.6.2
func servedGUAMIsTo(in []GUAMI) (ngap.ServedGUAMIList, error) {
	out := make(ngap.ServedGUAMIList, 0, len(in))

	for _, g := range in {
		v, err := guamiTo(g)
		if err != nil {
			return nil, err
		}

		out = append(out, ngap.ServedGUAMIItem{GUAMI: v})
	}

	return out, nil
}

// TS 38.413 clause 9.2.6.2
func plmnSupportFrom(in ngap.PLMNSupportList) ([]PLMNSupport, error) {
	out := make([]PLMNSupport, 0, len(in))

	for _, item := range in {
		plmn, err := gw.DecodePLMN(item.PLMNIdentity)
		if err != nil {
			return nil, err
		}

		out = append(out, PLMNSupport{PLMN: plmn, Slices: slicesFrom(item.SliceSupportList)})
	}

	return out, nil
}

// TS 38.413 clause 9.2.6.2
func plmnSupportTo(in []PLMNSupport) (ngap.PLMNSupportList, error) {
	out := make(ngap.PLMNSupportList, 0, len(in))

	for _, p := range in {
		plmn, err := gw.EncodePLMN[ngap.PLMNIdentity](p.PLMN)
		if err != nil {
			return nil, err
		}

		slices, err := slicesTo(p.Slices)
		if err != nil {
			return nil, err
		}

		out = append(out, ngap.PLMNSupportItem{PLMNIdentity: plmn, SliceSupportList: slices})
	}

	return out, nil
}

// TS 38.413 clause 9.3.1.90
func pagingDRXFrames(d ngap.PagingDRX) int {
	return 32 << d
}

// TS 38.413 clause 9.3.1.56
var timeToWait = map[ngap.TimeToWait]time.Duration{
	ngap.TimeToWaitV1s:  time.Second,
	ngap.TimeToWaitV2s:  2 * time.Second,
	ngap.TimeToWaitV5s:  5 * time.Second,
	ngap.TimeToWaitV10s: 10 * time.Second,
	ngap.TimeToWaitV20s: 20 * time.Second,
	ngap.TimeToWaitV60s: 60 * time.Second,
}

func causeText(c *ngap.Cause) string {
	if c == nil {
		return "no cause"
	}

	return c.String()
}
