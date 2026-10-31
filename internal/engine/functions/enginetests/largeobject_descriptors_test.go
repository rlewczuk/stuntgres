package enginetests_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestLargeObjectDescriptorCall(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		call string
		oid ids.OID
		result sqltype.ID
		formals []sqltype.ID
	}{
		{"lo_open(NULL,131072)", 952, sqltype.Int4, []sqltype.ID{sqltype.Oid, sqltype.Int4}},
		{"lo_close(NULL)", 953, sqltype.Int4, []sqltype.ID{sqltype.Int4}},
		{"loread(NULL,2)", 954, sqltype.Bytea, []sqltype.ID{sqltype.Int4, sqltype.Int4}},
		{"lowrite(NULL,decode('00ff','hex'))", 955, sqltype.Int4, []sqltype.ID{sqltype.Int4, sqltype.Bytea}},
	} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, tc.call)
		n := node.(functions.LargeObjectCall)
		d, err := exec.ExpressionRoutineDescriptor(n)
		if err != nil || d == nil || d.Address().ObjectID != tc.oid || !d.Strict || d.Volatility != expr.Volatile || d.Builtin.Parallel != 'u' { t.Fatal("descriptor provider identity", d, err) }
		if n.Usage.Type.ID() != tc.result || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren { t.Fatal("typed nonfolding recipe", n) }
		for i, child := range n.Args { if child.ResultUsage().Type.ID() != tc.formals[i] { t.Fatal("signed formal carrier", i, child.ResultUsage()) } }
		value, err := n.Eval(&evaluation)
		if err != nil || !value.IsNull() || value.Type() != n.Usage.Type { t.Fatal("strict typed NULL", value, err) }
		children := append([]expr.Expr(nil), n.Args...)
		rebuilt, err := n.Rebuild(n.Info, children)
		if err != nil { t.Fatal(err) }
		copy := rebuilt.(functions.LargeObjectCall)
		children[0] = nil
		if copy.Args[0] == nil || &copy.Args[0] == &n.Args[0] || copy.RoutineReference != n.RoutineReference { t.Fatal("rebuild borrowed argument vector or lost provenance") }
	}
	// Real volatile children distinguish once-only evaluation from NULL-based
	// skipping or replay. No mock runtime or echo provider supplies this witness.
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "lo_open(NULL,nextval('strict_null_steps'::regclass)::integer)")
	for iteration := int64(1); iteration <= 2; iteration++ {
		value, err := node.Eval(&evaluation)
		if err != nil || !value.IsNull() { t.Fatal(value, err) }
		current, _, ctx := strictNullUnitBind(t, e, op, scope, "currval('strict_null_steps'::regclass)")
		value, err = current.Eval(&ctx)
		if err != nil { t.Fatal(err) }
		n, err := value.Integer()
		if err != nil || n != iteration { t.Fatal("argument demand repeated or suppressed", n, err) }
	}
	// Bypass planning deliberately: a reached provider must demand arithmetic
	// even with a runtime NULL, independently of shared planning elimination.
	runtimeNull, _, runtimeContext := strictNullUnitBind(t, e, op, scope, "loread(NULL,1/0)")
	_, runtimeErr := runtimeNull.Eval(&runtimeContext)
	var runtimeDiagnostic *sqlstate.Error
	if !errors.As(runtimeErr, &runtimeDiagnostic) || runtimeDiagnostic.Code != "22012" || runtimeDiagnostic.Message != "division by zero" { t.Fatal("runtime NULL erased sibling error", runtimeErr) }
	invalid, _, ctx := strictNullUnitBind(t, e, op, scope, "loread(-1,0)")
	_, err := invalid.Eval(&ctx)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" || diagnostic.Message != "invalid large-object descriptor: -1" { t.Fatal("descriptor coerced through OID", err) }
	// Runtime result carriers use the actual transaction table, not a synthetic
	// capability. Bind/Describe above has opened no slots; the first is still 0.
	_, err = core.HostOf(scope).LargeObject(functions.LargeObjectRequest{Name: "lo_from_bytea", OID: 910291, Data: "abcdef"})
	if err != nil { t.Fatal(err) }
	open, _, ctx := strictNullUnitBind(t, e, op, scope, "lo_open(910291::oid,131072)")
	before := open.(functions.LargeObjectCall)
	value, err := open.Eval(&ctx)
	if err != nil { t.Fatal(err) }
	fd, err := value.Integer()
	if err != nil || fd != 0 || value.Type().ID() != sqltype.Int4 { t.Fatal("INT4 open result", value, err) }
	write, _, writeContext := strictNullUnitBind(t, e, op, scope, "lowrite(0,decode('00ff','hex'))")
	value, err = write.Eval(&writeContext)
	if err != nil { t.Fatal(err) }
	count, err := value.Integer()
	if err != nil || count != 2 || value.Type().ID() != sqltype.Int4 { t.Fatal("INT4 write result", value, err) }
	read, _, readContext := strictNullUnitBind(t, e, op, scope, "loread(0,2)")
	value, err = read.Eval(&readContext)
	if err != nil { t.Fatal(err) }
	data, err := value.Binary()
	if err != nil || data != "cd" || value.Type().ID() != sqltype.Bytea { t.Fatal("BYTEA descriptor result", value, err) }
	if !reflect.DeepEqual(before, open) { t.Fatal("recipe retained runtime cursor or transaction authority") }
	original := scope.TxRef
	scope.TxRef = original.Domain().Tx(original.Session(), original.ID(), original.Epoch()+1, original.Attachment(), original.Diagnostic())
	_, fenceErr := core.HostOf(scope).LargeObject(functions.LargeObjectRequest{Name: "loread", Descriptor: 0, Length: 1})
	scope.TxRef = original
	if !errors.Is(fenceErr, ids.ErrCatalogReplaced) { t.Fatal("old descriptor crossed catalog epoch", fenceErr) }
}

