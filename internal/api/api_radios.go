package api

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/s1"
)

type TA struct {
	TAC            uint32 `json:"tac"`
	BroadcastPLMNs []PLMN `json:"broadcast_plmns"`
}

type Radio struct {
	Ref                     string                  `json:"ref,omitempty"`
	Interface               string                  `json:"interface"`
	GlobalENBID             *GlobalENBID            `json:"global_enb_id,omitempty"`
	GlobalGNBID             *GlobalGNBID            `json:"global_gnb_id,omitempty"`
	ENBName                 string                  `json:"enb_name,omitempty"`
	RANNodeName             string                  `json:"ran_node_name,omitempty"`
	SupportedTAs            []TA                    `json:"supported_tas"`
	DefaultPagingDRX        int                     `json:"default_paging_drx,omitempty"`
	State                   string                  `json:"state"`
	ConnectedSince          time.Time               `json:"connected_since"`
	TNLAddresses            []string                `json:"tnl_addresses"`
	UEConnections           int                     `json:"ue_connections"`
	UEConnectionsByOperator []OperatorUEConnections `json:"ue_connections_by_operator,omitzero"`
}

type ListRadiosResponse struct {
	Items []Radio `json:"items"`
}

func ListRadios(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponse(r.Context(), w, ListRadiosResponse{Items: radios(cfg, ueConnections(cfg))}, http.StatusOK, cfg.Logger)
	})
}

func GetRadio(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref := r.PathValue("ref")
		ues := ueConnections(cfg)

		for _, resp := range radios(cfg, ues) {
			if resp.Ref == "" || resp.Ref != ref {
				continue
			}

			ops, err := cfg.Operators.ListOperators(r.Context())
			if err != nil {
				writeError(r.Context(), w, http.StatusInternalServerError, "Failed to list operators", err, cfg.Logger)
				return
			}

			resp.UEConnectionsByOperator = ueConnectionsByOperator(resp, ues, ops)
			writeResponse(r.Context(), w, resp, http.StatusOK, cfg.Logger)

			return
		}

		writeError(r.Context(), w, http.StatusNotFound, "Radio not found", nil, cfg.Logger)
	})
}

func radios(cfg Config, ues []ueRecord) []Radio {
	out := []Radio{}

	if cfg.S1 != nil {
		for _, n := range cfg.S1.RANNodes() {
			out = append(out, s1RadioResponse(n, ues))
		}
	}

	if cfg.NG != nil {
		for _, n := range cfg.NG.RANNodes() {
			out = append(out, ngRadioResponse(n, ues))
		}
	}

	slices.SortStableFunc(out, func(a, b Radio) int { return a.ConnectedSince.Compare(b.ConnectedSince) })

	return out
}

func onRadio(ref string) func(ueRecord) bool {
	return func(u ueRecord) bool { return u.conn.Radio.Ref == ref }
}

func s1RadioResponse(n s1.RANNode, ues []ueRecord) Radio {
	resp := Radio{
		Interface: metrics.InterfaceS1, ENBName: n.Name, SupportedTAs: make([]TA, 0, len(n.SupportedTAs)),
		DefaultPagingDRX: n.DefaultPagingDRX, State: string(n.State), ConnectedSince: n.ConnectedSince.UTC(),
		TNLAddresses: append([]string{}, n.TNLAddresses...),
	}

	if e := n.GlobalENBID; e != nil {
		resp.GlobalENBID = &GlobalENBID{PLMN: PLMN(e.PLMN), Type: e.Type, ID: int64(e.ID)}
		resp.Ref = radioRef(*e)
		resp.UEConnections = countUEConnections(ues, onRadio(resp.Ref))
	}

	for _, ta := range n.SupportedTAs {
		resp.SupportedTAs = append(resp.SupportedTAs, TA{TAC: uint32(ta.TAC), BroadcastPLMNs: plmnsResponse(ta.BroadcastPLMNs)})
	}

	return resp
}

func ngRadioResponse(n ng.RANNode, ues []ueRecord) Radio {
	resp := Radio{
		Interface: metrics.InterfaceNG, RANNodeName: n.Name, SupportedTAs: make([]TA, 0, len(n.SupportedTAs)),
		DefaultPagingDRX: n.DefaultPagingDRX, State: string(n.State), ConnectedSince: n.ConnectedSince.UTC(),
		TNLAddresses: append([]string{}, n.TNLAddresses...),
	}

	if g := n.GlobalGNBID; g != nil {
		resp.GlobalGNBID = &GlobalGNBID{PLMN: PLMN(g.PLMN), ID: int64(g.ID), Length: g.Length}
		resp.Ref = gnbRef(*g)
		resp.UEConnections = countUEConnections(ues, onRadio(resp.Ref))
	}

	for _, ta := range n.SupportedTAs {
		plmns := make([]PLMN, 0, len(ta.BroadcastPLMNs))
		for _, b := range ta.BroadcastPLMNs {
			plmns = append(plmns, PLMN(b.PLMN))
		}

		resp.SupportedTAs = append(resp.SupportedTAs, TA{TAC: ta.TAC, BroadcastPLMNs: plmns})
	}

	return resp
}

func (r Radio) name() string {
	return cmp.Or(r.ENBName, r.RANNodeName)
}

func radioRef(e db.GlobalENBID) string {
	return fmt.Sprintf("%s-%s:%s:%d", e.PLMN.MCC, e.PLMN.MNC, e.Type, e.ID)
}

func gnbRef(g db.GlobalGNBID) string {
	return fmt.Sprintf("%s-%s:gnb%d:%d", g.PLMN.MCC, g.PLMN.MNC, g.Length, g.ID)
}
