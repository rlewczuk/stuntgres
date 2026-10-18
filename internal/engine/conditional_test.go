package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Spies exercise evaluator demand, not a substituted PostgreSQL oracle.
type conditionalSpy struct {
	expr.Info
	value   sqltype.Value
	calls   *int
	failure error
}

func (e conditionalSpy) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	*e.calls++
	return e.value, e.failure
}

func TestConditionalRuntimeDemand(t *testing.T) {
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	value, _ := sqltype.Integer(integer, 1)
	other, _ := sqltype.Integer(integer, 2)
	ctx := &EvalContext{Context: context.Background()}
	calls := [5]int{}
	spy := func(i int, v sqltype.Value) expr.Expr {
		return conditionalSpy{expr.Info{Usage: v.Type().Usage(), Property: expr.Properties{RowDependent: true}}, v, &calls[i], nil}
	}
	comparison, err := sqltype.BindComparison(integer, integer, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := sqltype.PlanCoercion(integer.Usage(), integer.Usage(), sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	info := expr.Info{Usage: integer.Usage()}
	searched := caseExpression{Info: info, arms: []caseBranch{{condition: spy(0, sqltype.Boolean(boolean, true)), result: spy(1, value)}, {condition: spy(2, sqltype.Boolean(boolean, true)), result: spy(3, other)}}, otherwise: spy(4, other)}
	got, err := searched.Eval(ctx)
	if err != nil || !reflect.DeepEqual(got, value) || calls != [5]int{1, 1, 0, 0, 0} {
		t.Fatal(got, calls, err)
	}
	calls = [5]int{}
	simple := caseExpression{Info: info, test: spy(0, value), arms: []caseBranch{{condition: spy(1, other), result: spy(2, other), testPlan: plan, testUsage: integer.Usage(), equality: comparison}, {condition: spy(3, value), result: spy(4, value), testPlan: plan, testUsage: integer.Usage(), equality: comparison}}, otherwise: literalExpression{info, other}}
	got, err = simple.Eval(ctx)
	if err != nil || !reflect.DeepEqual(got, value) || calls != [5]int{1, 1, 0, 1, 1} {
		t.Fatal(got, calls, err)
	}
	calls = [5]int{}
	coalesce := conditionalExpression{Info: info, kind: parser.Coalesce, args: []expr.Expr{spy(0, sqltype.Null(integer)), spy(1, value), spy(2, other)}}
	got, err = coalesce.Eval(ctx)
	if err != nil || !reflect.DeepEqual(got, value) || calls != [5]int{1, 1, 0, 0, 0} {
		t.Fatal(got, calls, err)
	}
	calls = [5]int{}
	nullif := conditionalExpression{Info: info, kind: parser.NullIf, args: []expr.Expr{spy(0, sqltype.Null(integer)), spy(1, value)}, comparison: comparison}
	got, err = nullif.Eval(ctx)
	if err != nil || !got.IsNull() || calls != [5]int{1, 1, 0, 0, 0} {
		t.Fatal(got, calls, err)
	}
	calls = [5]int{}
	extrema := conditionalExpression{Info: info, kind: parser.Greatest, args: []expr.Expr{spy(0, other), spy(1, sqltype.Null(integer)), spy(2, value), spy(3, other)}, comparison: comparison}
	got, err = extrema.Eval(ctx)
	if err != nil || !reflect.DeepEqual(got, other) || calls != [5]int{1, 1, 1, 1, 0} {
		t.Fatal(got, calls, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := searched.Eval(&EvalContext{Context: canceled}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestConditionalAnalysisOrderAndMetadata(t *testing.T) {
	e := New()
	for _, tc := range []struct {
		expr   string
		id     sqltype.ID
		params []sqltype.ID
	}{
		{"COALESCE($1,1)", sqltype.Int4, []sqltype.ID{sqltype.Int4}},
		{"CASE WHEN $1 THEN 1 ELSE 2 END", sqltype.Int4, []sqltype.ID{sqltype.Bool}},
		{"NULLIF($1,1)", sqltype.Int4, []sqltype.ID{sqltype.Int4}},
		{"COALESCE($1,NULL)", sqltype.Text, []sqltype.ID{sqltype.Text}},
		{"NULLIF(1,2147483648)", sqltype.Int4, nil},
		{"NULLIF(1,2.2)", sqltype.Numeric, nil},
		{"CASE WHEN TRUE THEN 1 ELSE 2147483648 END", sqltype.Int8, nil},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		ast := statement(t, "SELECT "+tc.expr+" FROM t").(*parser.Select)
		node, err := b.bind(ast.Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc.expr, err)
		}
		if node.ResultUsage().Type.ID() != tc.id || node.ResultUsage().Typmod != -1 {
			t.Fatal(tc, node.ResultUsage())
		}
		p, err := b.finish()
		if err != nil {
			t.Fatal(err)
		}
		if p.Count != len(tc.params) {
			t.Fatal(p)
		}
		for i, id := range tc.params {
			if p.Types[i].Type.ID() != id {
				t.Fatal(p)
			}
		}
	}
	for _, tc := range []struct{ q, code, message, position, detail string }{
		{"SELECT COALESCE($1,1),COALESCE($1,TRUE) FROM t", "42804", "COALESCE types integer and boolean cannot be matched", "35", ""},
		{"SELECT CASE $1 WHEN 1 THEN 2 END FROM t", "42883", "operator does not exist: text = integer", "16", ""},
		{"SELECT $1,COALESCE($1,1) FROM t", "42P08", "inconsistent types deduced for parameter $1", "8", "integer versus text"},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		ast := statement(t, tc.q).(*parser.Select)
		var nodes []expr.Expr
		var err error
		for _, raw := range ast.Targets {
			var node expr.Expr
			node, err = b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				break
			}
			nodes = append(nodes, node)
		}
		if err == nil {
			for _, node := range nodes {
				_, err = b.finalizeProjection(node)
				if err != nil {
					break
				}
			}
		}
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message || d.Position != tc.position || d.Detail != tc.detail {
			t.Fatal(tc, err)
		}
	}
}

func TestConditionalCoercionOperandPosition(t *testing.T) {
	e := New()
	for _, q := range []string{"SELECT COALESCE(DATE '2000-01-01',TIME '00:00:00') FROM t", "SELECT CASE WHEN TRUE THEN TIME '00:00:00' ELSE DATE '2000-01-01' END FROM t"} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, q).(*parser.Select).Targets[0]
		_, err := b.bind(raw, expressionPolicy{context: projectionContext})
		var d *sqlstate.Error
		// Typed literals retain the STRING token's location, not the type name.
		wantPosition := "40"
		wantMessage := "COALESCE could not convert type time without time zone to date"
		if _, ok := raw.(parser.CaseExpr); ok {
			wantPosition = "33"
			wantMessage = "CASE/WHEN could not convert type time without time zone to date"
		}
		if !errors.As(err, &d) || d.Code != "42846" || d.Message != wantMessage || d.Position != wantPosition || d.Detail != "" || d.Hint != "" {
			t.Fatalf("%s: got %#v (%v), want 42846 %q at %s", q, d, err, wantMessage, wantPosition)
		}
	}
}
func TestConditionalPlanningDemand(t *testing.T) {
	e := New()
	ctx := &EvalContext{Context: context.Background()}
	for _, tc := range []struct {
		expr string
		code string
	}{
		{"CASE WHEN TRUE THEN 1 ELSE 1/0 END", ""}, {"CASE WHEN FALSE THEN 1/0 ELSE 1 END", ""}, {"COALESCE(1,1/0)", ""},
		{"CASE WHEN $1 THEN 1 ELSE 1/0 END", "22012"}, {"COALESCE($1,1/0)", "22012"},
		{"CASE WHEN $1 THEN (CASE WHEN TRUE THEN 1 ELSE CAST(CAST($2 AS TEXT) AS INTEGER) END)/0 ELSE 1 END", "22012"},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT "+tc.expr+" FROM t").(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = preflightCoercions(ctx, node)
		if tc.code == "" {
			if err != nil {
				t.Fatal(tc, err)
			}
		} else {
			state(t, err, tc.code)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT CASE WHEN $1 THEN 1 ELSE CAST(CAST($2 AS TEXT) AS INTEGER) END FROM t").(*parser.Select).Targets[0]
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	ctx.Parameters = []sqltype.Value{sqltype.Boolean(boolean, true), sqltype.String(text, "bad")}
	planned, _, err := preflightCoercions(ctx, node)
	if err != nil {
		t.Fatal("skipped parameter cast was eagerly evaluated", err)
	}
	got, err := planned.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := got.Integer()
	if err != nil || n != 1 {
		t.Fatal(got, err)
	}
	ctx.Parameters[0] = sqltype.Boolean(boolean, false)
	_, err = planned.Eval(ctx)
	state(t, err, "22P02")
}

func TestConditionalConservativeAuthorizationAndOrigins(t *testing.T) {
	e := New()
	owner, finish := roleOperation(t, e, "postgres")
	defer finish()
	roleExecute(t, e, owner, "CREATE USER reader")
	roleExecute(t, e, owner, "CREATE TABLE secret(a INT,b INT,s VARCHAR(3),r VARCHAR(3))")
	roleExecute(t, e, owner, "INSERT INTO secret VALUES(1,2,'x','y')")
	for _, tc := range []struct {
		q, name string
		mod     int32
	}{
		{"SELECT CASE WHEN TRUE THEN a ELSE a END FROM secret", "a", -1},
		{"SELECT COALESCE(s,r) FROM secret", "coalesce", 7},
		{"SELECT CASE WHEN TRUE THEN s ELSE r END FROM secret", "r", 7},
		{"SELECT CASE WHEN TRUE THEN s END FROM secret", "case", -1},
		{"SELECT NULLIF(s,r) FROM secret", "nullif", -1},
	} {
		result := roleExecute(t, e, owner, tc.q)
		schema := result.Rows.Schema()[0]
		if schema.Name != tc.name || schema.Usage.Typmod != tc.mod || schema.OriginTable != 0 || schema.OriginAttribute != 0 || schema.NullabilityKnown {
			t.Fatal(tc, schema)
		}
		result.Rows.Close()
	}
	column := roleTableRef(t, e, "secret")
	column.Address.SubID = 1
	reader, _ := e.Roles().Lookup("reader")
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, owner, "SET SESSION AUTHORIZATION reader")
	for _, q := range []string{"SELECT CASE WHEN TRUE THEN 1 ELSE a END FROM secret", "SELECT COALESCE(1,a) FROM secret", "SELECT CASE a WHEN 1 THEN 2 ELSE 3 END FROM secret"} {
		r := roleExecute(t, e, owner, q)
		r.Rows.Close()
	}
	for _, q := range []string{"SELECT CASE WHEN TRUE THEN 1 ELSE b END FROM secret", "SELECT COALESCE(1,b) FROM secret", "SELECT CASE 1 WHEN b THEN 2 ELSE 3 END FROM secret"} {
		roleError(t, e, owner, q, "42501", "permission denied for table secret", "")
	}
}

func TestConditionalTieRepresentationAndNameExecution(t *testing.T) {
	e := New()
	floating := eType(t, e, sqltype.Float8)
	negative, err := sqltype.Float(floating, math.Copysign(0, -1))
	if err != nil {
		t.Fatal(err)
	}
	positive, err := sqltype.Float(floating, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := expr.Info{Usage: floating.Usage()}
	comparison, err := sqltype.BindComparison(floating, floating, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []parser.ConditionalKind{parser.Greatest, parser.Least} {
		node := conditionalExpression{Info: info, kind: kind, args: []expr.Expr{literalExpression{info, negative}, literalExpression{info, positive}}, comparison: comparison}
		got, err := node.Eval(&EvalContext{Context: context.Background()})
		if err != nil {
			t.Fatal(err)
		}
		number, err := got.Float()
		if err != nil || !math.Signbit(number) {
			t.Fatal("tie lost first representation", number, err)
		}
	}
	name := eType(t, e, sqltype.Name)
	for _, tc := range []struct {
		expr string
		want string
		null bool
	}{
		{"COALESCE($1,'other')", "same", false},
		{"NULLIF($1,text('same'))", "", true},
		{"GREATEST($1,'other')", "same", false},
		{"CASE $1 WHEN 'same' THEN $1 ELSE 'other' END", "same", false},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		b.parameters[1] = name.Usage()
		raw := statement(t, "SELECT "+tc.expr+" FROM t").(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc, err)
		}
		if node.ResultUsage().Type != name {
			t.Fatal(tc, node.ResultUsage())
		}
		got, err := node.Eval(&EvalContext{Context: context.Background(), Parameters: []sqltype.Value{sqltype.String(name, "same")}})
		if err != nil {
			t.Fatal(tc, err)
		}
		if got.IsNull() != tc.null {
			t.Fatal(tc, got)
		}
		if !tc.null {
			text, err := got.Text()
			if err != nil || text != tc.want {
				t.Fatal(tc, text, err)
			}
		}
	}
	mood, err := sqltype.Enum(45000, "mood", "public.mood", []string{"z", "a"})
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := sqltype.Domain(45001, "wrapped", "public.wrapped", mood.Usage())
	if err != nil {
		t.Fatal(err)
	}
	first, err := wrapped.Input(context.Background(), "z", -1)
	if err != nil {
		t.Fatal(err)
	}
	last, err := wrapped.Input(context.Background(), "a", -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		expr string
		want sqltype.Value
	}{
		{"GREATEST($1,$2,$3)", last},
		{"LEAST($1,$2,$3)", first},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		for number := 1; number <= 3; number++ {
			b.parameters[number] = wrapped.Usage()
		}
		raw := statement(t, "SELECT "+tc.expr+" FROM t").(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc.expr, err)
		}
		if node.ResultUsage().Type != wrapped {
			t.Fatal("enum extrema lost domain result descriptor", node.ResultUsage())
		}
		for _, parameters := range [][]sqltype.Value{
			{first, sqltype.Null(wrapped), last},
			{sqltype.Null(wrapped), sqltype.Null(wrapped), sqltype.Null(wrapped)},
		} {
			got, err := node.Eval(&EvalContext{Context: context.Background(), Parameters: parameters})
			if err != nil {
				t.Fatal(tc.expr, err)
			}
			want := tc.want
			if parameters[0].IsNull() {
				want = sqltype.Null(wrapped)
			}
			if got.Type() != wrapped || !reflect.DeepEqual(got, want) {
				t.Fatal("enum extrema lost domain identity, declaration order or NULL", tc.expr, got, want)
			}
		}
	}
}

func TestConditionalStoredDefaultRetagProviders(t *testing.T) {
	source, target := New(), New()
	b := newTestExpressionBinder(t, context.Background(), source, sqltype.InputEnvironment{}, nil)
	b.defaultConsumer = true
	raw := statement(t, "SELECT CASE 2147483648 WHEN 2147483648 THEN COALESCE(NULL,NULLIF(2147483649,2147483650)) ELSE GREATEST(2147483651,2147483652) END FROM t").(*parser.Select).Targets[0]
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	retained := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old.ID() == sqltype.Int8 || old.ID() == sqltype.Bool {
			return eType(t, target, old.ID())
		}
		return old
	}
	retagged, changed, err := retagBoundTypes(target.CatalogRead(), node, retained)
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	got, err := retagged.Eval(&EvalContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	number, err := got.Integer()
	if err != nil || number != 2147483649 || got.Type() != eType(t, target, sqltype.Int8) {
		t.Fatal(number, got.Type(), err)
	}
	old, err := node.Eval(&EvalContext{Context: context.Background()})
	if err != nil || old.Type() != eType(t, source, sqltype.Int8) {
		t.Fatal("retag mutated old default", old, err)
	}
	if _, err := defaultSQL(context.Background(), target.CatalogRead(), retagged); err != nil {
		t.Fatal("retagged serialization provider ownership", err)
	}
	_, changed, err = retagBoundTypes(target.CatalogRead(), retagged, retained)
	if err != nil || changed {
		t.Fatal("unchanged tree copied", changed, err)
	}
}

func TestConditionalEnumStoredExtremaRetagProviders(t *testing.T) {
	ctx := context.Background()
	source, target := New(), New()
	for _, e := range []*Engine{source, target} {
		op, done := roleOperation(t, e, "postgres")
		domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE mood AS ENUM ('z','a')"))
		domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN wrapped AS mood"))
		done()
	}
	oldEnum := domainUnitType(t, source.CatalogRead(), "mood").SQLType
	oldDomain := domainUnitType(t, source.CatalogRead(), "wrapped").SQLType
	newEnum := domainUnitType(t, target.CatalogRead(), "mood").SQLType
	newDomain := domainUnitType(t, target.CatalogRead(), "wrapped").SQLType
	if oldEnum == newEnum || oldDomain == newDomain {
		t.Fatal("ownership fixture did not remint descriptors")
	}
	retained := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		switch old {
		case oldEnum:
			return newEnum
		case oldDomain:
			return newDomain
		default:
			return old
		}
	}
	bind := func(e *Engine, expression string) expr.Expr {
		t.Helper()
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		b.defaultConsumer = true
		raw := statement(t, "SELECT "+expression+" FROM t").(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		return node
	}
	assertValue := func(node expr.Expr, domain *sqltype.Descriptor, label string, null bool) {
		t.Helper()
		got, err := node.Eval(&EvalContext{Context: ctx})
		if err != nil || got.Type() != domain || got.IsNull() != null || node.ResultUsage().Type != domain {
			t.Fatal("stored extrema identity/NULL", got, node.ResultUsage(), err)
		}
		if !null {
			text, err := domain.Output(ctx, got)
			if err != nil || text != label {
				t.Fatal("stored extrema declaration order", text, label, err)
			}
		}
	}
	for _, tc := range []struct {
		expression, label string
		null              bool
	}{
		{"GREATEST('z'::wrapped,NULL::wrapped,'a'::wrapped)", "a", false},
		{"LEAST('z'::wrapped,NULL::wrapped,'a'::wrapped)", "z", false},
		{"GREATEST(NULL::wrapped,NULL::wrapped)", "", true},
		{"LEAST(NULL::wrapped,NULL::wrapped)", "", true},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			node := bind(source, tc.expression)
			before := expr.InfoOf(node)
			assertValue(node, oldDomain, tc.label, tc.null)
			rendered, err := defaultSQL(ctx, source.CatalogRead(), node)
			if err != nil {
				t.Fatal("original stored rendering", err)
			}
			assertValue(bind(source, rendered), oldDomain, tc.label, tc.null)
			next, changed, err := retagBoundTypes(New().CatalogRead(), node, retained)
			if err != nil || !changed {
				t.Fatal("stored extrema reownership", changed, err)
			}
			assertValue(next, newDomain, tc.label, tc.null)
			original, reminted := node.(conditionalExpression), next.(conditionalExpression)
			expectedInfo := before
			expectedInfo.Usage.Type = newDomain
			if original.Info != before || reminted.Info != expectedInfo {
				t.Fatal("reownership changed Usage fields, properties or positions")
			}
			oldProvider, err := sqltype.BindOrdering(oldDomain.Usage())
			if err != nil {
				t.Fatal(err)
			}
			newProvider, err := sqltype.BindOrdering(newDomain.Usage())
			if err != nil || !original.comparison.Equal(oldProvider) || !reminted.comparison.Equal(newProvider) {
				t.Fatal("stored extrema provider ownership", err)
			}
			if &original.args[0] == &reminted.args[0] {
				t.Fatal("reownership retained mutable operand slice")
			}
			for i := range original.args {
				if original.args[i].ResultUsage().Type != oldDomain || reminted.args[i].ResultUsage().Type != newDomain {
					t.Fatal("reownership mutated original operands")
				}
				oldValue, err := original.args[i].Eval(&EvalContext{Context: ctx})
				if err != nil || oldValue.Type() != oldDomain {
					t.Fatal("original operand datum ownership", oldValue, err)
				}
				newValue, err := reminted.args[i].Eval(&EvalContext{Context: ctx})
				if err != nil || newValue.Type() != newDomain || newValue.IsNull() != oldValue.IsNull() {
					t.Fatal("reminted operand datum ownership", newValue, err)
				}
			}
			rendered, err = defaultSQL(ctx, target.CatalogRead(), next)
			if err != nil {
				t.Fatal("reminted stored rendering", err)
			}
			assertValue(bind(target, rendered), newDomain, tc.label, tc.null)
			assertValue(node, oldDomain, tc.label, tc.null)
			if _, err := defaultSQL(ctx, source.CatalogRead(), node); err != nil {
				t.Fatal("original stored ownership changed", err)
			}
			unchanged, changed, err := retagBoundTypes(New().CatalogRead(), next, retained)
			if err != nil || changed {
				t.Fatal("unchanged stored tree reminted", changed, err)
			}
			if &unchanged.(conditionalExpression).args[0] != &reminted.args[0] {
				t.Fatal("unchanged stored tree copied operand slice")
			}
			invalid := reminted
			invalid.comparison = oldProvider
			if _, err := defaultSQL(ctx, target.CatalogRead(), invalid); err == nil {
				t.Fatal("renderer accepted foreign comparison ownership")
			}
		})
	}
}

func TestConditionalInsertPlanningLockBoundary(t *testing.T) {
	for _, mode := range []string{"success", "authority", "descriptor", "shape", "epoch", "preflight", "relock", "teardown"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(i INT4)")
			run(t, e, "CREATE ROLE actor LOGIN")
			actor, _ := e.Roles().Lookup("actor")
			ctx := &castPreflightContext{Context: context.Background(), e: e, t: t, cancelAt: mode}
			op, done, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			ctx.Context = op.ctx
			op.ctx = ctx
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if !scope.finished {
					_ = scope.Finish(context.Canceled)
				}
			}()
			var restore func()
			ctx.visit = func() {
				if scope.view != nil {
					t.Error("INSERT captured data before planning/revalidation")
				}
				if scope.lockedRef.Domain() != e.catalog.Domain || scope.lockedDescriptor == nil {
					t.Error("INSERT lost locked relation identity")
				}
				switch mode {
				case "authority":
					op.session.mu.Lock()
					op.session.authorization.state.CurrentUser = actor.Ref
					op.session.mu.Unlock()
				case "teardown":
					op.session.RequestEnd(CleanClose)
				case "descriptor", "shape":
					address := scope.lockedRef.Address
					original := e.catalog.state.Get(address)
					replacement := *scope.lockedDescriptor.(*relationDescriptor)
					replacement.ObjectRevision++
					if mode == "shape" {
						replacement.Attributes = append([]int32(nil), replacement.Attributes...)
						replacement.Attributes[0]++
					} else {
						replacement.Comment = "compatible metadata revision"
					}
					e.catalog.state.setDescriptor(address, &replacement)
					restore = func() { e.catalog.state.setDescriptor(address, original) }
				case "epoch":
					original := e.catalog.state.Epoch()
					e.catalog.state.SetEpoch(e.catalog.state.Epoch() + 1)
					restore = func() { e.catalog.state.SetEpoch(original) }
				}
			}
			result, executeErr := scope.Execute(statement(t, "INSERT INTO t VALUES(CASE WHEN TRUE THEN 1+2 ELSE CAST(CAST($1 AS TEXT) AS INTEGER) END)"), 1, func(c context.Context, signature ParameterInfo, frame []sqltype.Value, environment sqltype.InputEnvironment) error {
				value, err := signature.Types[0].Type.Input(c, "bad", -1)
				if err != nil {
					return err
				}
				frame[0] = value
				ctx.armed = true
				return nil
			})
			ctx.armed = false
			if restore != nil {
				e.mu.Lock()
				restore()
				e.mu.Unlock()
			}
			capturedView := scope.view != nil
			finishErr := scope.Finish(executeErr)
			done()
			if finishErr != executeErr {
				t.Error(finishErr, executeErr)
			}
			if !ctx.entered {
				t.Error("INSERT planning never entered unlocked checkpoint")
			}
			switch mode {
			case "success":
				if executeErr != nil || result.Count != 1 {
					t.Error(result, executeErr)
				}
			case "authority":
				state(t, executeErr, "42501")
			case "descriptor", "shape", "epoch":
				if !errors.Is(executeErr, ids.ErrCatalogReplaced) {
					t.Error(executeErr)
				}
			default:
				if !errors.Is(executeErr, context.Canceled) {
					t.Error(executeErr)
				}
			}
			if mode != "success" && (result.Rows != nil || capturedView) {
				t.Error("failed INSERT planning captured data")
			}
			want := int64(0)
			if mode == "success" {
				want = 1
			}
			published := run(t, e, "SELECT i FROM t")
			if published.Rows != nil {
				if err := published.Rows.Close(); err != nil {
					t.Error(err)
				}
			}
			if published.Count != want {
				t.Errorf("INSERT publication count %d, want %d", published.Count, want)
			}
		})
	}
}

