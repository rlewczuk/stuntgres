package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPreparedParameterFormalSeed(t *testing.T) {
	e := engine.New()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	integer, text := enginetest.Usage(e, sqltype.Int4), enginetest.Usage(e, sqltype.Text)
	wide := enginetest.Usage(e, sqltype.Int8)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	b := core.NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, nil)
	seed := []sqltype.Usage{integer, text}
	b.SeedParameters(seed)
	node, err := b.Bind(parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 17}}, analyze.ExpressionPolicy{Context: analyze.FunctionArgumentContext, Expected: wide, Mode: sqltype.Assignment})
	if err != nil {
		t.Fatal(err)
	}
	conversion, ok := node.(plan.CoercionExpr)
	if !ok || conversion.Child.ResultUsage().Type != integer.Type {
		t.Fatalf("formal source replaced by destination: %T", node)
	}
	info, err := b.Finish()
	if err != nil || info.Count != 2 || info.Types[0] != integer || info.Types[1] != text {
		t.Fatal(info, err)
	}
	if seed[0] != integer || seed[1] != text {
		t.Fatal("mutated formal contract")
	}
	for _, number := range []int{2, 2147483647} {
		b := core.NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, nil)
		if _, err := b.Bind(parser.ParameterExpr{Number: number}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext, Expected: integer}); err != nil {
			t.Fatal(err)
		}
		_, err := b.Finish()
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42P18" || d.Message != "could not determine data type of parameter $1" || d.Position != "" {
			t.Fatal(err)
		}
		if len(b.Params.Parameters) != 1 {
			t.Fatal("sparse index allocated intermediate slots")
		}
	}
	b = core.NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, nil)
	b.SeedParameters([]sqltype.Usage{{}})
	if _, err := b.Finish(); err == nil {
		t.Fatal("unused unknown declaration resolved")
	}
}

func TestPreparedSQLSessionOwner(t *testing.T) {
	e := engine.New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	r := core.PayloadPreparedSQL(core.PayloadOf(a))
	if r == nil || a.Resources[session.PreparedSQL] == nil || a.Resources[session.PreparedSQL].Owner != r || core.PreparedRegistryToken(r) != a.SessionToken {
		t.Fatal("missing attached session owner")
	}
	entry := core.NewPreparedStatement(core.PreparedStatementFields{Body: &parser.Select{}})
	core.PreparedRegistryEntries(r)["p"] = entry
	if err := r.EndSession(b.SessionToken, session.CleanClose); err == nil || core.PreparedRegistryEntries(r)["p"] != entry {
		t.Fatal("foreign end mutated registry", err)
	}
	if err := r.EndSession(a.SessionToken, session.InitFailure); err != nil || len(core.PreparedRegistryEntries(r)) != 0 {
		t.Fatal("end did not clear definitions", err)
	}
	if a.Resources[session.PreparedSQL].Owner != r {
		t.Fatal("clearing detached owner")
	}
	core.PreparedRegistryEntries(r)["p"] = entry
	a.RequestEnd(session.CleanClose)
	if err := a.WaitEnd(context.Background()); err != nil || len(core.PreparedRegistryEntries(r)) != 0 {
		t.Fatal("physical cleanup retained definition", err)
	}
	if core.PayloadPreparedSQL(core.PayloadOf(b)) == r {
		t.Fatal("peer session shared owner")
	}
}

func TestPreparedSQLCanceledPublication(t *testing.T) {
	e := engine.New()
	session := sessionStart(t, e, nil)
	ctx, cancel := context.WithCancel(context.Background())
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	raw := &parser.DeallocateSQL{All: true}
	if err := scope.Gate(raw); err != nil {
		t.Fatal(err)
	}
	entry := core.NewPreparedStatement(core.PreparedStatementFields{Body: &parser.Select{}})
	core.PreparedRegistryEntries(core.PayloadPreparedSQL(core.PayloadOf(session)))["p"] = entry
	cancel()
	_, err = core.ScopeExecuteDeallocateSQL(scope, raw, 0)
	if !errors.Is(err, context.Canceled) || core.PreparedRegistryEntries(core.PayloadPreparedSQL(core.PayloadOf(session)))["p"] != entry || scope.Completed {
		t.Fatal("canceled publication mutated owner", err)
	}
	if finish := scope.Finish(err); !errors.Is(finish, context.Canceled) {
		t.Fatal(finish)
	}
}
