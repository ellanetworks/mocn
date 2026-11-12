package db

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func operatorA() Operator {
	return Operator{
		Name:        "Operator A",
		PWSOperator: true,
		PLMNs:       []PLMN{{MCC: "001", MNC: "01"}, {MCC: "001", MNC: "001"}},
		GlobalENBID: &GlobalENBID{PLMN: PLMN{MCC: "001", MNC: "01"}, Type: ENBIDMacro, ID: 0xABCDE},
		GlobalGNBID: &GlobalGNBID{PLMN: PLMN{MCC: "001", MNC: "001"}, ID: 0x12345, Length: 22},
		MMEs:        []CoreNode{{TNLAddresses: []string{"10.0.0.1", "10.0.0.2"}}, {TNLAddresses: []string{"10.0.0.3"}}},
		AMFs:        []CoreNode{{TNLAddresses: []string{"10.0.1.1"}}},
	}
}

func operatorB() Operator {
	return Operator{
		Name:  "Operator B",
		PLMNs: []PLMN{{MCC: "999", MNC: "70"}},
		MMEs:  []CoreNode{{TNLAddresses: []string{"10.0.2.1"}}},
	}
}

func createTestOperator(t *testing.T, d *DB, o Operator) Operator {
	t.Helper()

	id, err := d.CreateOperator(context.Background(), o)
	if err != nil {
		t.Fatalf("CreateOperator %s: %v", o.Name, err)
	}

	o.ID = id

	return o
}

func TestOperatorLifecycle(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	if ops, err := d.ListOperators(ctx); err != nil || len(ops) != 0 {
		t.Fatalf("ListOperators = %v, %v, want none", ops, err)
	}

	a := createTestOperator(t, d, operatorA())
	b := createTestOperator(t, d, operatorB())

	got, err := d.GetOperator(ctx, a.Name)
	if err != nil || !reflect.DeepEqual(got, a) {
		t.Fatalf("GetOperator = %+v, %v, want %+v", got, err, a)
	}

	ops, err := d.ListOperators(ctx)
	if err != nil || !reflect.DeepEqual(ops, []Operator{a, b}) {
		t.Fatalf("ListOperators = %+v, %v, want %+v", ops, err, []Operator{a, b})
	}

	a.Name = "Operator A2"
	a.PWSOperator = false
	a.PLMNs = []PLMN{{MCC: "001", MNC: "02"}}
	a.GlobalENBID = &GlobalENBID{PLMN: PLMN{MCC: "001", MNC: "02"}, Type: ENBIDShortMacro, ID: 1<<18 - 1}
	a.GlobalGNBID = nil
	a.MMEs = []CoreNode{{TNLAddresses: []string{"10.0.0.2"}}}
	a.AMFs = nil

	if err := d.UpdateOperator(ctx, a); err != nil {
		t.Fatalf("UpdateOperator: %v", err)
	}

	if got, err := d.GetOperator(ctx, "Operator A2"); err != nil || !reflect.DeepEqual(got, a) {
		t.Fatalf("after update: %+v, %v, want %+v", got, err, a)
	}

	b.PWSOperator = true
	if err := d.UpdateOperator(ctx, b); err != nil {
		t.Fatalf("UpdateOperator PWS: %v", err)
	}

	if err := d.DeleteOperator(ctx, a.ID); err != nil {
		t.Fatalf("DeleteOperator: %v", err)
	}

	if _, err := d.GetOperator(ctx, a.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetOperator after delete: %v, want ErrNotFound", err)
	}

	again := operatorA()
	again.PWSOperator = false

	if _, err := d.CreateOperator(ctx, again); err != nil {
		t.Fatalf("PLMNs, IDs and addresses are free after delete: %v", err)
	}
}