func TestLargeObjectDescriptorArgumentDemand(t *testing.T) {
	for _, planner := range []struct {
		name string
		run func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
	}{{"preflight", exec.PreflightCoercionsWithPolicy}, {"conditional", exec.SimplifyConditional}} {
		t.Run(planner.name, func(t *testing.T) {
			for _, text := range []string{
				"lo_open($1::oid,1/$2::integer)",
				"loread($1::integer,1/$2::integer)",
				"lowrite(1/$2::integer,$1::bytea)",
				"lo_get($1::oid,0,1/$2::integer)",
				"lo_put($1::oid,1::bigint/$2::bigint,NULL::bytea)",
			} {
				t.Run(text, func(t *testing.T) {
					e, op, scope := strictNullUnitFixture(t)
					node, binder, evaluation := strictNullUnitBind(t, e, op, scope, text)
					zero, err := sqltype.Integer(binder.Params.Parameters[2].Type, 0)
					if err != nil { t.Fatal(err) }
					evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type), zero}
					planned, _, err := planner.run(&evaluation, node, true)
					if err == nil {
						_, err = planned.Eval(&evaluation)
					}
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || diagnostic.Message != "division by zero" {
						t.Fatal("strict NULL suppressed demanded child error", err)
					}
				})
			}
			for _, text := range []string{
				"lo_open(NULL,nextval('strict_null_steps'::regclass)::integer)",
				"loread(NULL,nextval('strict_null_steps'::regclass)::integer)",
				"lowrite(nextval('strict_null_steps'::regclass)::integer,NULL::bytea)",
				"lo_get(NULL,nextval('strict_null_steps'::regclass),1)",
				"lo_put(NULL,nextval('strict_null_steps'::regclass),NULL::bytea)",
			} {
				t.Run(text, func(t *testing.T) {
					e, op, scope := strictNullUnitFixture(t)
					node, _, evaluation := strictNullUnitBind(t, e, op, scope, text)
					sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "strict_null_steps")
					planned, _, err := planner.run(&evaluation, node, true)
					if err != nil { t.Fatal(err) }
					for range 2 {
						value, err := planned.Eval(&evaluation)
						if err != nil || !value.IsNull() || value.Type().ID() != node.ResultUsage().Type.ID() { t.Fatal("typed strict result", value, err) }
						e.Mu.Lock()
						called := e.Storage.Sequences[sequence.Address].Called
						e.Mu.Unlock()
						if called { t.Fatal("known NULL executed volatile sibling") }
					}
				})
			}
		})
	}
}
