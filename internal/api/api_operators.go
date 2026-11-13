package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/s1"
)

const (
	maxOperatorNameLength = 64
	maxOperatorBody       = 64 << 10
	minGNBIDLength        = 22
	maxGNBIDLength        = 32
)

type OperatorStore interface {
	CreateOperator(ctx context.Context, o db.Operator) (int64, error)
	UpdateOperator(ctx context.Context, o db.Operator) error
	DeleteOperator(ctx context.Context, id int64) error
	GetOperator(ctx context.Context, name string) (db.Operator, error)
	ListOperators(ctx context.Context) ([]db.Operator, error)
}

type PLMN struct {
	MCC string `json:"mcc"`
	MNC string `json:"mnc"`
}

type GlobalENBID struct {
	PLMN PLMN         `json:"plmn"`
	Type db.ENBIDType `json:"enb_id_type"`
	ID   int64        `json:"enb_id"`
}

type GlobalGNBID struct {
	PLMN   PLMN  `json:"plmn"`
	ID     int64 `json:"gnb_id"`
	Length int   `json:"gnb_id_length"`
}

type Operator struct {
	Name                 string               `json:"name"`
	PWSOperator          bool                 `json:"pws_operator"`
	PLMNs                []PLMN               `json:"plmns"`
	GlobalENBID          *GlobalENBID         `json:"global_enb_id"`
	GlobalGNBID          *GlobalGNBID         `json:"global_gnb_id"`
	MMEs                 []MME                `json:"mmes"`
	AMFs                 []CoreNode           `json:"amfs"`
	UEConnections        *int                 `json:"ue_connections,omitempty"`
	UEConnectionsByRadio []RadioUEConnections `json:"ue_connections_by_radio,omitzero"`
}

type CoreNode struct {
	TNLAddresses []string `json:"tnl_addresses"`
}

type MME struct {
	TNLAddresses []string   `json:"tnl_addresses"`
	Status       *MMEStatus `json:"status,omitempty"`
}

type ServedGUMMEIs struct {
	ServedPLMNs    []PLMN   `json:"served_plmns"`
	ServedGroupIDs []uint16 `json:"served_group_ids"`
	ServedMMECs    []int    `json:"served_mmecs"`
}

type MMEStatus struct {
	State               s1.MMEState     `json:"state"`
	Since               time.Time       `json:"since"`
	PrimaryTNLAddress   string          `json:"primary_tnl_address,omitempty"`
	MMEName             string          `json:"mme_name,omitempty"`
	ServedGUMMEIs       []ServedGUMMEIs `json:"served_gummeis,omitempty"`
	RelativeMMECapacity *uint8          `json:"relative_mme_capacity,omitempty"`
	Overload            []Overload      `json:"overload,omitempty"`
	UEConnections       int             `json:"ue_connections"`
}

type GUMMEI struct {
	PLMN       PLMN   `json:"plmn"`
	MMEGroupID uint16 `json:"mme_group_id"`
	MMECode    uint8  `json:"mme_code"`
}

type Overload struct {
	GUMMEI                         GUMMEI            `json:"gummei"`
	OverloadAction                 s1.OverloadAction `json:"overload_action"`
	TrafficLoadReductionIndication *uint8            `json:"traffic_load_reduction_indication,omitempty"`
}

type S1View interface {
	MMEStatus(operatorID int64, tnlAddresses []string) (s1.MMEStatus, bool)
	RANNodes() []s1.RANNode
	UEs() []s1.UE
}

type ListOperatorsResponse struct {
	Items []Operator `json:"items"`
}

var enbIDBits = map[db.ENBIDType]uint{
	db.ENBIDMacro:      20,
	db.ENBIDShortMacro: 18,
	db.ENBIDLongMacro:  21,
}

func ListOperators(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ops, err := cfg.Operators.ListOperators(r.Context())
		if err != nil {
			writeError(r.Context(), w, http.StatusInternalServerError, "Failed to list operators", err, cfg.Logger)
			return
		}

		ues := ueConnections(cfg)

		resp := ListOperatorsResponse{Items: make([]Operator, 0, len(ops))}
		for _, o := range ops {
			resp.Items = append(resp.Items, withStatus(cfg, o, operatorResponse(o), ues))
		}

		writeResponse(r.Context(), w, resp, http.StatusOK, cfg.Logger)
	})
}

func GetOperator(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o, ok := lookupOperator(w, r, cfg)
		if !ok {
			return
		}

		ues := ueConnections(cfg)
		resp := withStatus(cfg, o, operatorResponse(o), ues)

		if cfg.S1 != nil {
			resp.UEConnectionsByRadio = ueConnectionsByRadio(o, cfg.S1.RANNodes(), ues)
		}

		writeResponse(r.Context(), w, resp, http.StatusOK, cfg.Logger)
	})
}