func TestOperatorNotFound(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	if _, err := d.GetOperator(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetOperator: %v, want ErrNotFound", err)
	}

	if err := d.UpdateOperator(ctx, Operator{ID: 42, Name: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateOperator: %v, want ErrNotFound", err)
	}

	if err := d.DeleteOperator(ctx, 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteOperator: %v, want ErrNotFound", err)
	}
}

func TestOperatorDuplicates(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	createTestOperator(t, d, operatorA())

	tests := map[string]struct {
		edit func(*Operator)
		want string
	}{
		"name":         {edit: func(o *Operator) { o.Name = "Operator A" }, want: "name Operator A"},
		"PWS operator": {edit: func(o *Operator) { o.PWSOperator = true }, want: "PWS operator"},
		"PLMN":         {edit: func(o *Operator) { o.PLMNs = append(o.PLMNs, PLMN{MCC: "001", MNC: "01"}) }, want: "PLMN 00101"},
		"PLMN in list": {edit: func(o *Operator) { o.PLMNs = append(o.PLMNs, o.PLMNs[0]) }, want: "PLMN 99970"},
		"MME address in another MME": {edit: func(o *Operator) {
			o.MMEs = append(o.MMEs, CoreNode{TNLAddresses: []string{"10.0.3.1", "10.0.2.1"}})
		}, want: "MME 10.0.2.1"},
		"MME address in the same MME": {edit: func(o *Operator) {
			o.MMEs = []CoreNode{{TNLAddresses: []string{"10.0.2.1", "10.0.2.1"}}}
		}, want: "MME 10.0.2.1"},
		"AMF address": {edit: func(o *Operator) {
			o.GlobalGNBID = &GlobalGNBID{PLMN: o.PLMNs[0], ID: 1, Length: 22}
			o.AMFs = []CoreNode{{TNLAddresses: []string{"10.0.1.1"}}, {TNLAddresses: []string{"10.0.1.1"}}}
		}, want: "AMF 10.0.1.1"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			o := operatorB()
			tc.edit(&o)

			_, err := d.CreateOperator(ctx, o)
			if !errors.Is(err, ErrDuplicate) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CreateOperator: %v, want ErrDuplicate %q", err, tc.want)
			}

			if _, err := d.GetOperator(ctx, "Operator B"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("the failed create left Operator B behind: %v", err)
			}
		})
	}
}

func TestOperatorInvalid(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	tests := map[string]struct {
		edit func(*Operator)
		want error
	}{
		"empty name":           {edit: func(o *Operator) { o.Name = "" }, want: ErrConstraintCheck},
		"short MCC":            {edit: func(o *Operator) { o.PLMNs[0].MCC = "01" }, want: ErrConstraintCheck},
		"long MNC":             {edit: func(o *Operator) { o.PLMNs[0].MNC = "0011" }, want: ErrConstraintCheck},
		"letters in MCC":       {edit: func(o *Operator) { o.PLMNs[0].MCC = "0a1" }, want: ErrConstraintCheck},
		"empty MME address":    {edit: func(o *Operator) { o.MMEs[0].TNLAddresses[0] = "" }, want: ErrConstraintCheck},
		"MME without address":  {edit: func(o *Operator) { o.MMEs[1].TNLAddresses = nil }, want: ErrInvalid},
		"AMF without address":  {edit: func(o *Operator) { o.AMFs = []CoreNode{{}} }, want: ErrInvalid},
		"home eNB ID":          {edit: func(o *Operator) { o.GlobalENBID.Type = "home" }, want: ErrConstraintCheck},
		"13 PLMNs":             {edit: func(o *Operator) { o.PLMNs = append(o.PLMNs, plmns("002", 11)...) }, want: ErrTooManyPLMNs},
		"macro eNB ID too big": {edit: func(o *Operator) { o.GlobalENBID.ID = 1 << 20 }, want: ErrConstraintCheck},
		"short macro too big": {edit: func(o *Operator) {
			o.GlobalENBID.Type, o.GlobalENBID.ID = ENBIDShortMacro, 1<<18
		}, want: ErrConstraintCheck},
		"long macro too big": {edit: func(o *Operator) {
			o.GlobalENBID.Type, o.GlobalENBID.ID = ENBIDLongMacro, 1<<21
		}, want: ErrConstraintCheck},
		"unknown eNB ID type":  {edit: func(o *Operator) { o.GlobalENBID.Type = "pico" }, want: ErrConstraintCheck},
		"gNB ID too short":     {edit: func(o *Operator) { o.GlobalGNBID.Length = 21 }, want: ErrConstraintCheck},
		"gNB ID too long":      {edit: func(o *Operator) { o.GlobalGNBID.Length = 33 }, want: ErrConstraintCheck},
		"gNB ID over length":   {edit: func(o *Operator) { o.GlobalGNBID.ID = 1 << 22 }, want: ErrConstraintCheck},
		"foreign eNB ID PLMN":  {edit: func(o *Operator) { o.GlobalENBID.PLMN = PLMN{MCC: "999", MNC: "99"} }, want: ErrForeignPLMN},
		"foreign gNB ID PLMN":  {edit: func(o *Operator) { o.GlobalGNBID.PLMN = PLMN{MCC: "999", MNC: "99"} }, want: ErrForeignPLMN},
		"eNB ID without PLMNs": {edit: func(o *Operator) { o.PLMNs, o.GlobalGNBID = nil, nil }, want: ErrForeignPLMN},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			o := operatorA()
			tc.edit(&o)

			if _, err := d.CreateOperator(ctx, o); !errors.Is(err, tc.want) {
				t.Fatalf("CreateOperator: %v, want %v", err, tc.want)
			}

			if ops, err := d.ListOperators(ctx); err != nil || len(ops) != 0 {
				t.Fatalf("the failed create left %v, %v", ops, err)
			}
		})
	}
}

