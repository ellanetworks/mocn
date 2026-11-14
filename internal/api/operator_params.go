package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/ellanetworks/mocn/internal/db"
)

const (
	maxOperatorNameLength = 64
	maxOperatorBody       = 64 << 10
	minGNBIDBitLength     = 22
	maxGNBIDBitLength     = 32
)

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
		enb, err := operatorGlobalENBIDOf(*e, plmns)
		if err != nil {
			return db.Operator{}, err
		}

		o.GlobalENBID = &enb
	}

	if g := params.GlobalGNBID; g != nil {
		gnb, err := operatorGlobalGNBIDOf(*g, plmns)
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

	mmes := make([][]string, 0, len(params.MMEs))
	for _, m := range params.MMEs {
		mmes = append(mmes, m.TNLAddresses)
	}

	if o.MMEs, err = coreNodesOf(mmes, "mmes", "MME"); err != nil {
		return db.Operator{}, err
	}

	amfs := make([][]string, 0, len(params.AMFs))
	for _, m := range params.AMFs {
		amfs = append(amfs, m.TNLAddresses)
	}

	if o.AMFs, err = coreNodesOf(amfs, "amfs", "AMF"); err != nil {
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

func operatorGlobalENBIDOf(e GlobalENBID, plmns map[db.PLMN]bool) (db.GlobalENBID, error) {
	plmn, err := plmnOf(e.PLMN, "global_enb_id.plmn")
	if err != nil {
		return db.GlobalENBID{}, err
	}

	if !plmns[plmn] {
		return db.GlobalENBID{}, errors.New("global_enb_id.plmn must be one of the operator's plmns")
	}

	bits, ok := db.ENBIDBits[e.Type]
	if !ok || e.Type == db.ENBIDHome {
		return db.GlobalENBID{}, errors.New("global_enb_id.enb_id_type must be macro, short_macro or long_macro")
	}

	if e.ID < 0 || e.ID >= 1<<bits {
		return db.GlobalENBID{}, fmt.Errorf("global_enb_id.enb_id must be between 0 and %d for a %s eNB ID", int64(1)<<bits-1,
			e.Type)
	}

	return db.GlobalENBID{PLMN: plmn, Type: e.Type, ID: uint32(e.ID)}, nil
}

func operatorGlobalGNBIDOf(g GlobalGNBID, plmns map[db.PLMN]bool) (db.GlobalGNBID, error) {
	plmn, err := plmnOf(g.PLMN, "global_gnb_id.plmn")
	if err != nil {
		return db.GlobalGNBID{}, err
	}

	if !plmns[plmn] {
		return db.GlobalGNBID{}, errors.New("global_gnb_id.plmn must be one of the operator's plmns")
	}

	if g.BitLength < minGNBIDBitLength || g.BitLength > maxGNBIDBitLength {
		return db.GlobalGNBID{}, fmt.Errorf("global_gnb_id.bit_length must be between %d and %d", minGNBIDBitLength,
			maxGNBIDBitLength)
	}

	if g.ID < 0 || g.ID >= int64(1)<<g.BitLength {
		return db.GlobalGNBID{}, fmt.Errorf("global_gnb_id.gnb_id must be between 0 and %d for a %d-bit gNB ID",
			int64(1)<<g.BitLength-1, g.BitLength)
	}

	return db.GlobalGNBID{PLMN: plmn, ID: uint32(g.ID), Length: g.BitLength}, nil
}

func coreNodesOf(in [][]string, field, what string) ([]db.CoreNode, error) {
	out := make([]db.CoreNode, 0, len(in))
	seen := map[netip.Addr]bool{}

	for _, addrs := range in {
		if len(addrs) == 0 {
			return nil, fmt.Errorf("%s: each %s needs at least one TNL address", field, what)
		}

		node := db.CoreNode{TNLAddresses: make([]string, 0, len(addrs))}

		for _, s := range addrs {
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
