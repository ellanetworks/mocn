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
	ENBIDHome       ENBIDType = "home"
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

// TS 23.251 clause 4.2.1
type Operator struct {
	ID          int64
	Name        string
	PWSOperator bool
	PLMNs       []PLMN
	GlobalENBID *GlobalENBID
	GlobalGNBID *GlobalGNBID
	MMEs        []string
	AMFs        []string
}

func (d *DB) CreateOperator(ctx context.Context, o Operator) (id int64, err error) {
	defer d.observe(poolWrite, &err)()

	return run[int64](ctx, d, cmdCreateOperator, "create operator", o)
}

func applyCreateOperator(a *applyCtx, o Operator) (int64, error) {
	res, err := a.tx.ExecContext(a.ctx, `INSERT INTO operators (name, pws_operator) VALUES (?, ?)`, o.Name, o.PWSOperator)
	if err != nil {
		return 0, operatorRowError(err, o)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	return id, insertOperatorParts(a.ctx, a.tx, id, o)
}

func (d *DB) UpdateOperator(ctx context.Context, o Operator) (err error) {
	defer d.observe(poolWrite, &err)()

	_, err = run[struct{}](ctx, d, cmdUpdateOperator, "update operator", o)

	return err
}

func applyUpdateOperator(a *applyCtx, o Operator) (struct{}, error) {
	ctx, tx := a.ctx, a.tx

	res, err := tx.ExecContext(ctx, `UPDATE operators SET name = ?, pws_operator = ? WHERE id = ?`, o.Name, o.PWSOperator,
		o.ID)
	if err != nil {
		return struct{}{}, operatorRowError(err, o)
	}

	if err := checkAffected(res); err != nil {
		return struct{}{}, err
	}

	for _, q := range []string{
		`DELETE FROM global_enb_ids WHERE operator_id = ?`,
		`DELETE FROM global_gnb_ids WHERE operator_id = ?`,
		`DELETE FROM mmes WHERE operator_id = ?`,
		`DELETE FROM amfs WHERE operator_id = ?`,
		`DELETE FROM operator_plmns WHERE operator_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, o.ID); err != nil {
			return struct{}{}, err
		}
	}

	return struct{}{}, insertOperatorParts(ctx, tx, o.ID, o)
}

func insertOperatorParts(ctx context.Context, tx *sql.Tx, id int64, o Operator) error {
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

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO global_enb_ids (operator_id, operator_plmn_id, enb_id_type, enb_id) VALUES (?, ?, ?, ?)`,
			id, plmnID, e.Type, e.ID); err != nil {
			return err
		}
	}

	if g := o.GlobalGNBID; g != nil {
		plmnID, ok := plmnIDs[g.PLMN]
		if !ok {
			return fmt.Errorf("%w: global gNB ID PLMN %s", ErrForeignPLMN, g.PLMN)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO global_gnb_ids (operator_id, operator_plmn_id, gnb_id, gnb_id_length) VALUES (?, ?, ?, ?)`,
			id, plmnID, g.ID, g.Length); err != nil {
			return err
		}
	}

	for _, n := range []struct {
		query, what string
		addresses   []string
	}{
		{`INSERT INTO mmes (operator_id, address) VALUES (?, ?)`, "MME", o.MMEs},
		{`INSERT INTO amfs (operator_id, address) VALUES (?, ?)`, "AMF", o.AMFs},
	} {
		for _, addr := range n.addresses {
			if _, err := tx.ExecContext(ctx, n.query, id, addr); err != nil {
				return operatorError(err, n.what+" "+addr)
			}
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
	defer d.observe(poolWrite, &err)()

	_, err = run[struct{}](ctx, d, cmdDeleteOperator, "delete operator", id)

	return err
}

func applyDeleteOperator(a *applyCtx, id int64) (struct{}, error) {
	ctx, tx := a.ctx, a.tx

	for _, q := range []string{
		`DELETE FROM global_enb_ids WHERE operator_id = ?`,
		`DELETE FROM global_gnb_ids WHERE operator_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return struct{}{}, err
		}
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM operators WHERE id = ?`, id)
	if err != nil {
		return struct{}{}, err
	}

	return struct{}{}, checkAffected(res)
}

func (d *DB) GetOperator(ctx context.Context, name string) (_ Operator, err error) {
	defer d.observe(poolRead, &err)()

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
	defer d.observe(poolRead, &err)()

	ops, err := d.readOperators(ctx, `SELECT id, name, pws_operator FROM operators ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list operators: %w", err)
	}

	return ops, nil
}

func (d *DB) readOperators(ctx context.Context, query string, args ...any) ([]Operator, error) {
	tx, err := d.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}

	defer func() { _ = tx.Rollback() }()

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

	for _, n := range []struct {
		query string
		add   func(*Operator, string)
	}{
		{`SELECT operator_id, address FROM mmes ORDER BY id`, func(o *Operator, a string) { o.MMEs = append(o.MMEs, a) }},
		{`SELECT operator_id, address FROM amfs ORDER BY id`, func(o *Operator, a string) { o.AMFs = append(o.AMFs, a) }},
	} {
		if err := each(ctx, q, n.query, func(s scanner) error {
			var (
				id   int64
				addr string
			)

			if err := s.Scan(&id, &addr); err != nil {
				return err
			}

			if o := byID[id]; o != nil {
				n.add(o, addr)
			}

			return nil
		}); err != nil {
			return err
		}
	}

	return nil
}

func each(ctx context.Context, q querier, query string, fn func(scanner) error, args ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}

	return rows.Err()
}

func init() {
	register(cmdCreateOperator, handle(applyCreateOperator))
	register(cmdUpdateOperator, handle(applyUpdateOperator))
	register(cmdDeleteOperator, handle(applyDeleteOperator))
}
