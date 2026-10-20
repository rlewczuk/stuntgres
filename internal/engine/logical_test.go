package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

func TestLogicalTypedTruthAndFrames(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Bool)
	values := []sqltype.Value{sqltype.Boolean(typ, true), sqltype.Boolean(typ, false), sqltype.Null(typ)}
	// Each row explicitly specifies AND, OR and NOT for an ordered input pair.
	cases := []struct {
		a, b         int
		and, or, not plan.Truth
	}{
		{0, 0, plan.True, plan.True, plan.False}, {0, 1, plan.False, plan.True, plan.False}, {0, 2, plan.Unknown, plan.True, plan.False},
		{1, 0, plan.False, plan.True, plan.True}, {1, 1, plan.False, plan.False, plan.True}, {1, 2, plan.False, plan.Unknown, plan.True},
		{2, 0, plan.Unknown, plan.True, plan.Unknown}, {2, 1, plan.False, plan.Unknown, plan.Unknown}, {2, 2, plan.Unknown, plan.Unknown, plan.Unknown},
	}
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	rawParam := func(n int) parser.Expr {
		return parser.ParameterExpr{Number: n, Pos: parser.Position{Character: n + 7}}
	}
	nodes := make([]expr.Expr, 3)
	for i, op := range []parser.LogicalOperator{parser.LogicalAnd, parser.LogicalOr, parser.LogicalNot} {
		raw := parser.LogicalExpr{Op: op, Left: rawParam(1), Pos: parser.Position{Character: 10}}
		if op != parser.LogicalNot {
			raw.Right = rawParam(2)
		}
		var err error
		nodes[i], err = binder.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if nodes[i].ResultUsage() != typ.Usage() || !nodes[i].Properties().ParameterDependent || nodes[i].Properties().RowDependent {
			t.Fatal(nodes[i])
		}
	}
	info, err := binder.finish()
	if err != nil || info.Count != 2 || !reflect.DeepEqual(info.Types, []sqltype.Usage{typ.Usage(), typ.Usage()}) {
		t.Fatal(info, err)
	}
	check := func(tc struct {
		a, b         int
		and, or, not plan.Truth
	}) {
		for i, want := range []plan.Truth{tc.and, tc.or, tc.not} {
			v, err := nodes[i].Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background(), Parameters: []sqltype.Value{values[tc.a], values[tc.b]}}})
			if err != nil || v.Type() != typ || v.IsNull() != (want == plan.Unknown) {
				t.Error(v, err, want)
				continue
			}
			if want != plan.Unknown {
				actual, err := v.Boolean()
				if err != nil || actual != (want == plan.True) {
					t.Error(actual, err, want)
				}
			}
		}
	}
	for _, tc := range cases {
		check(tc)
	}
	var wg sync.WaitGroup
	for _, tc := range cases {
		wg.Go(func() { check(tc) })
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := nodes[0].Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := nodes[0].Eval((*EvalContext)(nil)); err == nil {
		t.Fatal("missing context")
	}
	if _, err := nodes[0].Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}); err == nil {
		t.Fatal("missing parameter frame")
	}
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	if _, err := nodes[0].Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background(), Parameters: []sqltype.Value{sqltype.Null(integer), values[0]}}}); err == nil {
		t.Fatal("wrong typed frame")
	}
}

func TestLogicalBindingDiagnosticsAndInvariants(t *testing.T) {
	e := New()
	pos := parser.Position{Character: 17}
	literal := func(kind parser.ValueKind) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: kind, Integer: 1, Boolean: true, Pos: pos}}
	}
	for _, op := range []parser.LogicalOperator{parser.LogicalAnd, parser.LogicalOr, parser.LogicalNot} {
		construct := map[parser.LogicalOperator]string{parser.LogicalAnd: "AND", parser.LogicalOr: "OR", parser.LogicalNot: "NOT"}[op]
		for _, exprContext := range []expressionContext{projectionContext, filterContext} {
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			raw := parser.LogicalExpr{Op: op, Left: literal(parser.IntegerValue), Pos: parser.Position{Character: 8}}
			if op != parser.LogicalNot {
				raw.Right = literal(parser.BooleanValue)
			}
			node, err := b.bind(raw, expressionPolicy{context: exprContext})
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "argument of " + construct + " must be type boolean, not type integer", Position: "17"}
			if node != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(node, err, diagnostic, want)
			}
		}
	}
	for _, raw := range []parser.LogicalExpr{
		{Op: parser.LogicalAnd, Left: literal(parser.BooleanValue)},
		{Op: parser.LogicalNot, Left: literal(parser.BooleanValue), Right: literal(parser.BooleanValue)},
		{Op: parser.LogicalOperator(99), Left: literal(parser.BooleanValue)},
		{Op: parser.LogicalNot},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		if _, err := b.bind(raw, expressionPolicy{context: projectionContext}); err == nil {
			t.Fatal("invalid raw logical node", raw)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	_, err := b.bind(parser.ParameterExpr{Number: 1, Pos: pos}, expressionPolicy{context: projectionContext, expected: b.typ(sqltype.Int4).Usage()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.bindBoolean(parser.ParameterExpr{Number: 1, Pos: pos}, projectionContext, "AND")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" || diagnostic.Message != "argument of AND must be type boolean, not type integer" || diagnostic.Position != "17" {
		t.Fatal(err)
	}
	// Stage dependence is preserved from each child without any row-time allocation.
	typ := b.typ(sqltype.Bool)
	child := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{StageDependent: true}}, Value: sqltype.Boolean(typ, true)}
	invalid := plan.LogicalExpr{Info: expr.Info{Usage: typ.Usage()}, Op: parser.LogicalOperator(99), Left: child, Right: child}
	if _, err := invalid.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}); err == nil {
		t.Fatal("invalid bound operator")
	}
}

