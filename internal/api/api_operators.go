package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gtpu"
)

type operatorStore interface {
	CreateOperator(ctx context.Context, o db.Operator) (int64, error)
	UpdateOperator(ctx context.Context, o db.Operator) error
	DeleteOperator(ctx context.Context, id int64) error
	GetOperator(ctx context.Context, name string) (db.Operator, error)
	ListOperators(ctx context.Context) ([]db.Operator, error)
}

type Operator struct {
	Name                 string               `json:"name"`
	PWSOperator          bool                 `json:"pws_operator"`
	PLMNs                []PLMN               `json:"plmns"`
	GlobalENBID          *GlobalENBID         `json:"global_enb_id,omitempty"`
	GlobalGNBID          *GlobalGNBID         `json:"global_gnb_id,omitempty"`
	MMEs                 []MME                `json:"mmes"`
	AMFs                 []AMF                `json:"amfs"`
	UserPlane            *UserPlane           `json:"user_plane,omitempty"`
	UEConnections        *int                 `json:"ue_connections,omitempty"`
	UEConnectionsByRadio []RadioUEConnections `json:"ue_connections_by_radio,omitzero"`
}

type UserPlane struct {
	Aggregation *bool            `json:"aggregation,omitempty"`
	Status      *UserPlaneStatus `json:"status,omitempty"`
}

type UserPlaneStatus struct {
	State     gtpu.State  `json:"state"`
	Reason    gtpu.Reason `json:"reason,omitempty"`
	Interface string      `json:"interface,omitempty"`
	Addresses []string    `json:"addresses"`
	Since     time.Time   `json:"since"`
}

type MME struct {
	TNLAddresses []string   `json:"tnl_addresses"`
	Status       *MMEStatus `json:"status,omitempty"`
}

type AMF struct {
	TNLAddresses []string   `json:"tnl_addresses"`
	Status       *AMFStatus `json:"status,omitempty"`
}

type ListOperatorsResponse struct {
	Items []Operator `json:"items"`
}

func listOperators(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ops, err := cfg.Operators.ListOperators(r.Context())
		if err != nil {
			writeError(r.Context(), w, http.StatusInternalServerError, "Failed to list operators", err, cfg.Logger)
			return
		}

		ues := ueConnections(cfg)

		resp := ListOperatorsResponse{Items: make([]Operator, 0, len(ops))}
		for _, o := range ops {
			resp.Items = append(resp.Items, withStatus(cfg, o, operatorResponse(cfg, o), ues))
		}

		writeResponse(r.Context(), w, resp, http.StatusOK, cfg.Logger)
	})
}

func getOperator(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o, ok := lookupOperator(w, r, cfg)
		if !ok {
			return
		}

		writeResponse(r.Context(), w, operatorDetail(cfg, o), http.StatusOK, cfg.Logger)
	})
}

func createOperator(a *auth) http.Handler {
	cfg := a.cfg

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o, ok := operatorParams(w, r, cfg)
		if !ok {
			return
		}

		id, err := cfg.Operators.CreateOperator(r.Context(), o)
		if err != nil {
			writeOperatorError(w, r, cfg, err, "Failed to create operator")
			return
		}

		o.ID = id

		p, _ := principalFrom(r.Context())
		a.audit(r, "create_operator", p.Email, "Operator "+o.Name+" created")
		writeResponse(r.Context(), w, operatorDetail(cfg, o), http.StatusCreated, cfg.Logger)
	})
}

func updateOperator(a *auth) http.Handler {
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

		p, _ := principalFrom(r.Context())
		a.audit(r, "update_operator", p.Email, details)
		writeResponse(r.Context(), w, operatorDetail(cfg, o), http.StatusOK, cfg.Logger)
	})
}

func deleteOperator(a *auth) http.Handler {
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

		p, _ := principalFrom(r.Context())
		a.audit(r, "delete_operator", p.Email, "Operator "+o.Name+" deleted")
		writeResponse(r.Context(), w, successResponse{Message: "Operator deleted successfully"}, http.StatusOK, cfg.Logger)
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

func operatorResponse(cfg Config, o db.Operator) Operator {
	aggregation := o.UserPlane.Aggregation && cfg.UserPlane
	resp := Operator{
		Name: o.Name, PWSOperator: o.PWSOperator, PLMNs: plmnsOfDB(o.PLMNs),
		MMEs: make([]MME, 0, len(o.MMEs)), AMFs: make([]AMF, 0, len(o.AMFs)),
		UserPlane: &UserPlane{Aggregation: &aggregation},
	}

	for _, m := range o.MMEs {
		resp.MMEs = append(resp.MMEs, MME{TNLAddresses: append([]string{}, m.TNLAddresses...)})
	}

	for _, m := range o.AMFs {
		resp.AMFs = append(resp.AMFs, AMF{TNLAddresses: append([]string{}, m.TNLAddresses...)})
	}

	if e := o.GlobalENBID; e != nil {
		resp.GlobalENBID = globalENBIDOfDB(*e)
	}

	if g := o.GlobalGNBID; g != nil {
		resp.GlobalGNBID = globalGNBIDOfDB(*g)
	}

	return resp
}

func operatorDetail(cfg Config, o db.Operator) Operator {
	ues := ueConnections(cfg)
	resp := withStatus(cfg, o, operatorResponse(cfg, o), ues)

	if cfg.S1 != nil || cfg.NG != nil {
		resp.UEConnectionsByRadio = ueConnectionsByRadio(o, radios(cfg, ues), ues)
	}

	return resp
}
