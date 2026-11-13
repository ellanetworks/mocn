package api

import (
	"net/http"
	"time"

	"github.com/ellanetworks/mocn/internal/s1"
)

type TA struct {
	TAC            uint16 `json:"tac"`
	BroadcastPLMNs []PLMN `json:"broadcast_plmns"`
}

type RANNode struct {
	GlobalENBID      *GlobalENBID    `json:"global_enb_id"`
	ENBName          string          `json:"enb_name,omitempty"`
	SupportedTAs     []TA            `json:"supported_tas"`
	DefaultPagingDRX int             `json:"default_paging_drx,omitempty"`
	State            s1.RANNodeState `json:"state"`
	ConnectedSince   time.Time       `json:"connected_since"`
	TNLAddresses     []string        `json:"tnl_addresses"`
	LastFailureCause string          `json:"last_failure_cause,omitempty"`
}

type ListRANNodesResponse struct {
	Items []RANNode `json:"items"`
}

func ListRANNodes(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ListRANNodesResponse{Items: []RANNode{}}

		if cfg.S1 != nil {
			for _, n := range cfg.S1.RANNodes() {
				resp.Items = append(resp.Items, ranNodeResponse(n))
			}
		}

		writeResponse(r.Context(), w, resp, http.StatusOK, cfg.Logger)
	})
}

func ranNodeResponse(n s1.RANNode) RANNode {
	resp := RANNode{
		ENBName: n.Name, SupportedTAs: make([]TA, 0, len(n.SupportedTAs)), DefaultPagingDRX: n.DefaultPagingDRX,
		State: n.State, ConnectedSince: n.ConnectedSince.UTC(), TNLAddresses: append([]string{}, n.TNLAddresses...),
		LastFailureCause: n.LastFailureCause,
	}

	if e := n.GlobalENBID; e != nil {
		resp.GlobalENBID = &GlobalENBID{PLMN: PLMN(e.PLMN), Type: e.Type, ID: int64(e.ID)}
	}

	for _, ta := range n.SupportedTAs {
		resp.SupportedTAs = append(resp.SupportedTAs, TA{TAC: ta.TAC, BroadcastPLMNs: plmnsResponse(ta.BroadcastPLMNs)})
	}

	return resp
}