func CreateOperator(a *auth) http.Handler {
	cfg := a.cfg

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o, ok := operatorParams(w, r, cfg)
		if !ok {
			return
		}

		if _, err := cfg.Operators.CreateOperator(r.Context(), o); err != nil {
			writeOperatorError(w, r, cfg, err, "Failed to create operator")
			return
		}

		p, _ := PrincipalFrom(r.Context())
		a.audit(r, "create_operator", p.Email, "Operator "+o.Name+" created")
		writeResponse(r.Context(), w, operatorResponse(o), http.StatusCreated, cfg.Logger)
	})
}

func UpdateOperator(a *auth) http.Handler {
	cfg := a.cfg

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o, ok := operatorParams(w, r, cfg)
		if !ok {
			return
		}

		old, ok := lookupOperator(w, r, cfg)
		if !ok {
			return
		}

		o.ID = old.ID

		if err := cfg.Operators.UpdateOperator(r.Context(), o); err != nil {
			writeOperatorError(w, r, cfg, err, "Failed to update operator")
			return
		}

		details := "Operator " + old.Name + " updated"
		if o.Name != old.Name {
			details = fmt.Sprintf("Operator %s updated and renamed %s", old.Name, o.Name)
		}

		p, _ := PrincipalFrom(r.Context())
		a.audit(r, "update_operator", p.Email, details)
		writeResponse(r.Context(), w, operatorResponse(o), http.StatusOK, cfg.Logger)
	})
}

func DeleteOperator(a *auth) http.Handler {
	cfg := a.cfg

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o, ok := lookupOperator(w, r, cfg)
		if !ok {
			return
		}

		if err := cfg.Operators.DeleteOperator(r.Context(), o.ID); err != nil {
			writeOperatorError(w, r, cfg, err, "Failed to delete operator")
			return
		}

		p, _ := PrincipalFrom(r.Context())
		a.audit(r, "delete_operator", p.Email, "Operator "+o.Name+" deleted")
		writeResponse(r.Context(), w, SuccessResponse{Message: "Operator deleted successfully"}, http.StatusOK, cfg.Logger)
	})
}

func lookupOperator(w http.ResponseWriter, r *http.Request, cfg Config) (db.Operator, bool) {
	o, err := cfg.Operators.GetOperator(r.Context(), r.PathValue("name"))
	if errors.Is(err, db.ErrNotFound) {
		writeError(r.Context(), w, http.StatusNotFound, "Operator not found", nil, cfg.Logger)
		return db.Operator{}, false
	}

	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, "Failed to retrieve operator", err, cfg.Logger)
		return db.Operator{}, false
	}

	return o, true
}

func writeOperatorError(w http.ResponseWriter, r *http.Request, cfg Config, err error, message string) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(r.Context(), w, http.StatusNotFound, "Operator not found", nil, cfg.Logger)
	case errors.Is(err, db.ErrDuplicate):
		_, what, _ := strings.Cut(err.Error(), db.ErrDuplicate.Error()+": ")

		msg := "Another operator already has " + what
		if what == "PWS operator" {
			msg = "Another operator is already the PWS operator"
		} else if strings.HasPrefix(what, "MME ") || strings.HasPrefix(what, "AMF ") {
			msg = "The operator lists " + what + " twice"
		}

		writeError(r.Context(), w, http.StatusConflict, msg, err, cfg.Logger)
	case errors.Is(err, db.ErrForeignPLMN):
		writeError(r.Context(), w, http.StatusBadRequest, "The Global eNB and gNB IDs must use one of the operator's PLMNs",
			err, cfg.Logger)
	case errors.Is(err, db.ErrTooManyPLMNs):
		_, msg, _ := strings.Cut(err.Error(), db.ErrTooManyPLMNs.Error()+": ")
		writeError(r.Context(), w, http.StatusBadRequest, msg, err, cfg.Logger)
	case errors.Is(err, db.ErrConstraintCheck), errors.Is(err, db.ErrInvalid):
		writeError(r.Context(), w, http.StatusBadRequest, "Invalid operator", err, cfg.Logger)
	default:
		writeError(r.Context(), w, http.StatusInternalServerError, message, err, cfg.Logger)
	}
}

func operatorParams(w http.ResponseWriter, r *http.Request, cfg Config) (db.Operator, bool) {
	var params Operator
	if !decodeLimited(w, r, &params, cfg, maxOperatorBody) {
		return db.Operator{}, false
	}

	o, err := operatorOf(params)
	if err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, err.Error(), nil, cfg.Logger)
		return db.Operator{}, false
	}

	return o, true
}

