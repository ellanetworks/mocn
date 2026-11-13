package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mattn/go-sqlite3"
)

// TS 23.003 clause 12.1
type PLMN struct {
	MCC string
	MNC string
}

func (p PLMN) String() string {
	return p.MCC + p.MNC
}

type ENBIDType string

const (
	ENBIDMacro      ENBIDType = "macro"
	ENBIDShortMacro ENBIDType = "short_macro"
	ENBIDLongMacro  ENBIDType = "long_macro"
)

// TS 36.413 clause 9.2.1.37
type GlobalENBID struct {
	PLMN PLMN
	Type ENBIDType
	ID   uint32
}

// TS 38.413 clause 9.3.1.6
type GlobalGNBID struct {
	PLMN   PLMN
	ID     uint32
	Length int
}

// TS 36.412 clause 7
// TS 38.412 clause 7
type CoreNode struct {
	TNLAddresses []string
}

// TS 38.413 clause 9.2.6.1
const MaxOperatorPLMNs = 12

// TS 38.413 clause 9.2.6.2
const MaxAMFPLMNs = 12

// TS 36.413 clause 9.1.8.5
const MaxMMEPLMNs = 32

// TS 23.501 clause 5.18.1
type Operator struct {
	ID          int64
	Name        string
	PWSOperator bool
	PLMNs       []PLMN
	GlobalENBID *GlobalENBID
	GlobalGNBID *GlobalGNBID
	MMEs        []CoreNode
	AMFs        []CoreNode
}

func (d *DB) CreateOperator(ctx context.Context, o Operator) (id int64, err error) {
	ctx, done := d.observe(ctx, "CreateOperator", poolWrite)
	defer done(&err)

	return write(ctx, d, "create operator", func(tx querier) (int64, error) { return createOperator(ctx, tx, o) })
}

func createOperator(ctx context.Context, tx querier, o Operator) (int64, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO operators (name, pws_operator) VALUES (?, ?)`, o.Name, o.PWSOperator)
	if err != nil {
		return 0, operatorRowError(err, o)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	return id, insertOperatorParts(ctx, tx, id, o)
}

func (d *DB) UpdateOperator(ctx context.Context, o Operator) (err error) {
	ctx, done := d.observe(ctx, "UpdateOperator", poolWrite)
	defer done(&err)

	return d.exec(ctx, "update operator", func(tx querier) error { return updateOperator(ctx, tx, o) })
}

func updateOperator(ctx context.Context, tx querier, o Operator) error {
	res, err := tx.ExecContext(ctx, `UPDATE operators SET name = ?, pws_operator = ? WHERE id = ?`, o.Name, o.PWSOperator,
		o.ID)
	if err != nil {
		return operatorRowError(err, o)
	}

	if err := checkAffected(res); err != nil {
		return err
	}

	for _, q := range []string{
		`DELETE FROM global_enb_ids WHERE operator_id = ?`,
		`DELETE FROM global_gnb_ids WHERE operator_id = ?`,
		`DELETE FROM mmes WHERE operator_id = ?`,
		`DELETE FROM amfs WHERE operator_id = ?`,
		`DELETE FROM operator_plmns WHERE operator_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, o.ID); err != nil {
			return err
		}
	}

	return insertOperatorParts(ctx, tx, o.ID, o)
}

func insertOperatorParts(ctx context.Context, tx querier, id int64, o Operator) error {
	plmnIDs := make(map[PLMN]int64, len(o.PLMNs))

	for _, p := range o.PLMNs {
		res, err := tx.ExecContext(ctx, `INSERT INTO operator_plmns (operator_id, mcc, mnc) VALUES (?, ?, ?)`, id, p.MCC,
			p.MNC)
		if err != nil {
			return operatorError(err, "PLMN "+p.String())
		}

		if plmnIDs[p], err = res.LastInsertId(); err != nil {
			return err
		}
	}

	if e := o.GlobalENBID; e != nil {
		plmnID, ok := plmnIDs[e.PLMN]
		if !ok {
			return fmt.Errorf("%w: global eNB ID PLMN %s", ErrForeignPLMN, e.PLMN)
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO global_enb_ids (operator_id, operator_plmn_id, enb_id_type, enb_id) VALUES (?, ?, ?, ?)`,
			id, plmnID, e.Type, e.ID); err != nil {
			return err
		}
	}

	if g := o.GlobalGNBID; g != nil {
		plmnID, ok := plmnIDs[g.PLMN]
		if !ok {
			return fmt.Errorf("%w: global gNB ID PLMN %s", ErrForeignPLMN, g.PLMN)
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO global_gnb_ids (operator_id, operator_plmn_id, gnb_id, gnb_id_length) VALUES (?, ?, ?, ?)`,
			id, plmnID, g.ID, g.Length); err != nil {
			return err
		}
	}

	for _, n := range []coreNodeInserts{
		{
			`INSERT INTO mmes (operator_id) VALUES (?)`,
			`INSERT INTO mme_tnl_addresses (mme_id, operator_id, address) VALUES (?, ?, ?)`, "MME", o.MMEs,
		},
		{
			`INSERT INTO amfs (operator_id) VALUES (?)`,
			`INSERT INTO amf_tnl_addresses (amf_id, operator_id, address) VALUES (?, ?, ?)`, "AMF", o.AMFs,
		},
	} {
		if err := insertCoreNodes(ctx, tx, id, n); err != nil {
			return err
		}
	}

	return checkPLMNLimits(ctx, tx, id)
}

