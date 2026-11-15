package s1

import (
	"fmt"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/plmnid"
)

const (
	maxnoofTACs        = 256
	maxnoofBPLMNs      = 6
	maxnoofPLMNsPerMME = 32
	maxnoofGroupIDs    = 65535
)

// TS 36.413 clause 9.2.1.37
func globalENBIDFrom(g s1ap.GlobalENBID) (db.GlobalENBID, error) {
	plmn, err := plmnid.Decode(g.PLMNIdentity)
	if err != nil {
		return db.GlobalENBID{}, err
	}

	id, ok := g.ENBID.Value()

	var t db.ENBIDType

	switch {
	case g.ENBID.MacroENBID != nil:
		t = db.ENBIDMacro
	case g.ENBID.HomeENBID != nil:
		t = db.ENBIDHome
	case g.ENBID.ShortMacroENBID != nil:
		t = db.ENBIDShortMacro
	case g.ENBID.LongMacroENBID != nil:
		t = db.ENBIDLongMacro
	}

	if !ok {
		return db.GlobalENBID{}, fmt.Errorf("unknown eNB ID kind %q", g.ENBID.AlternativeName())
	}

	return db.GlobalENBID{PLMN: plmn, Type: t, ID: id}, nil
}

// TS 36.413 clause 9.2.1.37
func globalENBIDTo(g db.GlobalENBID) (s1ap.GlobalENBID, error) {
	plmn, err := plmnid.Encode[s1ap.PLMNIdentity](g.PLMN)
	if err != nil {
		return s1ap.GlobalENBID{}, err
	}

	var id s1ap.ENBID

	switch g.Type {
	case db.ENBIDMacro:
		id.MacroENBID = s1ap.Ptr(s1ap.MacroENBID(g.ID))
	case db.ENBIDHome:
		id.HomeENBID = s1ap.Ptr(s1ap.HomeENBID(g.ID))
	case db.ENBIDShortMacro:
		id.ShortMacroENBID = s1ap.Ptr(s1ap.ShortMacroENBID(g.ID))
	case db.ENBIDLongMacro:
		id.LongMacroENBID = s1ap.Ptr(s1ap.LongMacroENBID(g.ID))
	default:
		return s1ap.GlobalENBID{}, fmt.Errorf("unknown eNB ID type %q", g.Type)
	}

	return s1ap.GlobalENBID{PLMNIdentity: plmn, ENBID: id}, nil
}

func plmnsFrom(ids []s1ap.PLMNIdentity) ([]db.PLMN, error) {
	out := make([]db.PLMN, 0, len(ids))

	for _, id := range ids {
		p, err := plmnid.Decode(id)
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, nil
}

func plmnsTo(plmns []db.PLMN) ([]s1ap.PLMNIdentity, error) {
	out := make([]s1ap.PLMNIdentity, 0, len(plmns))

	for _, p := range plmns {
		id, err := plmnid.Encode[s1ap.PLMNIdentity](p)
		if err != nil {
			return nil, err
		}

		out = append(out, id)
	}

	return out, nil
}

// TS 36.413 clause 9.1.8.4
func tasFrom(in s1ap.SupportedTAs) ([]TA, error) {
	out := make([]TA, 0, len(in))

	for _, ta := range in {
		plmns, err := plmnsFrom(ta.BroadcastPLMNs)
		if err != nil {
			return nil, err
		}

		out = append(out, TA{TAC: uint16(ta.TAC), BroadcastPLMNs: plmns})
	}

	return out, nil
}

// TS 36.413 clause 9.1.8.4
func tasTo(in []TA) (s1ap.SupportedTAs, error) {
	out := make(s1ap.SupportedTAs, 0, len(in))

	for _, ta := range in {
		plmns, err := plmnsTo(ta.BroadcastPLMNs)
		if err != nil {
			return nil, err
		}

		out = append(out, s1ap.SupportedTAItem{TAC: s1ap.TAC(ta.TAC), BroadcastPLMNs: plmns})
	}

	return out, nil
}

// TS 36.413 clause 9.1.8.5
func gummeisFrom(in s1ap.ServedGUMMEIs) ([]ServedGUMMEIs, error) {
	out := make([]ServedGUMMEIs, 0, len(in))

	for _, item := range in {
		plmns, err := plmnsFrom(item.ServedPLMNs)
		if err != nil {
			return nil, err
		}

		g := ServedGUMMEIs{PLMNs: plmns}

		for _, id := range item.ServedGroupIDs {
			g.GroupIDs = append(g.GroupIDs, uint16(id[0])<<8|uint16(id[1]))
		}

		for _, c := range item.ServedMMECs {
			g.MMECodes = append(g.MMECodes, uint8(c))
		}

		out = append(out, g)
	}

	return out, nil
}

// TS 36.413 clause 9.1.8.5
func gummeisTo(in []ServedGUMMEIs) (s1ap.ServedGUMMEIs, error) {
	out := make(s1ap.ServedGUMMEIs, 0, len(in))

	for _, g := range in {
		plmns, err := plmnsTo(g.PLMNs)
		if err != nil {
			return nil, err
		}

		item := s1ap.ServedGUMMEIsItem{ServedPLMNs: plmns}

		for _, id := range g.GroupIDs {
			item.ServedGroupIDs = append(item.ServedGroupIDs, s1ap.MMEGroupID{byte(id >> 8), byte(id)})
		}

		for _, c := range g.MMECodes {
			item.ServedMMECs = append(item.ServedMMECs, s1ap.MMECode(c))
		}

		out = append(out, item)
	}

	return out, nil
}

// TS 36.413 clause 9.2.1.16
func pagingDRXFrames(d s1ap.PagingDRX) int {
	return 32 << d
}

// TS 36.413 clause 9.2.1.61
var timeToWait = map[s1ap.TimeToWait]time.Duration{
	s1ap.TimeToWaitV1s:  time.Second,
	s1ap.TimeToWaitV2s:  2 * time.Second,
	s1ap.TimeToWaitV5s:  5 * time.Second,
	s1ap.TimeToWaitV10s: 10 * time.Second,
	s1ap.TimeToWaitV20s: 20 * time.Second,
	s1ap.TimeToWaitV60s: 60 * time.Second,
}

func causeLabel(c s1ap.Cause) string {
	name, _ := c.ValueName()

	return assoc.CauseLabel(name)
}

func causeText(c *s1ap.Cause) string {
	if c == nil {
		return "no cause"
	}

	return c.String()
}

func waitOf(t *s1ap.TimeToWait) *time.Duration {
	if t == nil {
		return nil
	}

	d := timeToWait[*t]

	return &d
}
