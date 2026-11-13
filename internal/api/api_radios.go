package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/s1"
)

type TA struct {
	TAC            uint16 `json:"tac"`
	BroadcastPLMNs []PLMN `json:"broadcast_plmns"`
}

type Radio struct {
	Ref                     string                  `json:"ref,omitempty"`
	GlobalENBID             *GlobalENBID            `json:"global_enb_id"`
	ENBName                 string                  `json:"enb_name,omitempty"`
	SupportedTAs            []TA                    `json:"supported_tas"`
	DefaultPagingDRX        int                     `json:"default_paging_drx,omitempty"`
	State                   s1.RANNodeState         `json:"state"`
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
		resp := ListRadiosResponse{Items: []Radio{}}

		if cfg.S1 != nil {
			ues := cfg.S1.UEs()
			for _, n := range cfg.S1.RANNodes() {
				resp.Items = append(resp.Items, radioResponse(n, ues))
			}
		}

		writeResponse(r.Context(), w, resp, http.StatusOK, cfg.Logger)
	})
}

func GetRadio(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref := r.PathValue("ref")

		if cfg.S1 != nil {
			ues := cfg.S1.UEs()
			for _, n := range cfg.S1.RANNodes() {
				resp := radioResponse(n, ues)
				if resp.Ref == "" || resp.Ref != ref {
					continue
				}

				ops, err := cfg.Operators.ListOperators(r.Context())
				if err != nil {
					writeError(r.Context(), w, http.StatusInternalServerError, "Failed to list operators", err, cfg.Logger)
					return
				}

				resp.UEConnectionsByOperator = ueConnectionsByOperator(n, ues, ops)
				writeResponse(r.Context(), w, resp, http.StatusOK, cfg.Logger)

				return
			}
		}

		writeError(r.Context(), w, http.StatusNotFound, "Radio not found", nil, cfg.Logger)
	})
}

func radioResponse(n s1.RANNode, ues []s1.UE) Radio {
	resp := Radio{
		ENBName: n.Name, SupportedTAs: make([]TA, 0, len(n.SupportedTAs)), DefaultPagingDRX: n.DefaultPagingDRX,
		State: n.State, ConnectedSince: n.ConnectedSince.UTC(), TNLAddresses: append([]string{}, n.TNLAddresses...),
	}

	if e := n.GlobalENBID; e != nil {
		resp.GlobalENBID = &GlobalENBID{PLMN: PLMN(e.PLMN), Type: e.Type, ID: int64(e.ID)}
		resp.Ref = radioRef(*e)
		resp.UEConnections = countUEConnections(ues, func(u s1.UE) bool { return u.RadioGlobalENBID == *e })
	}

	for _, ta := range n.SupportedTAs {
		resp.SupportedTAs = append(resp.SupportedTAs, TA{TAC: ta.TAC, BroadcastPLMNs: plmnsResponse(ta.BroadcastPLMNs)})
	}

	return resp
}

func radioRef(e db.GlobalENBID) string {
	return fmt.Sprintf("%s-%s:%s:%d", e.PLMN.MCC, e.PLMN.MNC, e.Type, e.ID)
}