func TestLogicalDependenciesMetadataAndAuthorization(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT,a BOOL,b BOOL)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	ast := statement(t, "SELECT FALSE AND a,TRUE OR b,$1 AND a,TRUE OR pg_type_is_visible(id) FROM t WHERE NOT id=1")
	ref, err := e.resolveReference(auth, ast.(*parser.Select).From.(*parser.TableReference).Name)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	bound, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if bound.parameters.Count != 1 || bound.parameters.Types[0] != eType(t, e, sqltype.Bool).Usage() {
		t.Fatal(bound.parameters)
	}
	project := bound.query.Root.(*plan.ProjectNode)
	for i, node := range project.Expressions {
		p := node.Properties()
		volatility := expr.Immutable
		if i == 3 {
			volatility = expr.Stable
		}
		if !p.RowDependent || p.ParameterDependent != (i == 2) || p.Volatility != volatility {
			t.Fatal(p)
		}
		if node.ResultUsage().Type.ID() != sqltype.Bool || node.ResultUsage().Typmod != -1 {
			t.Fatal(node.ResultUsage())
		}
	}
	for _, column := range project.Schema() {
		if column.Name != "?column?" || column.Usage != eType(t, e, sqltype.Bool).Usage() || column.OriginTable != 0 || column.OriginAttribute != 0 || column.NullabilityKnown {
			t.Fatal("logical computed schema", column)
		}
	}
	refs := map[ids.ObjectRef]bool{}
	for _, dependency := range bound.dependencies {
		refs[dependency.Ref] = true
	}
	for _, sub := range []int32{1, 2, 3} {
		column := ref
		column.Address.SubID = sub
		if !refs[column] {
			t.Fatal("logical dependency omitted", sub, bound.dependencies)
		}
	}
	// The same actual-use authorization applies to decisive logical branches.
	aclEngine := New()
	owner, finish := roleOperation(t, aclEngine, "postgres")
	defer finish()
	roleExecute(t, aclEngine, owner, "CREATE USER reader")
	roleExecute(t, aclEngine, owner, "CREATE TABLE secret(a BOOL,b BOOL)")
	roleExecute(t, aclEngine, owner, "INSERT INTO secret VALUES(TRUE,TRUE)")
	column := roleTableRef(t, aclEngine, "secret")
	column.Address.SubID = 1
	reader, _ := aclEngine.Roles().Lookup("reader")
	if err := aclEngine.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: aclEngine.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, aclEngine, owner, "SET SESSION AUTHORIZATION reader")
	for _, q := range []string{"SELECT FALSE AND a FROM secret", "SELECT TRUE OR a FROM secret", "SELECT TRUE FROM secret WHERE FALSE AND a", "SELECT TRUE OR num_nulls(a)>0 FROM secret"} {
		r := roleExecute(t, aclEngine, owner, q)
		if r.Rows != nil {
			r.Rows.Close()
		}
	}
	for _, q := range []string{"SELECT FALSE AND b FROM secret", "SELECT TRUE OR b FROM secret", "SELECT TRUE FROM secret WHERE TRUE OR b", "SELECT TRUE OR num_nulls(b)>0 FROM secret"} {
		roleError(t, aclEngine, owner, q, "42501", "permission denied for table secret", "")
	}
}

func eType(t *testing.T, e *Engine, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	typ, ok := e.CatalogRead().LookupType(id)
	if !ok {
		t.Fatal(id)
	}
	return typ
}