func TestConditionalDefaultDecisivePolicy(t *testing.T) {
	bad := "CAST(CAST('oops' AS TEXT) AS INTEGER)=1"
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{"CASE WHEN TRUE THEN FALSE AND " + bad + " ELSE FALSE END", false},
		{"CASE WHEN TRUE THEN TRUE OR " + bad + " ELSE FALSE END", true},
		{"COALESCE(NULL,FALSE AND " + bad + ")", false},
		{"COALESCE(NULL,TRUE OR " + bad + ")", true},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE d(a BOOL DEFAULT ("+tc.expr+"),k INT DEFAULT 1)")
			view := e.CatalogRead()
			ref := roleTableRef(t, e, "d")
			d := view.MustRelation(ref.Address)
			original := d.Defaults[d.Attributes[0]]
			c := &EvalContext{Context: context.Background()}
			planned, changed, err := preflightCoercionsWithPolicy(c, original.Expression, true)
			if err != nil || !changed {
				t.Fatal(planned, changed, err)
			}
			if planned.Properties() != original.Expression.Properties() || planned.ResultUsage() != original.Expression.ResultUsage() || planned.SourcePosition() != original.Expression.SourcePosition() {
				t.Fatal("analysis metadata changed")
			}
			value, err := planned.Eval(c)
			if err != nil {
				t.Fatal(err)
			}
			truth, err := TruthOf(value)
			if err != nil || truth == Unknown || (truth == True) != tc.want {
				t.Fatal(truth, err, tc.want)
			}
			_, _, err = preflightCoercions(c, original.Expression)
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatalf("ordinary policy: %#v (%v), want %#v", diagnostic, err, want)
			}
			run(t, e, "INSERT INTO d VALUES(DEFAULT,DEFAULT)")
			run(t, e, "CREATE TABLE src(i INT)")
			if r := run(t, e, "INSERT INTO d(k) SELECT i FROM src"); r.Count != 0 {
				t.Fatal(r.Count)
			}
			run(t, e, "INSERT INTO d(k) VALUES(1)")
			run(t, e, "INSERT INTO src VALUES(1)")
			if r := run(t, e, "INSERT INTO d(k) SELECT i FROM src"); r.Count != 1 {
				t.Fatal(r.Count)
			}
			if err := (relationProvider{}).Validate(view, d); err != nil {
				t.Fatal("stored definition invalid", err)
			}
			if !reflect.DeepEqual(d.Defaults[d.Attributes[0]], original) {
				t.Fatal("stored default changed")
			}
		})
	}
	for _, expr := range []string{
		"CASE WHEN TRUE THEN TRUE AND " + bad + " ELSE FALSE END",
		"CASE WHEN TRUE THEN FALSE OR " + bad + " ELSE FALSE END",
		"COALESCE(NULL,NULL AND " + bad + ")",
		"COALESCE(NULL,NULL OR " + bad + ")",
	} {
		e := New()
		run(t, e, "CREATE TABLE d(a BOOL DEFAULT ("+expr+"))")
		ref := roleTableRef(t, e, "d")
		d := e.CatalogRead().MustRelation(ref.Address)
		_, _, err := preflightCoercionsWithPolicy(&EvalContext{Context: context.Background()}, d.Defaults[d.Attributes[0]].Expression, true)
		state(t, err, "22P02")
		run(t, e, "INSERT INTO d VALUES(FALSE)")
	}
}

