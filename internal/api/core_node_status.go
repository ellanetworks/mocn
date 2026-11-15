package api

import (
	"time"

	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/s1"
)

// TS 36.413 clause 9.1.8.5
type ServedGUMMEIsItem struct {
	ServedPLMNs    []PLMN   `json:"served_plmns"`
	ServedGroupIDs []uint16 `json:"served_group_ids"`
	ServedMMECs    []int    `json:"served_mmecs"`
}

// TS 36.413 clause 9.1.8.13
type MMEOverload struct {
	GUMMEI                         GUMMEI            `json:"gummei"`
	OverloadAction                 s1.OverloadAction `json:"overload_action"`
	TrafficLoadReductionIndication *uint8            `json:"traffic_load_reduction_indication,omitempty"`
}

type MMEStatus struct {
	State               s1.MMEState         `json:"state"`
	Since               time.Time           `json:"since"`
	PrimaryTNLAddress   string              `json:"primary_tnl_address,omitempty"`
	MMEName             string              `json:"mme_name,omitempty"`
	ServedGUMMEIs       []ServedGUMMEIsItem `json:"served_gummeis,omitempty"`
	RelativeMMECapacity *uint8              `json:"relative_mme_capacity,omitempty"`
	Overload            []MMEOverload       `json:"overload,omitempty"`
	UEConnections       int                 `json:"ue_connections"`
}

// TS 38.413 clause 9.2.6.2
type PLMNSupport struct {
	PLMN    PLMN     `json:"plmn"`
	SNSSAIs []SNSSAI `json:"snssais"`
}

// TS 38.413 clause 9.2.6.14
type SliceOverload struct {
	SNSSAIs                        []SNSSAI           `json:"snssais"`
	OverloadAction                 *ng.OverloadAction `json:"overload_action,omitempty"`
	TrafficLoadReductionIndication *uint8             `json:"traffic_load_reduction_indication,omitempty"`
}

// TS 38.413 clause 9.2.6.14
type AMFOverload struct {
	GUAMIs                         []GUAMI            `json:"guamis"`
	OverloadAction                 *ng.OverloadAction `json:"overload_action,omitempty"`
	TrafficLoadReductionIndication *uint8             `json:"traffic_load_reduction_indication,omitempty"`
	Slices                         []SliceOverload    `json:"slices,omitempty"`
}

type AMFStatus struct {
	State               ng.AMFState   `json:"state"`
	Since               time.Time     `json:"since"`
	PrimaryTNLAddress   string        `json:"primary_tnl_address,omitempty"`
	AMFName             string        `json:"amf_name,omitempty"`
	ServedGUAMIs        []GUAMI       `json:"served_guamis,omitempty"`
	PLMNSupport         []PLMNSupport `json:"plmn_support,omitempty"`
	RelativeAMFCapacity *uint8        `json:"relative_amf_capacity,omitempty"`
	UnavailableGUAMIs   []GUAMI       `json:"unavailable_guamis,omitempty"`
	Overload            []AMFOverload `json:"overload,omitempty"`
	UEConnections       int           `json:"ue_connections"`
}

func withStatus(cfg Config, o db.Operator, resp Operator, ues []ueRecord) Operator {
	if cfg.Plane != nil && resp.UserPlane != nil {
		if st, ok := cfg.Plane.Status(o.ID); ok {
			resp.UserPlane.Status = userPlaneStatusResponse(st)
		}
	}

	if cfg.S1 == nil && cfg.NG == nil {
		return resp
	}

	total := 0

	if cfg.S1 != nil {
		for i, m := range o.MMEs {
			n := countUEConnections(ues, onCoreNode(metrics.InterfaceS1, o.ID, m.TNLAddresses))
			total += n

			if st, ok := cfg.S1.MMEStatus(o.ID, m.TNLAddresses); ok {
				resp.MMEs[i].Status = mmeStatusResponse(st)
				resp.MMEs[i].Status.UEConnections = n
			}
		}
	}

	if cfg.NG != nil {
		for i, m := range o.AMFs {
			n := countUEConnections(ues, onCoreNode(metrics.InterfaceNG, o.ID, m.TNLAddresses))
			total += n

			if st, ok := cfg.NG.AMFStatus(o.ID, m.TNLAddresses); ok {
				resp.AMFs[i].Status = amfStatusResponse(st)
				resp.AMFs[i].Status.UEConnections = n
			}
		}
	}

	resp.UEConnections = &total

	return resp
}