type coreNodeInserts struct {
	nodeQuery, addressQuery, what string
	nodes                         []CoreNode
}

func insertCoreNodes(ctx context.Context, tx querier, operatorID int64, ins coreNodeInserts) error {
	for _, n := range ins.nodes {
		if len(n.TNLAddresses) == 0 {
			return fmt.Errorf("%w: %s without a TNL address", ErrInvalid, ins.what)
		}

		res, err := tx.ExecContext(ctx, ins.nodeQuery, operatorID)
		if err != nil {
			return err
		}

		nodeID, err := res.LastInsertId()
		if err != nil {
			return err
		}

		for _, addr := range n.TNLAddresses {
			if _, err := tx.ExecContext(ctx, ins.addressQuery, nodeID, operatorID, addr); err != nil {
				return operatorError(err, ins.what+" "+addr)
			}
		}
	}

	return nil
}

func checkPLMNLimits(ctx context.Context, tx querier, operatorID int64) error {
	for _, l := range []struct {
		query, message string
		args           []any
		limit          int
	}{
		{
			`SELECT count(*) FROM operator_plmns WHERE operator_id = ?`, "An operator can have at most %d PLMNs",
			[]any{operatorID},
			MaxOperatorPLMNs,
		},
		{
			`SELECT count(*) FROM operator_plmns WHERE operator_id IN (SELECT operator_id FROM amfs)`,
			"Operators with AMFs can have at most %d PLMNs in total", nil, MaxAMFPLMNs,
		},
		{
			`SELECT count(*) FROM operator_plmns WHERE operator_id IN (SELECT operator_id FROM mmes)`,
			"Operators with MMEs can have at most %d PLMNs in total", nil, MaxMMEPLMNs,
		},
	} {
		var n int
		if err := tx.QueryRowContext(ctx, l.query, l.args...).Scan(&n); err != nil {
			return err
		}

		if n > l.limit {
			return fmt.Errorf("%w: "+l.message, ErrTooManyPLMNs, l.limit)
		}
	}

	return nil
}

func operatorRowError(err error, o Operator) error {
	if isConstraint(err, sqlite3.ErrConstraintUnique) && strings.Contains(err.Error(), "pws_operator") {
		return fmt.Errorf("%w: PWS operator", ErrDuplicate)
	}

	return operatorError(err, "name "+o.Name)
}

func operatorError(err error, what string) error {
	if !isConstraint(err, sqlite3.ErrConstraintUnique) {
		return err
	}

	return fmt.Errorf("%w: %s", ErrDuplicate, what)
}

func (d *DB) DeleteOperator(ctx context.Context, id int64) (err error) {
	ctx, done := d.observe(ctx, "DeleteOperator", poolWrite)
	defer done(&err)

	return d.exec(ctx, "delete operator", func(tx querier) error { return deleteOperator(ctx, tx, id) })
}

func deleteOperator(ctx context.Context, tx querier, id int64) error {
	for _, q := range []string{
		`DELETE FROM global_enb_ids WHERE operator_id = ?`,
		`DELETE FROM global_gnb_ids WHERE operator_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM operators WHERE id = ?`, id)
	if err != nil {
		return err
	}

	return checkAffected(res)
}

func (d *DB) GetOperator(ctx context.Context, name string) (_ Operator, err error) {
	ctx, done := d.observe(ctx, "GetOperator", poolRead)
	defer done(&err)

	ops, err := d.readOperators(ctx, `SELECT id, name, pws_operator FROM operators WHERE name = ?`, name)
	if err != nil {
		return Operator{}, fmt.Errorf("get operator: %w", err)
	}

	if len(ops) == 0 {
		return Operator{}, ErrNotFound
	}

	return ops[0], nil
}