func TestOperatorLargestIDs(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	o := operatorA()
	o.GlobalENBID.Type, o.GlobalENBID.ID = ENBIDLongMacro, 1<<21-1
	o.GlobalGNBID.ID, o.GlobalGNBID.Length = 1<<32-1, 32
	o = createTestOperator(t, d, o)

	if got, err := d.GetOperator(ctx, o.Name); err != nil || !reflect.DeepEqual(got, o) {
		t.Fatalf("GetOperator = %+v, %v, want %+v", got, err, o)
	}
}

func TestOperatorUpdateRollsBack(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	a := createTestOperator(t, d, operatorA())
	createTestOperator(t, d, operatorB())

	broken := a
	broken.MMEs = []CoreNode{{TNLAddresses: []string{"10.0.2.1"}}, {TNLAddresses: []string{"10.0.2.1"}}}

	if err := d.UpdateOperator(ctx, broken); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("UpdateOperator: %v, want ErrDuplicate", err)
	}

	if got, err := d.GetOperator(ctx, a.Name); err != nil || !reflect.DeepEqual(got, a) {
		t.Fatalf("after the failed update: %+v, %v, want %+v", got, err, a)
	}
}

func plmns(mcc string, n int) []PLMN {
	out := make([]PLMN, 0, n)
	for i := range n {
		out = append(out, PLMN{MCC: mcc, MNC: fmt.Sprintf("%02d", i)})
	}

	return out
}

func TestOperatorAddressesArePerOperator(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	a := createTestOperator(t, d, operatorA())

	b := operatorB()
	b.GlobalENBID = &GlobalENBID{PLMN: b.PLMNs[0], Type: ENBIDMacro, ID: 1}
	b.GlobalGNBID = &GlobalGNBID{PLMN: b.PLMNs[0], ID: 1, Length: 22}
	b.MMEs = []CoreNode{{TNLAddresses: []string{"10.0.0.1", "10.0.0.2"}}}
	b.AMFs = []CoreNode{{TNLAddresses: []string{"10.0.1.1"}}}
	b = createTestOperator(t, d, b)

	ops, err := d.ListOperators(ctx)
	if err != nil || !reflect.DeepEqual(ops, []Operator{a, b}) {
		t.Fatalf("ListOperators = %+v, %v, want %+v", ops, err, []Operator{a, b})
	}

	if err := d.DeleteOperator(ctx, a.ID); err != nil {
		t.Fatalf("DeleteOperator: %v", err)
	}

	if got, err := d.GetOperator(ctx, b.Name); err != nil || !reflect.DeepEqual(got, b) {
		t.Fatalf("GetOperator = %+v, %v, want %+v", got, err, b)
	}
}

func TestOperatorPLMNLimits(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	a := operatorA()
	a.PLMNs = append(plmns("005", 10), a.PLMNs...)
	a = createTestOperator(t, d, a)

	b := operatorB()
	b.MMEs = nil
	b.PLMNs = plmns("002", 12)
	b = createTestOperator(t, d, b)

	b.GlobalGNBID = &GlobalGNBID{PLMN: b.PLMNs[0], ID: 1, Length: 22}
	b.AMFs = []CoreNode{{TNLAddresses: []string{"10.0.1.1"}}}

	err := d.UpdateOperator(ctx, b)
	if !errors.Is(err, ErrTooManyPLMNs) || !strings.HasSuffix(err.Error(), ": Operators with AMFs can have at most 12 PLMNs in total") {
		t.Fatalf("UpdateOperator over the AMF total: %v", err)
	}

	b.AMFs = nil
	b.GlobalENBID = &GlobalENBID{PLMN: b.PLMNs[0], Type: ENBIDMacro, ID: 1}
	b.MMEs = []CoreNode{{TNLAddresses: []string{"10.0.0.1"}}}

	if err := d.UpdateOperator(ctx, b); err != nil {
		t.Fatalf("UpdateOperator within the MME total: %v", err)
	}

	c := Operator{Name: "Operator C", PLMNs: plmns("003", 9), GlobalENBID: &GlobalENBID{
		PLMN: PLMN{MCC: "003", MNC: "00"}, Type: ENBIDMacro, ID: 1,
	}, MMEs: []CoreNode{{TNLAddresses: []string{"10.0.0.1"}}}}

	if _, err := d.CreateOperator(ctx, c); !errors.Is(err, ErrTooManyPLMNs) ||
		!strings.HasSuffix(err.Error(), ": Operators with MMEs can have at most 32 PLMNs in total") {
		t.Fatalf("CreateOperator over the MME total: %v", err)
	}

	c.PLMNs, c.GlobalENBID.PLMN = c.PLMNs[:8], c.PLMNs[0]
	if _, err := d.CreateOperator(ctx, c); err != nil {
		t.Fatalf("CreateOperator at the MME total: %v", err)
	}

	if got, err := d.GetOperator(ctx, a.Name); err != nil || !reflect.DeepEqual(got, a) {
		t.Fatalf("GetOperator = %+v, %v, want %+v", got, err, a)
	}
}
