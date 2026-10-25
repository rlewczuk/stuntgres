package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestEnumPreparedLiteralLifetime(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TYPE mood AS ENUM ('z','q')")
	old := domainUnitType(t, e.CatalogRead(), "mood")
	description, err := e.Describe(op, enginetest.Statement(t, "SELECT 'q'::mood AS v"))
	if err != nil {
		t.Fatal(err)
	}
	if len(description.Temporals) != 1 {
		t.Fatal("enum literal did not enter immutable prepared analysis", description.Temporals)
	}
	capture := description.Temporals[0]
	member, _ := capture.Value.EnumMemberID()
	members := old.SQLType.EnumMembers()
	members[1].Label = "end"
	typ, err := sqltype.EnumWithMembers(old.SQLType.ID(), old.Name(), old.SQLType.SQLName(), members)
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.SQLType = typ
	next.ObjectRevision++
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.ReownStagedTypeGenerationsLocked(stage)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, &description)
	b.Query.Auth = &auth
	value, failure := b.InputConstant("q", typ.Usage(), parser.Position{Offset: capture.Offset, Character: 8})
	e.Mu.Unlock()
	if err != nil || failure != nil {
		t.Fatal(err, failure)
	}
	id, _ := value.EnumMemberID()
	if id != member || value.Type() != typ {
		t.Fatal("retained constant re-input or foreign generation", value)
	}
	if text, err := typ.Output(context.Background(), value); err != nil || text != "end" {
		t.Fatal(text, err)
	}
	stale := description
	*core.DescriptionCatalogEpochRef(&stale)++
	e.Mu.Lock()
	b = core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, &stale)
	b.Query.Auth = &auth
	_, failure = b.InputConstant("q", typ.Usage(), parser.Position{Offset: capture.Offset, Character: 8})
	e.Mu.Unlock()
	if d, ok := failure.(*sqlstate.Error); !ok || d.Code != "0A000" || d.Message != "cached plan must not change result type" {
		t.Fatal("replacement literal bypassed signature fence", failure)
	}
}

func TestEnumPreparedParameterPrivateGeneration(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TYPE mood AS ENUM ('old')")
	old := domainUnitType(t, e.CatalogRead(), "mood")
	description, err := e.Describe(op, enginetest.Statement(t, "SELECT $1::mood AS v"))
	if err != nil {
		t.Fatal(err)
	}
	typ, err := sqltype.EnumWithMembers(old.SQLType.ID(), old.Name(), old.SQLType.SQLName(),
		append(old.SQLType.EnumMembers(), sqltype.EnumMember{ID: 100000, Label: "pending", SortOrder: 2}))
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.SQLType = typ
	next.ObjectRevision++
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	auth.Catalog = e.StageRead(stage)
	auth.Recapture(auth.Catalog)
	b := core.NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, &description)
	b.Query.Auth = &auth
	node, failure := b.Bind(parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 8}}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext, Expected: typ.Usage()})
	e.Mu.Unlock()
	if err != nil || failure != nil {
		t.Fatal(err, failure)
	}
	if node.ResultUsage().Type != typ || b.Params.Parameters[1].Type != typ || description.Parameters.Types[0].Type != old.SQLType || domainUnitType(t, e.CatalogRead(), "mood") != old {
		t.Fatal("private parameter refresh lost codec, immutable contract or published root")
	}
	stale := description
	*core.DescriptionCatalogEpochRef(&stale)++
	e.Mu.Lock()
	b = core.NewExpressionBinder(op.Ctx, e, sqltype.InputEnvironment{}, op, &stale)
	b.Query.Auth = &auth
	_, failure = b.Bind(parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 8}}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext, Expected: typ.Usage()})
	e.Mu.Unlock()
	if d, ok := failure.(*sqlstate.Error); !ok || d.Code != "0A000" || d.Message != "cached plan must not change parameter type" {
		t.Fatal("private refresh bypassed lifetime fence", failure)
	}
}