func operatorOf(params Operator) (db.Operator, error) {
	o := db.Operator{Name: strings.TrimSpace(params.Name), PWSOperator: params.PWSOperator}

	if o.Name == "" {
		return db.Operator{}, errors.New("name is required")
	}

	if utf8.RuneCountInString(o.Name) > maxOperatorNameLength {
		return db.Operator{}, fmt.Errorf("name must be at most %d characters", maxOperatorNameLength)
	}

	if len(params.PLMNs) == 0 {
		return db.Operator{}, errors.New("at least one PLMN is required")
	}

	if len(params.PLMNs) > db.MaxOperatorPLMNs {
		return db.Operator{}, fmt.Errorf("an operator can have at most %d PLMNs", db.MaxOperatorPLMNs)
	}

	plmns := map[db.PLMN]bool{}

	for _, p := range params.PLMNs {
		plmn, err := plmnOf(p, "plmns")
		if err != nil {
			return db.Operator{}, err
		}

		if plmns[plmn] {
			return db.Operator{}, fmt.Errorf("PLMN %s is listed twice", plmn)
		}

		plmns[plmn] = true
		o.PLMNs = append(o.PLMNs, plmn)
	}

	if e := params.GlobalENBID; e != nil {
		enb, err := globalENBIDOf(*e, plmns)
		if err != nil {
			return db.Operator{}, err
		}

		o.GlobalENBID = &enb
	}

	if g := params.GlobalGNBID; g != nil {
		gnb, err := globalGNBIDOf(*g, plmns)
		if err != nil {
			return db.Operator{}, err
		}

		o.GlobalGNBID = &gnb
	}

	if len(params.MMEs) > 0 && o.GlobalENBID == nil {
		return db.Operator{}, errors.New("mmes require a global_enb_id")
	}

	if len(params.AMFs) > 0 && o.GlobalGNBID == nil {
		return db.Operator{}, errors.New("amfs require a global_gnb_id")
	}

	var err error

	mmes := make([]CoreNode, 0, len(params.MMEs))
	for _, m := range params.MMEs {
		mmes = append(mmes, CoreNode{TNLAddresses: m.TNLAddresses})
	}

	if o.MMEs, err = coreNodesOf(mmes, "mmes", "MME"); err != nil {
		return db.Operator{}, err
	}

	if o.AMFs, err = coreNodesOf(params.AMFs, "amfs", "AMF"); err != nil {
		return db.Operator{}, err
	}

	return o, nil
}

func plmnOf(p PLMN, field string) (db.PLMN, error) {
	if len(p.MCC) != 3 || !digits(p.MCC) {
		return db.PLMN{}, fmt.Errorf("%s: mcc must be 3 digits", field)
	}

	if (len(p.MNC) != 2 && len(p.MNC) != 3) || !digits(p.MNC) {
		return db.PLMN{}, fmt.Errorf("%s: mnc must be 2 or 3 digits", field)
	}

	return db.PLMN{MCC: p.MCC, MNC: p.MNC}, nil
}

func digits(s string) bool {
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}

	return true
}

func globalENBIDOf(e GlobalENBID, plmns map[db.PLMN]bool) (db.GlobalENBID, error) {
	plmn, err := plmnOf(e.PLMN, "global_enb_id.plmn")
	if err != nil {
		return db.GlobalENBID{}, err
	}

	if !plmns[plmn] {
		return db.GlobalENBID{}, errors.New("global_enb_id.plmn must be one of the operator's plmns")
	}

	bits, ok := enbIDBits[e.Type]
	if !ok {
		return db.GlobalENBID{}, errors.New("global_enb_id.enb_id_type must be macro, short_macro or long_macro")
	}

	if e.ID < 0 || e.ID >= 1<<bits {
		return db.GlobalENBID{}, fmt.Errorf("global_enb_id.enb_id must be between 0 and %d for a %s eNB ID", int64(1)<<bits-1,
			e.Type)
	}

	return db.GlobalENBID{PLMN: plmn, Type: e.Type, ID: uint32(e.ID)}, nil
}

func globalGNBIDOf(g GlobalGNBID, plmns map[db.PLMN]bool) (db.GlobalGNBID, error) {
	plmn, err := plmnOf(g.PLMN, "global_gnb_id.plmn")
	if err != nil {
		return db.GlobalGNBID{}, err
	}

	if !plmns[plmn] {
		return db.GlobalGNBID{}, errors.New("global_gnb_id.plmn must be one of the operator's plmns")
	}

	if g.Length < minGNBIDLength || g.Length > maxGNBIDLength {
		return db.GlobalGNBID{}, fmt.Errorf("global_gnb_id.gnb_id_length must be between %d and %d", minGNBIDLength,
			maxGNBIDLength)
	}

	if g.ID < 0 || g.ID >= int64(1)<<g.Length {
		return db.GlobalGNBID{}, fmt.Errorf("global_gnb_id.gnb_id must be between 0 and %d for a %d-bit gNB ID",
			int64(1)<<g.Length-1, g.Length)
	}

	return db.GlobalGNBID{PLMN: plmn, ID: uint32(g.ID), Length: g.Length}, nil
}