func mmeStatusResponse(st s1.MMEStatus) *MMEStatus {
	resp := &MMEStatus{
		State: st.State, Since: st.Since.UTC(), PrimaryTNLAddress: st.PrimaryTNLAddress, MMEName: st.MMEName,
		RelativeMMECapacity: st.RelativeMMECapacity,
	}

	for _, g := range st.ServedGUMMEIs {
		resp.ServedGUMMEIs = append(resp.ServedGUMMEIs, ServedGUMMEIsItem{
			ServedPLMNs:    plmnsOfDB(g.PLMNs),
			ServedGroupIDs: append([]uint16{}, g.GroupIDs...),
			ServedMMECs:    mmecs(g.MMECodes),
		})
	}

	for _, o := range st.Overload {
		resp.Overload = append(resp.Overload, MMEOverload{
			GUMMEI: GUMMEI{
				PLMN: plmnOfDB(o.GUMMEI.PLMN), MMEGroupID: o.GUMMEI.MMEGroupID, MMECode: o.GUMMEI.MMECode,
			},
			OverloadAction:                 o.Action,
			TrafficLoadReductionIndication: o.TrafficLoadReductionIndication,
		})
	}

	return resp
}

func mmecs(codes []uint8) []int {
	out := make([]int, len(codes))
	for i, c := range codes {
		out[i] = int(c)
	}

	return out
}

func guamisResponse(in []ng.GUAMI) []GUAMI {
	out := make([]GUAMI, 0, len(in))
	for _, g := range in {
		out = append(out, GUAMI{PLMN: plmnOfDB(g.PLMN), AMFRegionID: g.AMFRegionID, AMFSetID: g.AMFSetID, AMFPointer: g.AMFPointer})
	}

	return out
}

func snssaisResponse(in []ng.SNSSAI) []SNSSAI {
	out := make([]SNSSAI, 0, len(in))
	for _, s := range in {
		out = append(out, SNSSAI(s))
	}

	return out
}

func amfStatusResponse(st ng.AMFStatus) *AMFStatus {
	resp := &AMFStatus{
		State: st.State, Since: st.Since.UTC(), PrimaryTNLAddress: st.PrimaryTNLAddress, AMFName: st.AMFName,
		RelativeAMFCapacity: st.RelativeAMFCapacity,
	}

	if len(st.ServedGUAMIs) > 0 {
		resp.ServedGUAMIs = guamisResponse(st.ServedGUAMIs)
	}

	if len(st.UnavailableGUAMIs) > 0 {
		resp.UnavailableGUAMIs = guamisResponse(st.UnavailableGUAMIs)
	}

	for _, p := range st.PLMNSupport {
		resp.PLMNSupport = append(resp.PLMNSupport, PLMNSupport{PLMN: plmnOfDB(p.PLMN), SNSSAIs: snssaisResponse(p.Slices)})
	}

	for _, o := range st.Overload {
		item := AMFOverload{
			GUAMIs: guamisResponse(o.GUAMIs), OverloadAction: o.Action, TrafficLoadReductionIndication: o.TrafficLoadReductionIndication,
		}

		for _, s := range o.Slices {
			item.Slices = append(item.Slices, SliceOverload{
				SNSSAIs: snssaisResponse(s.SNSSAIs), OverloadAction: s.Action,
				TrafficLoadReductionIndication: s.TrafficLoadReductionIndication,
			})
		}

		resp.Overload = append(resp.Overload, item)
	}

	return resp
}

func userPlaneStatusResponse(st gtpu.Status) *UserPlaneStatus {
	resp := &UserPlaneStatus{
		State: st.State, Reason: st.Reason, Interface: st.Interface, Addresses: make([]string, 0, len(st.Addresses)),
		Since: st.Since.UTC(),
	}

	for _, a := range st.Addresses {
		resp.Addresses = append(resp.Addresses, a.String())
	}

	return resp
}
