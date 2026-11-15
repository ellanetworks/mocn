package api

import (
	"net/http"
	"slices"
	"time"

	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/s1"
)

// TS 36.413 clause 9.1.8.4
// TS 38.413 clause 9.2.6.1
type BroadcastPLMN struct {
	PLMN    PLMN     `json:"plmn"`
	SNSSAIs []SNSSAI `json:"snssais,omitempty"`
}

// TS 36.413 clause 9.1.8.4
// TS 38.413 clause 9.2.6.1
type SupportedTA struct {
	TAC            string          `json:"tac"`
	BroadcastPLMNs []BroadcastPLMN `json:"broadcast_plmns"`
}

type RadioSetupFailure struct {
	Cause  string    `json:"cause"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

type Radio struct {
	Ref                     string                  `json:"ref,omitempty"`
	Type                    string                  `json:"type"`
	GlobalENBID             *GlobalENBID            `json:"global_enb_id,omitempty"`
	GlobalGNBID             *GlobalGNBID            `json:"global_gnb_id,omitempty"`
	Name                    string                  `json:"name,omitempty"`
	SupportedTAs            []SupportedTA           `json:"supported_tas"`
	DefaultPagingDRX        int                     `json:"default_paging_drx,omitempty"`
	State                   string                  `json:"state"`
	SetupFailure            *RadioSetupFailure      `json:"setup_failure,omitempty"`
	ConnectedAt             time.Time               `json:"connected_at"`
	TNLAddresses            []string                `json:"tnl_addresses"`
	UEConnections           int                     `json:"ue_connections"`
	UEConnectionsByOperator []OperatorUEConnections `json:"ue_connections_by_operator,omitempty"`
}

type ListRadiosResponse struct {
	Items      []Radio `json:"items"`
	Page       int     `json:"page"`
	PerPage    int     `json:"per_page"`
	TotalCount int     `json:"total_count"`
}

func listRadios(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, perPage, ok := pagination(w, r, cfg)
		if !ok {
			return
		}

		all := radios(cfg, ueConnections(cfg))
		resp := ListRadiosResponse{Items: pageOf(all, page, perPage), Page: page, PerPage: perPage, TotalCount: len(all)}
		writeResponse(r.Context(), w, resp, http.StatusOK, cfg.Logger)
	})
}

func getRadio(cfg Config) http.Handler {
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

	slices.SortStableFunc(out, func(a, b Radio) int { return a.ConnectedAt.Compare(b.ConnectedAt) })

	return out
}

func onRadio(ref string) func(ueRecord) bool {
	return func(u ueRecord) bool { return u.conn.Radio.Ref == ref }
}

func s1RadioResponse(n s1.RANNode, ues []ueRecord) Radio {
	resp := Radio{
		Type: ranNodeTypeENB, Name: n.Name,
		SupportedTAs: make([]SupportedTA, 0, len(n.SupportedTAs)), DefaultPagingDRX: n.DefaultPagingDRX,
		State: string(n.State), ConnectedAt: n.ConnectedSince.UTC(), TNLAddresses: append([]string{}, n.TNLAddresses...),
	}

	if e := n.GlobalENBID; e != nil {
		resp.GlobalENBID = globalENBIDOfDB(*e)
	}

	if f := n.SetupFailure; f != nil {
		resp.SetupFailure = &RadioSetupFailure{Cause: f.Cause, Reason: f.Reason, At: f.At.UTC()}
	} else if e := n.GlobalENBID; e != nil {
		resp.Ref = e.Ref()
		resp.UEConnections = countUEConnections(ues, onRadio(resp.Ref))
	}

	for _, ta := range n.SupportedTAs {
		plmns := make([]BroadcastPLMN, 0, len(ta.BroadcastPLMNs))
		for _, p := range ta.BroadcastPLMNs {
			plmns = append(plmns, BroadcastPLMN{PLMN: plmnOfDB(p)})
		}

		resp.SupportedTAs = append(resp.SupportedTAs, SupportedTA{TAC: tacString(uint32(ta.TAC)), BroadcastPLMNs: plmns})
	}

	return resp
}

func ngRadioResponse(n ng.RANNode, ues []ueRecord) Radio {
	resp := Radio{
		Type: ranNodeTypeGNB, Name: n.Name,
		SupportedTAs: make([]SupportedTA, 0, len(n.SupportedTAs)), DefaultPagingDRX: n.DefaultPagingDRX,
		State: string(n.State), ConnectedAt: n.ConnectedSince.UTC(), TNLAddresses: append([]string{}, n.TNLAddresses...),
	}

	if g := n.GlobalGNBID; g != nil {
		resp.GlobalGNBID = globalGNBIDOfDB(*g)
	}

	if f := n.SetupFailure; f != nil {
		resp.SetupFailure = &RadioSetupFailure{Cause: f.Cause, Reason: f.Reason, At: f.At.UTC()}
	} else if g := n.GlobalGNBID; g != nil {
		resp.Ref = g.Ref()
		resp.UEConnections = countUEConnections(ues, onRadio(resp.Ref))
	}

	for _, ta := range n.SupportedTAs {
		plmns := make([]BroadcastPLMN, 0, len(ta.BroadcastPLMNs))
		for _, b := range ta.BroadcastPLMNs {
			plmns = append(plmns, BroadcastPLMN{PLMN: plmnOfDB(b.PLMN), SNSSAIs: snssaisResponse(b.Slices)})
		}

		resp.SupportedTAs = append(resp.SupportedTAs, SupportedTA{TAC: tacString(ta.TAC), BroadcastPLMNs: plmns})
	}

	return resp
}
