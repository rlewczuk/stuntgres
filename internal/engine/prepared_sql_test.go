package engine

import (
	"context"
	"errors"
	"testing"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPreparedParameterFormalSeed(t *testing.T) {
	e := New()
	op, done, err := testOperation(e, context.Background())
	if err != nil { t.Fatal(err) }
	defer done()
	integer, text := testUsage(e, sqltype.Int4), testUsage(e, sqltype.Text)
	wide := testUsage(e, sqltype.Int8)
	e.mu.Lock()
	defer e.mu.Unlock()
	b := newExpressionBinder(op.ctx, e, sqltype.InputEnvironment{}, op, nil)
	seed := []sqltype.Usage{integer, text}
	b.seedParameters(seed)
	node, err := b.bind(parser.ParameterExpr{Number:1, Pos:parser.Position{Character:17}}, expressionPolicy{context:functionArgumentContext, expected:wide, mode:sqltype.Assignment})
	if err != nil { t.Fatal(err) }
	conversion, ok := node.(coercionExpression)
	if !ok || conversion.child.ResultUsage().Type != integer.Type { t.Fatalf("formal source replaced by destination: %T", node) }
	info, err := b.finish()
	if err != nil || info.Count != 2 || info.Types[0] != integer || info.Types[1] != text { t.Fatal(info, err) }
	if seed[0] != integer || seed[1] != text { t.Fatal("mutated formal contract") }
	for _, number := range []int{2, 2147483647} {
		b := newExpressionBinder(op.ctx, e, sqltype.InputEnvironment{}, op, nil)
		if _, err := b.bind(parser.ParameterExpr{Number:number}, expressionPolicy{context:projectionContext, expected:integer}); err != nil { t.Fatal(err) }
		_, err := b.finish()
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42P18" || d.Message != "could not determine data type of parameter $1" || d.Position != "" { t.Fatal(err) }
		if len(b.parameters) != 1 { t.Fatal("sparse index allocated intermediate slots") }
	}
	b = newExpressionBinder(op.ctx, e, sqltype.InputEnvironment{}, op, nil)
	b.seedParameters([]sqltype.Usage{{}})
	if _, err := b.finish(); err == nil { t.Fatal("unused unknown declaration resolved") }
}

func TestPreparedSQLSessionOwner(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	r := a.preparedSQL
	if r == nil || a.resources[PreparedSQL] == nil || a.resources[PreparedSQL].owner != r || r.token != a.token { t.Fatal("missing attached session owner") }
	entry := &sqlPreparedStatement{body:&parser.Select{}}
	r.entries["p"] = entry
	if err := r.EndSession(b.token, CleanClose); err == nil || r.entries["p"] != entry { t.Fatal("foreign end mutated registry", err) }
	if err := r.EndSession(a.token, InitFailure); err != nil || len(r.entries) != 0 { t.Fatal("end did not clear definitions", err) }
	if a.resources[PreparedSQL].owner != r { t.Fatal("clearing detached owner") }
	r.entries["p"] = entry
	a.RequestEnd(CleanClose)
	if err := a.WaitEnd(context.Background()); err != nil || len(r.entries) != 0 { t.Fatal("physical cleanup retained definition", err) }
	if b.preparedSQL == r { t.Fatal("peer session shared owner") }
}

func TestPreparedSQLCanceledPublication(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	ctx, cancel := context.WithCancel(context.Background())
	op, err := session.Acquire(ctx)
	if err != nil { t.Fatal(err) }
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	raw := &parser.DeallocateSQL{All:true}
	if err := scope.gate(raw); err != nil { t.Fatal(err) }
	entry := &sqlPreparedStatement{body:&parser.Select{}}
	session.preparedSQL.entries["p"] = entry
	cancel()
	_, err = scope.executeDeallocateSQL(raw, 0)
	if !errors.Is(err, context.Canceled) || session.preparedSQL.entries["p"] != entry || scope.completed { t.Fatal("canceled publication mutated owner", err) }
	if finish := scope.Finish(err); !errors.Is(finish, context.Canceled) { t.Fatal(finish) }
}

func TestPreparedSQLSameScopeAndFreshFrames(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	body := &parser.Select{Targets:[]parser.Expr{parser.ParameterExpr{Number:1}}}
	op, err := session.Acquire(context.Background())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	// Describe a raw body through the existing scope; this unit checks machinery,
	// not a second set of public SQL assertions.
	scope.initialParameters = []sqltype.Usage{testUsage(e, sqltype.Int4)}
	description, err := scope.Describe(body)
	if err != nil { _ = scope.Finish(err); t.Fatal(err) }
	if err := scope.Finish(nil); err != nil { t.Fatal(err) }
	session.preparedSQL.entries["p"] = &sqlPreparedStatement{body:body, description:description}
	for _, n := range []int64{7,8} {
		scope, err := e.StartStatement(op)
		if err != nil { t.Fatal(err) }
		actual := parser.LiteralExpr{Value:parser.Value{Kind:parser.IntegerValue, Integer:n}}
		result, err := scope.Execute(&parser.ExecuteSQL{Name:parser.Identifier{Text:"p"}, Arguments:[]parser.Expr{actual}}, 0, nil)
		if err != nil { _ = scope.Finish(err); t.Fatal(err) }
		if !scope.replacementRead || scope.Expected != &session.preparedSQL.entries["p"].description || scope.owner != session.transactions { t.Fatal("wrapper escaped effective body admission") }
		if result.Rows == nil { t.Fatal("missing body result") }
		values := tuples(result)
		if len(values) != 1 || len(values[0]) != 1 || values[0][0] != n {
			t.Fatal("execution-private frame leaked a prior value", values, n)
		}
		result.Rows.Close()
		if err := scope.Finish(nil); err != nil { t.Fatal(err) }
		if session.transactions.state != TxIdle { t.Fatal("nested or unfinished transaction") }
	}
}
