package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsCountFailuresOnly(t *testing.T) {
	d := openTestDB(t)
	ctx := t.Context()

	if _, err := d.CountUsers(ctx); err != nil {
		t.Fatal(err)
	}

	if err := d.DeleteUser(ctx, 42); err == nil {
		t.Fatal("DeleteUser of a missing user succeeded")
	}

	if n := testutil.CollectAndCount(d.metrics.duration); n != 2 {
		t.Fatalf("duration has %d series, want 2", n)
	}

	for pool, want := range map[string]float64{poolWrite: 0, poolRead: 0} {
		if got := testutil.ToFloat64(d.metrics.errors.WithLabelValues(pool)); got != want {
			t.Errorf("errors{pool=%q} = %v, want %v", pool, got, want)
		}
	}

	if err := d.reader().Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := d.CountUsers(ctx); err == nil {
		t.Fatal("CountUsers on a closed database succeeded")
	}

	if got := testutil.ToFloat64(d.metrics.errors.WithLabelValues(poolRead)); got != 1 {
		t.Errorf("errors{pool=%q} = %v, want 1", poolRead, got)
	}
}

func TestMetricsLint(t *testing.T) {
	d := openTestDB(t)

	for _, c := range d.Collectors() {
		problems, err := testutil.CollectAndLint(c)
		if err != nil {
			t.Fatal(err)
		}

		for _, p := range problems {
			t.Errorf("lint: %s: %s", p.Metric, p.Text)
		}
	}
}

func TestEveryCallIsObserved(t *testing.T) {
	unobserved := map[string]bool{
		"Close": true, "Collectors": true, "Ping": true, "JWTSecret": true,
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() || unobserved[fn.Name.Name] || !onDB(fn) {
				continue
			}

			if !startsObserved(fn) {
				t.Errorf("%s: DB.%s does not start with d.observe(ctx, %q, ...) and defer done(&err)",
					fset.Position(fn.Pos()), fn.Name.Name, fn.Name.Name)
			}
		}
	}
}

func onDB(fn *ast.FuncDecl) bool {
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}

	id, ok := star.X.(*ast.Ident)

	return ok && id.Name == "DB"
}

func startsObserved(fn *ast.FuncDecl) bool {
	if fn.Body == nil || len(fn.Body.List) < 2 {
		return false
	}

	assign, ok := fn.Body.List[0].(*ast.AssignStmt)
	if !ok || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return false
	}

	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 3 {
		return false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "observe" {
		return false
	}

	name, ok := call.Args[1].(*ast.BasicLit)
	if !ok || name.Kind != token.STRING || name.Value != strconv.Quote(fn.Name.Name) {
		return false
	}

	done, ok := assign.Lhs[1].(*ast.Ident)
	if !ok {
		return false
	}

	d, ok := fn.Body.List[1].(*ast.DeferStmt)
	if !ok {
		return false
	}

	callee, ok := d.Call.Fun.(*ast.Ident)

	return ok && callee.Name == done.Name
}
