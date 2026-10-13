package engine

import (
	"context"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestEnumPreparedLiteralLifetime(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE mood AS ENUM ('z','q')"))
	old := domainUnitType(t, e.CatalogRead(), "mood")
	description, err := e.Describe(op, statement(t, "SELECT 'q'::mood AS v"))
	if err != nil {
		t.Fatal(err)
	}
	if len(description.temporals) != 1 {
		t.Fatal("enum literal did not enter immutable prepared analysis", description.temporals)
	}
	capture := description.temporals[0]
	member, _ := capture.value.EnumMemberID()
	members := old.typ.EnumMembers()
	members[1].Label = "end"
	typ, err := sqltype.EnumWithMembers(old.typ.ID(), old.Name(), old.typ.SQLName(), members)
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.typ = typ
	next.revision++
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err = e.reownStagedTypeGenerationsLocked(stage)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, &description)
	b.queryAuth = &auth
	value, failure := b.inputConstant("q", typ.Usage(), parser.Position{Offset: capture.offset, Character: 8})
	e.mu.Unlock()
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
	stale.catalogEpoch++
	e.mu.Lock()
	b = newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, &stale)
	b.queryAuth = &auth
	_, failure = b.inputConstant("q", typ.Usage(), parser.Position{Offset: capture.offset, Character: 8})
	e.mu.Unlock()
	if d, ok := failure.(*sqlstate.Error); !ok || d.Code != "0A000" || d.Message != "cached plan must not change result type" {
		t.Fatal("replacement literal bypassed signature fence", failure)
	}
}

func TestEnumPreparedParameterPrivateGeneration(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE mood AS ENUM ('old')"))
	old := domainUnitType(t, e.CatalogRead(), "mood")
	description, err := e.Describe(op, statement(t, "SELECT $1::mood AS v"))
	if err != nil {
		t.Fatal(err)
	}
	typ, err := sqltype.EnumWithMembers(old.typ.ID(), old.Name(), old.typ.SQLName(),
		append(old.typ.EnumMembers(), sqltype.EnumMember{ID: 100000, Label: "pending", SortOrder: 2}))
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.typ = typ
	next.revision++
	stage := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	auth.catalog = e.stageRead(stage)
	auth.roles.catalog = auth.catalog
	b := newExpressionBinder(op.ctx, e, sqltype.InputEnvironment{}, op, &description)
	b.queryAuth = &auth
	node, failure := b.bind(parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 8}}, expressionPolicy{context: projectionContext, expected: typ.Usage()})
	e.mu.Unlock()
	if err != nil || failure != nil {
		t.Fatal(err, failure)
	}
	if node.ResultUsage().Type != typ || b.parameters[1].Type != typ || description.Parameters.Types[0].Type != old.typ || domainUnitType(t, e.CatalogRead(), "mood") != old {
		t.Fatal("private parameter refresh lost codec, immutable contract or published root")
	}
	stale := description
	stale.catalogEpoch++
	e.mu.Lock()
	b = newExpressionBinder(op.ctx, e, sqltype.InputEnvironment{}, op, &stale)
	b.queryAuth = &auth
	_, failure = b.bind(parser.ParameterExpr{Number: 1, Pos: parser.Position{Character: 8}}, expressionPolicy{context: projectionContext, expected: typ.Usage()})
	e.mu.Unlock()
	if d, ok := failure.(*sqlstate.Error); !ok || d.Code != "0A000" || d.Message != "cached plan must not change parameter type" {
		t.Fatal("private refresh bypassed lifetime fence", failure)
	}
}
