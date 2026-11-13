package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/ellanetworks/mocn/internal/s1"
)

type TA struct {
	TAC            uint16 `json:"tac"`
	BroadcastPLMNs []PLMN `json:"broadcast_plmns"`
}

type Radio struct {
	Ref              string          `json:"ref,omitempty"`
	GlobalENBID      *GlobalENBID    `json:"global_enb_id"`
	ENBName          string          `json:"enb_name,omitempty"`
	SupportedTAs     []TA            `json:"supported_tas"`
	DefaultPagingDRX int             `json:"default_paging_drx,omitempty"`
	State            s1.RANNodeState `json:"state"`
	ConnectedSince   time.Time       `json:"connected_since"`
	TNLAddresses     []string        `json:"tnl_addresses"`
}

type ListRadiosResponse struct {
	Items []Radio `json:"items"`
}

func ListRadios(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ListRadiosResponse{Items: []Radio{}}

		if cfg.S1 != nil {
			for _, n := range cfg.S1.RANNodes() {
				resp.Items = append(resp.Items, radioResponse(n))
			}
		}

		writeResponse(r.Context(), w, resp, http.StatusOK, cfg.Logger)
	})
}

func GetRadio(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref := r.PathValue("ref")

		if cfg.S1 != nil {
			for _, n := range cfg.S1.RANNodes() {
				if resp := radioResponse(n); resp.Ref != "" && resp.Ref == ref {
					writeResponse(r.Context(), w, resp, http.StatusOK, cfg.Logger)
					return
				}
			}
		}

		writeError(r.Context(), w, http.StatusNotFound, "Radio not found", nil, cfg.Logger)
	})
}

func radioResponse(n s1.RANNode) Radio {
	resp := Radio{
		ENBName: n.Name, SupportedTAs: make([]TA, 0, len(n.SupportedTAs)), DefaultPagingDRX: n.DefaultPagingDRX,
		State: n.State, ConnectedSince: n.ConnectedSince.UTC(), TNLAddresses: append([]string{}, n.TNLAddresses...),
	}

	if e := n.GlobalENBID; e != nil {
		resp.GlobalENBID = &GlobalENBID{PLMN: PLMN(e.PLMN), Type: e.Type, ID: int64(e.ID)}
		resp.Ref = fmt.Sprintf("%s-%s:%s:%d", e.PLMN.MCC, e.PLMN.MNC, e.Type, e.ID)
	}

	for _, ta := range n.SupportedTAs {
		resp.SupportedTAs = append(resp.SupportedTAs, TA{TAC: ta.TAC, BroadcastPLMNs: plmnsResponse(ta.BroadcastPLMNs)})
	}

	return resp
}
