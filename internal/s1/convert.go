package s1

import (
	"fmt"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
)

const (
	maxnoofTACs        = 256
	maxnoofBPLMNs      = 6
	maxnoofPLMNsPerMME = 32
	maxnoofGroupIDs    = 65535
)

var enbIDKinds = map[s1ap.ENBIDKind]db.ENBIDType{
	s1ap.ENBIDMacro:      db.ENBIDMacro,
	s1ap.ENBIDHome:       ENBIDHome,
	s1ap.ENBIDShortMacro: db.ENBIDShortMacro,
	s1ap.ENBIDLongMacro:  db.ENBIDLongMacro,
}

// TS 36.413 clause 9.2.1.37
func globalENBIDFrom(g s1ap.GlobalENBID) (db.GlobalENBID, error) {
	plmn, err := decodePLMN(g.PLMNIdentity)
	if err != nil {
		return db.GlobalENBID{}, err
	}

	t, ok := enbIDKinds[g.ENBID.Kind]
	if !ok {
		return db.GlobalENBID{}, fmt.Errorf("unknown eNB ID kind %d", g.ENBID.Kind)
	}

	return db.GlobalENBID{PLMN: plmn, Type: t, ID: g.ENBID.Value}, nil
}

// TS 36.413 clause 9.2.1.37
func globalENBIDTo(g db.GlobalENBID) (s1ap.GlobalENBID, error) {
	plmn, err := encodePLMN(g.PLMN)
	if err != nil {
		return s1ap.GlobalENBID{}, err
	}

	for kind, t := range enbIDKinds {
		if t == g.Type {
			return s1ap.GlobalENBID{PLMNIdentity: plmn, ENBID: s1ap.ENBID{Kind: kind, Value: g.ID}}, nil
		}
	}

	return s1ap.GlobalENBID{}, fmt.Errorf("unknown eNB ID type %q", g.Type)
}

func plmnsFrom(ids []s1ap.PLMNIdentity) ([]db.PLMN, error) {
	out := make([]db.PLMN, 0, len(ids))

	for _, id := range ids {
		p, err := decodePLMN(id)
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
		id, err := encodePLMN(p)
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

func causeText(c *s1ap.Cause) string {
	if c == nil {
		return "no cause"
	}

	return c.String()
}
