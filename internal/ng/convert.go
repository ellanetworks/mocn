package ng

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/plmnid"
)

const (
	maxnoofTACs         = 256
	maxnoofBPLMNs       = 12
	maxnoofPLMNs        = 12
	maxnoofSliceItems   = 1024
	maxnoofServedGUAMIs = 256
)

var errNotGNB = errors.New("the Global RAN Node ID is not a Global gNB ID")

// TS 23.003 clause 28.4.2
var noSD = ngap.SD{0xff, 0xff, 0xff}

// TS 38.413 clause 9.3.3.21
// TS 38.413 clause 9.3.3.51
type amfName struct {
	visible string
	utf8    string
}

func (n amfName) String() string {
	if n.utf8 != "" {
		return n.utf8
	}

	return n.visible
}

func (n amfName) empty() bool {
	return n.visible == "" && n.utf8 == ""
}

func (n amfName) matches(o amfName) bool {
	return (n.visible != "" && n.visible == o.visible) || (n.utf8 != "" && n.utf8 == o.utf8)
}

// TS 38.413 clause 8.7.1.2
// TS 38.413 clause 8.7.3.2
// TS 38.413 clause 9.3.3.51
func amfNameFrom(name *string, ext *ngap.ExtendedAMFName) amfName {
	if ext != nil && (ext.AMFNameVisibleString != nil || ext.AMFNameUTF8String != nil) {
		var n amfName

		if ext.AMFNameVisibleString != nil {
			n.visible = string(*ext.AMFNameVisibleString)
		}

		if ext.AMFNameUTF8String != nil {
			n.utf8 = string(*ext.AMFNameUTF8String)
		}

		return n
	}

	if name == nil {
		return amfName{}
	}

	return amfName{visible: *name}
}

func backupNameFrom(name *ngap.Name, ext *ngap.ExtendedAMFName) amfName {
	if name == nil {
		return amfNameFrom(nil, ext)
	}

	return amfNameFrom(new(string(*name)), ext)
}

// TS 38.413 clause 9.3.1.6
func globalGNBIDFrom(id ngap.GlobalRANNodeID) (db.GlobalGNBID, error) {
	if id.GlobalGNBID == nil || id.GlobalGNBID.GNBID.GNBID == nil {
		return db.GlobalGNBID{}, errNotGNB
	}

	plmn, err := plmnid.Decode(id.GlobalGNBID.PLMNIdentity)
	if err != nil {
		return db.GlobalGNBID{}, err
	}

	g := id.GlobalGNBID.GNBID.GNBID

	return db.GlobalGNBID{PLMN: plmn, ID: g.Value, Length: g.Bits}, nil
}

// TS 38.413 clause 9.3.1.6
func globalGNBIDTo(id db.GlobalGNBID) (ngap.GlobalRANNodeID, error) {
	plmn, err := plmnid.Encode[ngap.PLMNIdentity](id.PLMN)
	if err != nil {
		return ngap.GlobalRANNodeID{}, err
	}

	return ngap.GlobalRANNodeID{GlobalGNBID: &ngap.GlobalGNBID{
		PLMNIdentity: plmn, GNBID: ngap.GNBID{GNBID: &ngap.GNBIDValue{Value: id.ID, Bits: id.Length}},
	}}, nil
}

// TS 38.413 clause 9.3.1.24
// TS 23.003 clause 28.4.2
func snssaiFrom(s ngap.SNSSAI) SNSSAI {
	out := SNSSAI{SST: uint8(s.SST)}
	if s.SD != nil && *s.SD != noSD {
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
			plmn, err := plmnid.Decode(b.PLMNIdentity)
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
			plmn, err := plmnid.Encode[ngap.PLMNIdentity](b.PLMN)
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
	plmn, err := plmnid.Decode(g.PLMNIdentity)
	if err != nil {
		return GUAMI{}, err
	}

	return GUAMI{
		PLMN: plmn, AMFRegionID: uint8(g.AMFRegionID), AMFSetID: uint16(g.AMFSetID), AMFPointer: uint8(g.AMFPointer),
	}, nil
}

// TS 38.413 clause 9.3.3.3
func guamiTo(g GUAMI) (ngap.GUAMI, error) {
	plmn, err := plmnid.Encode[ngap.PLMNIdentity](g.PLMN)
	if err != nil {
		return ngap.GUAMI{}, err
	}

	return ngap.GUAMI{
		PLMNIdentity: plmn, AMFRegionID: ngap.AMFRegionID(g.AMFRegionID), AMFSetID: ngap.AMFSetID(g.AMFSetID),
		AMFPointer: ngap.AMFPointer(g.AMFPointer),
	}, nil
}

type servedGUAMIs struct {
	guamis  []GUAMI
	backups map[GUAMI]amfName
	types   map[GUAMI]ngap.GUAMIType
}

// TS 38.413 clause 8.7.1.2
// TS 38.413 clause 9.2.6.2
func servedGUAMIsFrom(in ngap.ServedGUAMIList) (servedGUAMIs, error) {
	out := servedGUAMIs{
		guamis: make([]GUAMI, 0, len(in)), backups: map[GUAMI]amfName{}, types: map[GUAMI]ngap.GUAMIType{},
	}

	for _, item := range in {
		g, err := guamiFrom(item.GUAMI)
		if err != nil {
			return servedGUAMIs{}, err
		}

		out.guamis = append(out.guamis, g)

		if b := backupNameFrom(item.BackupAMFName, item.ExtendedBackupAMFName); !b.empty() {
			out.backups[g] = b
		}

		if item.GUAMIType != nil {
			out.types[g] = *item.GUAMIType
		}
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
		plmn, err := plmnid.Decode(item.PLMNIdentity)
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
		plmn, err := plmnid.Encode[ngap.PLMNIdentity](p.PLMN)
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

func causeLabel(c ngap.Cause) string {
	_, name := c.Value()
	if name == "" || strings.HasPrefix(name, "unknown(") {
		name = "unknown"
	}

	return assoc.CauseLabel(name)
}

func causeText(c *ngap.Cause) string {
	if c == nil {
		return "no cause"
	}

	return c.String()
}

func waitOf(t *ngap.TimeToWait) *time.Duration {
	if t == nil {
		return nil
	}

	d := timeToWait[*t]

	return &d
}
