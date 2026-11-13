package api

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/s1"
)

type UEConnectionRadio struct {
	Ref         string `json:"ref"`
	ENBUES1APID uint32 `json:"enb_ue_s1ap_id"`
	MMEUES1APID uint32 `json:"mme_ue_s1ap_id"`
}

type UEConnectionMME struct {
	TNLAddresses []string `json:"tnl_addresses"`
	ENBUES1APID  uint32   `json:"enb_ue_s1ap_id"`
	MMEUES1APID  *uint32  `json:"mme_ue_s1ap_id"`
}

type TAI struct {
	PLMN PLMN   `json:"plmn"`
	TAC  uint16 `json:"tac"`
}

type ECGI struct {
	PLMN   PLMN   `json:"plmn"`
	CellID uint32 `json:"cell_id"`
}

type UEConnection struct {
	Radio    UEConnectionRadio `json:"radio"`
	MME      UEConnectionMME   `json:"mme"`
	Operator string            `json:"operator"`
	PLMN     PLMN              `json:"plmn"`
	Since    time.Time         `json:"since"`
	TAI      *TAI              `json:"tai"`
	ECGI     *ECGI             `json:"ecgi"`
}

type ListUEConnectionsResponse struct {
	Items []UEConnection `json:"items"`
}

func ListUEConnections(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		radio, radioSet := queryValue(r, "radio")
		operator, operatorSet := queryValue(r, "operator")
		resp := ListUEConnectionsResponse{Items: []UEConnection{}}

		for _, u := range ueConnections(cfg) {
			c := ueConnectionResponse(u)
			if (radioSet && c.Radio.Ref != radio) || (operatorSet && c.Operator != operator) {
				continue
			}

			resp.Items = append(resp.Items, c)
		}

		writeResponse(r.Context(), w, resp, http.StatusOK, cfg.Logger)
	})
}

func queryValue(r *http.Request, key string) (string, bool) {
	q := r.URL.Query()
	return q.Get(key), q.Has(key)
}

func ueConnections(cfg Config) []s1.UE {
	if cfg.S1 == nil {
		return nil
	}

	return cfg.S1.UEs()
}

func countUEConnections(ues []s1.UE, keep func(s1.UE) bool) int {
	n := 0

	for _, u := range ues {
		if keep(u) {
			n++
		}
	}

	return n
}

func onMME(operatorID int64, tnlAddresses []string) func(s1.UE) bool {
	return func(u s1.UE) bool {
		return u.OperatorID == operatorID && slices.Equal(u.MMETNLAddresses, tnlAddresses)
	}
}

func ueConnectionResponse(u s1.UE) UEConnection {
	resp := UEConnection{
		Radio: UEConnectionRadio{
			Ref: radioRef(u.RadioGlobalENBID), ENBUES1APID: u.RadioENBUES1APID, MMEUES1APID: u.RadioMMEUES1APID,
		},
		MME: UEConnectionMME{
			TNLAddresses: append([]string{}, u.MMETNLAddresses...), ENBUES1APID: u.MMEENBUES1APID,
			MMEUES1APID: u.MMEUES1APID,
		},
		Operator: u.OperatorName,
		PLMN:     PLMN(u.PLMN),
		Since:    u.Since.UTC(),
	}

	if t := u.TAI; t != nil {
		resp.TAI = &TAI{PLMN: PLMN(t.PLMN), TAC: t.TAC}
	}

	if e := u.ECGI; e != nil {
		resp.ECGI = &ECGI{PLMN: PLMN(e.PLMN), CellID: e.CellID}
	}

	return resp
}

type OperatorUEConnections struct {
	Operator      string `json:"operator"`
	UEConnections int    `json:"ue_connections"`
}

type RadioUEConnections struct {
	Radio         string `json:"radio"`
	Name          string `json:"name,omitempty"`
	UEConnections int    `json:"ue_connections"`
}

func ueConnectionsByOperator(n s1.RANNode, ues []s1.UE, ops []db.Operator) []OperatorUEConnections {
	names := make(map[int64]string, len(ops))
	owners := map[db.PLMN]string{}

	for _, o := range ops {
		names[o.ID] = o.Name
		for _, p := range o.PLMNs {
			owners[p] = o.Name
		}
	}

	counts := map[string]int{}

	for _, ta := range n.SupportedTAs {
		for _, p := range ta.BroadcastPLMNs {
			if name, ok := owners[p]; ok && !hasKey(counts, name) {
				counts[name] = 0
			}
		}
	}

	for _, u := range ues {
		if n.GlobalENBID == nil || u.RadioGlobalENBID != *n.GlobalENBID {
			continue
		}

		name, ok := names[u.OperatorID]
		if !ok {
			name = u.OperatorName
		}

		if name != "" {
			counts[name]++
		}
	}

	out := make([]OperatorUEConnections, 0, len(counts))
	for name, c := range counts {
		out = append(out, OperatorUEConnections{Operator: name, UEConnections: c})
	}

	slices.SortFunc(out, func(a, b OperatorUEConnections) int { return strings.Compare(a.Operator, b.Operator) })

	return out
}

func ueConnectionsByRadio(o db.Operator, nodes []s1.RANNode, ues []s1.UE) []RadioUEConnections {
	plmns := map[db.PLMN]bool{}
	for _, p := range o.PLMNs {
		plmns[p] = true
	}

	byRef := map[string]*RadioUEConnections{}

	for _, n := range nodes {
		if n.GlobalENBID == nil {
			continue
		}

		ref := radioRef(*n.GlobalENBID)
		if broadcastsAny(n, plmns) {
			byRef[ref] = &RadioUEConnections{Radio: ref, Name: n.Name}
		}
	}

	for _, u := range ues {
		if u.OperatorID != o.ID {
			continue
		}

		ref := radioRef(u.RadioGlobalENBID)
		if byRef[ref] == nil {
			byRef[ref] = &RadioUEConnections{Radio: ref, Name: radioName(nodes, u.RadioGlobalENBID)}
		}

		byRef[ref].UEConnections++
	}

	out := make([]RadioUEConnections, 0, len(byRef))
	for _, r := range byRef {
		out = append(out, *r)
	}

	slices.SortFunc(out, func(a, b RadioUEConnections) int {
		return cmp.Or(strings.Compare(cmp.Or(a.Name, a.Radio), cmp.Or(b.Name, b.Radio)), strings.Compare(a.Radio, b.Radio))
	})

	return out
}

func broadcastsAny(n s1.RANNode, plmns map[db.PLMN]bool) bool {
	for _, ta := range n.SupportedTAs {
		for _, p := range ta.BroadcastPLMNs {
			if plmns[p] {
				return true
			}
		}
	}

	return false
}

func radioName(nodes []s1.RANNode, id db.GlobalENBID) string {
	for _, n := range nodes {
		if n.GlobalENBID != nil && *n.GlobalENBID == id {
			return n.Name
		}
	}

	return ""
}

func hasKey(m map[string]int, k string) bool {
	_, ok := m[k]
	return ok
}