func TestConditionalInsertPlanningPriorityAtomicity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE d(id INT NOT NULL,v INT DEFAULT CAST(CAST('declared' AS TEXT) AS INTEGER))")
	run(t, e, "INSERT INTO d VALUES(0,9)")
	run(t, e, "CREATE ROLE actor LOGIN")
	actor, _ := e.Roles().Lookup("actor")
	for _, denied := range []bool{false, true} {
		op, done, err := testOperation(e, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if denied {
			op.session.mu.Lock()
			op.session.authorization.state.CurrentUser = actor.Ref
			op.session.mu.Unlock()
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			done()
			t.Fatal(err)
		}
		_, executeErr := scope.Execute(statement(t, "INSERT INTO d(id) VALUES(CASE WHEN TRUE THEN CAST(CAST('supplied' AS TEXT) AS INTEGER) ELSE 0 END)"), 0, nil)
		finishErr := scope.Finish(executeErr)
		done()
		if finishErr != executeErr {
			t.Fatal(finishErr, executeErr)
		}
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type integer: "supplied"`}
		if !errors.As(executeErr, &diagnostic) || *diagnostic != want {
			t.Fatalf("supplied/default/ACL priority: %#v (%v), want %#v", diagnostic, executeErr, want)
		}
	}
	r := run(t, e, "SELECT * FROM d")
	got := tuples(r)
	r.Rows.Close()
	if !reflect.DeepEqual(got, [][]driver.Value{{int64(0), int64(9)}}) {
		t.Fatal("partial supplied/default publication", got)
	}
	run(t, e, "CREATE TABLE t(i INT NOT NULL)")
	run(t, e, "INSERT INTO t VALUES(5)")
	q := "INSERT INTO t VALUES(COALESCE(1,CAST(CAST($1 AS TEXT) AS INTEGER))),(CASE WHEN TRUE THEN CAST(CAST($1 AS TEXT) AS INTEGER) ELSE 0 END)"
	_, err := testExecute(e, context.Background(), statement(t, q), []driver.NamedValue{{Ordinal: 1, Value: "bad"}})
	state(t, err, "22P02")
	r = run(t, e, "SELECT i FROM t")
	got = tuples(r)
	r.Rows.Close()
	if !reflect.DeepEqual(got, [][]driver.Value{{int64(5)}}) {
		t.Fatal("partial later-row publication", got)
	}
	if r := run(t, e, q, "7"); r.Count != 2 {
		t.Fatal(r.Count)
	}
	r = run(t, e, "SELECT i FROM t ORDER BY i")
	got = tuples(r)
	r.Rows.Close()
	if !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(5)}, {int64(7)}}) {
		t.Fatal("healthy INSERT reuse", got)
	}
}
