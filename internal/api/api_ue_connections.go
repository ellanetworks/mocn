package api

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/s1"
)

type UEConnectionRadio struct {
	Ref         string  `json:"ref"`
	ENBUES1APID *uint32 `json:"enb_ue_s1ap_id,omitempty"`
	MMEUES1APID *uint32 `json:"mme_ue_s1ap_id,omitempty"`
	RANUENGAPID *uint32 `json:"ran_ue_ngap_id,omitempty"`
	AMFUENGAPID *uint64 `json:"amf_ue_ngap_id,omitempty"`
}

type UEConnectionMME struct {
	TNLAddresses []string `json:"tnl_addresses"`
	ENBUES1APID  uint32   `json:"enb_ue_s1ap_id"`
	MMEUES1APID  *uint32  `json:"mme_ue_s1ap_id"`
}

type UEConnectionAMF struct {
	TNLAddresses []string `json:"tnl_addresses"`
	RANUENGAPID  uint32   `json:"ran_ue_ngap_id"`
	AMFUENGAPID  *uint64  `json:"amf_ue_ngap_id"`
}

type TAI struct {
	PLMN PLMN   `json:"plmn"`
	TAC  uint32 `json:"tac"`
}

type ECGI struct {
	PLMN   PLMN   `json:"plmn"`
	CellID uint32 `json:"cell_id"`
}

type NRCGI struct {
	PLMN   PLMN   `json:"plmn"`
	CellID uint64 `json:"cell_id"`
}

type UEConnection struct {
	Interface string            `json:"interface"`
	Radio     UEConnectionRadio `json:"radio"`
	MME       *UEConnectionMME  `json:"mme,omitempty"`
	AMF       *UEConnectionAMF  `json:"amf,omitempty"`
	Operator  string            `json:"operator"`
	PLMN      PLMN              `json:"plmn"`
	Since     time.Time         `json:"since"`
	TAI       *TAI              `json:"tai"`
	ECGI      *ECGI             `json:"ecgi,omitempty"`
	NRCGI     *NRCGI            `json:"nr_cgi,omitempty"`
}

type ListUEConnectionsResponse struct {
	Items []UEConnection `json:"items"`
}

type ueRecord struct {
	conn       UEConnection
	operatorID int64
}

func ListUEConnections(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		radio, radioSet := queryValue(r, "radio")
		operator, operatorSet := queryValue(r, "operator")
		resp := ListUEConnectionsResponse{Items: []UEConnection{}}

		for _, u := range ueConnections(cfg) {
			c := u.conn
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

func ueConnections(cfg Config) []ueRecord {
	var out []ueRecord

	if cfg.S1 != nil {
		for _, u := range cfg.S1.UEs() {
			out = append(out, ueRecord{conn: s1UEConnection(u), operatorID: u.OperatorID})
		}
	}

	if cfg.NG != nil {
		for _, u := range cfg.NG.UEs() {
			out = append(out, ueRecord{conn: ngUEConnection(u), operatorID: u.OperatorID})
		}
	}

	slices.SortStableFunc(out, func(a, b ueRecord) int { return a.conn.Since.Compare(b.conn.Since) })

	return out
}

func countUEConnections(ues []ueRecord, keep func(ueRecord) bool) int {
	n := 0

	for _, u := range ues {
		if keep(u) {
			n++
		}
	}

	return n
}

func onCoreNode(iface string, operatorID int64, tnlAddresses []string) func(ueRecord) bool {
	return func(u ueRecord) bool {
		if u.conn.Interface != iface || u.operatorID != operatorID {
			return false
		}

		if u.conn.MME != nil {
			return slices.Equal(u.conn.MME.TNLAddresses, tnlAddresses)
		}

		return u.conn.AMF != nil && slices.Equal(u.conn.AMF.TNLAddresses, tnlAddresses)
	}
}

func s1UEConnection(u s1.UE) UEConnection {
	resp := UEConnection{
		Interface: metrics.InterfaceS1,
		Radio: UEConnectionRadio{
			Ref: radioRef(u.RadioGlobalENBID), ENBUES1APID: new(u.RadioENBUES1APID), MMEUES1APID: new(u.RadioMMEUES1APID),
		},
		MME: &UEConnectionMME{
			TNLAddresses: append([]string{}, u.MMETNLAddresses...), ENBUES1APID: u.MMEENBUES1APID,
			MMEUES1APID: u.MMEUES1APID,
		},
		Operator: u.OperatorName,
		PLMN:     PLMN(u.PLMN),
		Since:    u.Since.UTC(),
	}

	if t := u.TAI; t != nil {
		resp.TAI = &TAI{PLMN: PLMN(t.PLMN), TAC: uint32(t.TAC)}
	}

	if e := u.ECGI; e != nil {
		resp.ECGI = &ECGI{PLMN: PLMN(e.PLMN), CellID: e.CellID}
	}

	return resp
}

func ngUEConnection(u ng.UE) UEConnection {
	resp := UEConnection{
		Interface: metrics.InterfaceNG,
		Radio: UEConnectionRadio{
			Ref: gnbRef(u.RadioGlobalGNBID), RANUENGAPID: new(u.RadioRANUENGAPID), AMFUENGAPID: new(u.RadioAMFUENGAPID),
		},
		AMF: &UEConnectionAMF{
			TNLAddresses: append([]string{}, u.AMFTNLAddresses...), RANUENGAPID: u.AMFRANUENGAPID,
			AMFUENGAPID: u.AMFUENGAPID,
		},
		Operator: u.OperatorName,
		PLMN:     PLMN(u.PLMN),
		Since:    u.Since.UTC(),
	}

	if t := u.TAI; t != nil {
		resp.TAI = &TAI{PLMN: PLMN(t.PLMN), TAC: t.TAC}
	}

	if c := u.NRCGI; c != nil {
		resp.NRCGI = &NRCGI{PLMN: PLMN(c.PLMN), CellID: c.CellID}
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

func ueConnectionsByOperator(n Radio, ues []ueRecord, ops []db.Operator) []OperatorUEConnections {
	names := make(map[int64]string, len(ops))
	owners := map[PLMN]string{}

	for _, o := range ops {
		names[o.ID] = o.Name
		for _, p := range o.PLMNs {
			owners[PLMN(p)] = o.Name
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
		if n.Ref == "" || u.conn.Radio.Ref != n.Ref {
			continue
		}

		name, ok := names[u.operatorID]
		if !ok {
			name = u.conn.Operator
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

func ueConnectionsByRadio(o db.Operator, nodes []Radio, ues []ueRecord) []RadioUEConnections {
	plmns := map[PLMN]bool{}
	for _, p := range o.PLMNs {
		plmns[PLMN(p)] = true
	}

	names := map[string]string{}
	byRef := map[string]*RadioUEConnections{}

	for _, n := range nodes {
		if n.Ref == "" {
			continue
		}

		names[n.Ref] = n.name()

		if broadcastsAny(n, plmns) {
			byRef[n.Ref] = &RadioUEConnections{Radio: n.Ref, Name: n.name()}
		}
	}

	for _, u := range ues {
		if u.operatorID != o.ID {
			continue
		}

		ref := u.conn.Radio.Ref
		if byRef[ref] == nil {
			byRef[ref] = &RadioUEConnections{Radio: ref, Name: names[ref]}
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

func broadcastsAny(n Radio, plmns map[PLMN]bool) bool {
	for _, ta := range n.SupportedTAs {
		for _, p := range ta.BroadcastPLMNs {
			if plmns[p] {
				return true
			}
		}
	}

	return false
}

func hasKey(m map[string]int, k string) bool {
	_, ok := m[k]
	return ok
}