func (d *DB) ListOperators(ctx context.Context) (_ []Operator, err error) {
	ctx, done := d.observe(ctx, "ListOperators", poolRead)
	defer done(&err)

	ops, err := d.readOperators(ctx, `SELECT id, name, pws_operator FROM operators ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list operators: %w", err)
	}

	return ops, nil
}

func (d *DB) readOperators(ctx context.Context, query string, args ...any) ([]Operator, error) {
	sqlTx, err := d.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}

	defer func() { _ = sqlTx.Rollback() }()

	tx := sqlTx

	ops := []Operator{}

	if err := each(ctx, tx, query, func(s scanner) error {
		var o Operator
		if err := s.Scan(&o.ID, &o.Name, &o.PWSOperator); err != nil {
			return err
		}

		ops = append(ops, o)

		return nil
	}, args...); err != nil {
		return nil, err
	}

	if err := loadOperatorParts(ctx, tx, ops); err != nil {
		return nil, err
	}

	return ops, nil
}

func loadOperatorParts(ctx context.Context, q querier, ops []Operator) error {
	byID := make(map[int64]*Operator, len(ops))
	for i := range ops {
		byID[ops[i].ID] = &ops[i]
	}

	if err := each(ctx, q, `SELECT operator_id, mcc, mnc FROM operator_plmns ORDER BY id`, func(s scanner) error {
		var (
			id int64
			p  PLMN
		)

		if err := s.Scan(&id, &p.MCC, &p.MNC); err != nil {
			return err
		}

		if o := byID[id]; o != nil {
			o.PLMNs = append(o.PLMNs, p)
		}

		return nil
	}); err != nil {
		return err
	}

	if err := each(ctx, q, `SELECT e.operator_id, p.mcc, p.mnc, e.enb_id_type, e.enb_id FROM global_enb_ids e
		JOIN operator_plmns p ON p.id = e.operator_plmn_id`, func(s scanner) error {
		var (
			id int64
			e  GlobalENBID
		)

		if err := s.Scan(&id, &e.PLMN.MCC, &e.PLMN.MNC, &e.Type, &e.ID); err != nil {
			return err
		}

		if o := byID[id]; o != nil {
			o.GlobalENBID = &e
		}

		return nil
	}); err != nil {
		return err
	}

	if err := each(ctx, q, `SELECT g.operator_id, p.mcc, p.mnc, g.gnb_id, g.gnb_id_length FROM global_gnb_ids g
		JOIN operator_plmns p ON p.id = g.operator_plmn_id`, func(s scanner) error {
		var (
			id int64
			g  GlobalGNBID
		)

		if err := s.Scan(&id, &g.PLMN.MCC, &g.PLMN.MNC, &g.ID, &g.Length); err != nil {
			return err
		}

		if o := byID[id]; o != nil {
			o.GlobalGNBID = &g
		}

		return nil
	}); err != nil {
		return err
	}

	return loadCoreNodes(ctx, q, byID)
}

func loadCoreNodes(ctx context.Context, q querier, byID map[int64]*Operator) error {
	for _, n := range []struct {
		query string
		nodes func(*Operator) *[]CoreNode
	}{
		{
			`SELECT m.operator_id, m.id, a.address FROM mmes m JOIN mme_tnl_addresses a ON a.mme_id = m.id ORDER BY m.id, a.id`,
			func(o *Operator) *[]CoreNode { return &o.MMEs },
		},
		{
			`SELECT f.operator_id, f.id, a.address FROM amfs f JOIN amf_tnl_addresses a ON a.amf_id = f.id ORDER BY f.id, a.id`,
			func(o *Operator) *[]CoreNode { return &o.AMFs },
		},
	} {
		last := int64(-1)

		if err := each(ctx, q, n.query, func(s scanner) error {
			var (
				id, nodeID int64
				addr       string
			)

			if err := s.Scan(&id, &nodeID, &addr); err != nil {
				return err
			}

			o := byID[id]
			if o == nil {
				return nil
			}

			nodes := n.nodes(o)
			if nodeID != last {
				*nodes = append(*nodes, CoreNode{})
				last = nodeID
			}

			node := &(*nodes)[len(*nodes)-1]
			node.TNLAddresses = append(node.TNLAddresses, addr)

			return nil
		}); err != nil {
			return err
		}
	}

	return nil
}