func coreNodesOf(in []CoreNode, field, what string) ([]db.CoreNode, error) {
	out := make([]db.CoreNode, 0, len(in))
	seen := map[netip.Addr]bool{}

	for _, n := range in {
		if len(n.TNLAddresses) == 0 {
			return nil, fmt.Errorf("%s: each %s needs at least one TNL address", field, what)
		}

		node := db.CoreNode{TNLAddresses: make([]string, 0, len(n.TNLAddresses))}

		for _, s := range n.TNLAddresses {
			addr, err := netip.ParseAddr(s)
			if err != nil || addr.Zone() != "" {
				return nil, fmt.Errorf("%s: tnl_addresses must be IPv4 or IPv6 addresses, not %q", field, s)
			}

			addr = addr.Unmap()
			if seen[addr] {
				return nil, fmt.Errorf("%s list %s twice", field, addr)
			}

			seen[addr] = true
			node.TNLAddresses = append(node.TNLAddresses, addr.String())
		}

		out = append(out, node)
	}

	return out, nil
}

func coreNodesResponse(in []db.CoreNode) []CoreNode {
	out := make([]CoreNode, 0, len(in))
	for _, n := range in {
		out = append(out, CoreNode{TNLAddresses: append([]string{}, n.TNLAddresses...)})
	}

	return out
}

func operatorResponse(o db.Operator) Operator {
	resp := Operator{
		Name: o.Name, PWSOperator: o.PWSOperator, PLMNs: make([]PLMN, 0, len(o.PLMNs)),
		MMEs: make([]MME, 0, len(o.MMEs)), AMFs: coreNodesResponse(o.AMFs),
	}

	for _, m := range o.MMEs {
		resp.MMEs = append(resp.MMEs, MME{TNLAddresses: append([]string{}, m.TNLAddresses...)})
	}

	for _, p := range o.PLMNs {
		resp.PLMNs = append(resp.PLMNs, PLMN(p))
	}

	if e := o.GlobalENBID; e != nil {
		resp.GlobalENBID = &GlobalENBID{PLMN: PLMN(e.PLMN), Type: e.Type, ID: int64(e.ID)}
	}

	if g := o.GlobalGNBID; g != nil {
		resp.GlobalGNBID = &GlobalGNBID{PLMN: PLMN(g.PLMN), ID: int64(g.ID), Length: g.Length}
	}

	return resp
}

func withStatus(cfg Config, o db.Operator, resp Operator, ues []s1.UE) Operator {
	if cfg.S1 == nil {
		return resp
	}

	total := 0

	for i, m := range o.MMEs {
		n := countUEConnections(ues, onMME(o.ID, m.TNLAddresses))
		total += n

		if st, ok := cfg.S1.MMEStatus(o.ID, m.TNLAddresses); ok {
			resp.MMEs[i].Status = mmeStatusResponse(st)
			resp.MMEs[i].Status.UEConnections = n
		}
	}

	resp.UEConnections = &total

	return resp
}

func plmnsResponse(in []db.PLMN) []PLMN {
	out := make([]PLMN, 0, len(in))
	for _, p := range in {
		out = append(out, PLMN(p))
	}

	return out
}

func mmeStatusResponse(st s1.MMEStatus) *MMEStatus {
	resp := &MMEStatus{
		State: st.State, Since: st.Since.UTC(), PrimaryTNLAddress: st.PrimaryTNLAddress, MMEName: st.MMEName,
		RelativeMMECapacity: st.RelativeMMECapacity,
	}

	for _, g := range st.ServedGUMMEIs {
		resp.ServedGUMMEIs = append(resp.ServedGUMMEIs, ServedGUMMEIs{
			ServedPLMNs:    plmnsResponse(g.PLMNs),
			ServedGroupIDs: append([]uint16{}, g.GroupIDs...),
			ServedMMECs:    mmecs(g.MMECodes),
		})
	}

	for _, o := range st.Overload {
		resp.Overload = append(resp.Overload, Overload{
			GUMMEI: GUMMEI{
				PLMN: PLMN(o.GUMMEI.PLMN), MMEGroupID: o.GUMMEI.MMEGroupID, MMECode: o.GUMMEI.MMECode,
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
