package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestXML2XSLTOwnership(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	err := enginetest.ExtensionUnitExecute(t, e, op, &parser.CreateExtension{Name: parser.Identifier{Text: "xml2"}})
	done()
	if err != nil {
		t.Fatal(err)
	}
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	const style = `<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:output method="text" encoding="UTF-16LE"/><xsl:template match="/"><xsl:value-of select="/r"/></xsl:template></xsl:stylesheet>`
	const query = "SELECT xslt_process('<r>あé</r>','" + style + "'),xslt_process('<r>あé</r>','" + style + "','')"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("xslt_owned", query, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("xslt_owned")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing real prepared definition")
	}
	analysis, owner := definition.Description.Analysis, definition.Description.Folds
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "xml2"}))
	immutable, volatile := view.MustProcedure(ext.Members[11]), view.MustProcedure(ext.Members[10])
	if analysis.Domain != view.Domain() || analysis.Epoch != view.Epoch() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 || analysis.FoldCallables[immutable.Address()] != immutable || analysis.FoldCallables[volatile.Address()] != volatile {
		t.Fatal("parse/describe lost callable ownership or evaluated XSLT")
	}
	bind := func(name string) *core.PreparedBinding {
		t.Helper()
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "xslt_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		b := registry.Binding(name)
		if b == nil || b.Resource == nil || b.Resource.Root == nil || b.Folds == nil || b.Entry.Description.Analysis != analysis || b.Entry.Description.Folds != owner {
			t.Fatal("missing accepted portal")
		}
		return b
	}
	first, second := bind("xslt_first"), bind("xslt_second")
	accepted := owner.Current()
	if accepted.Generation == 0 || len(accepted.Entries) != 1 {
		t.Fatal("only the two-argument overload may fold", accepted)
	}
	entry := accepted.Entries[0]
	text, textErr := entry.Value.Text()
	if entry.Callable != immutable || entry.Source == (expr.FoldSourceOccurrence{}) || entry.Occurrence == 0 || len(entry.Inputs) != 2 || entry.Result.Type.ID() != sqltype.Text || textErr != nil || text != "\x42\x30\xe9\x00" {
		t.Fatal("complete owned fold datum/provenance", entry, textErr)
	}
	for _, input := range entry.Inputs {
		if input.Usage.Type != analysis.FoldTypes[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 25}] || input.Value.Type() != input.Usage.Type {
			t.Fatal("fold operand backing generation")
		}
	}
	firstRoot, secondRoot := first.Resource.Root, second.Resource.Root
	firstFolds, secondFolds := *first.Folds, *second.Folds
	if first.Resource == second.Resource || firstRoot == secondRoot || first.Folds == second.Folds {
		t.Fatal("mutable portal state shared")
	}
	assert := func(root *plan.ProjectNode, folds exec.AcceptedFolds) {
		t.Helper()
		if len(root.Expressions) != 2 || folds.Generation != accepted.Generation || len(folds.Entries) != 1 || &folds.Entries[0] != &accepted.Entries[0] || !enginetest.SamePublishedFold(folds.Entries[0], entry) || !sqltype.SameDatum(folds.Entries[0].Value, entry.Value) {
			t.Fatal("accepted graph/fold backing changed")
		}
		literal, ok := root.Expressions[0].(plan.LiteralExpr)
		if !ok || literal.RoutineReference != nil || literal.Source != entry.Source || literal.Occurrence != entry.Occurrence || !sqltype.SameDatum(literal.Value, entry.Value) {
			t.Fatal("graph lost immutable XSLT datum")
		}
		call, ok := root.Expressions[1].(plan.InstalledXML2Expr)
		if !ok || call.Procedure != volatile || call.RoutineReference != volatile || call.Procedure.Signature != "25,25,25" || call.Operation != functions.XML2XSLT || call.Ref.Address != volatile.Address() || call.IntrinsicVolatility() != expr.Volatile || len(call.Args) != 3 {
			t.Fatal("volatile overload lost selected recipe")
		}
	}
	assert(firstRoot, firstFolds)
	assert(secondRoot, secondFolds)
	ctx, cancel := context.WithCancel(context.Background())
	operation, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(operation)
	if err != nil {
		operation.Done()
		t.Fatal(err)
	}
	local := enginetest.CloneFoldDescription(t, definition.Description)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(operation)
	if err != nil {
		e.Mu.Unlock()
		_ = scope.Finish(err)
		operation.Done()
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		e.Mu.Unlock()
		_ = scope.Finish(err)
		operation.Done()
		t.Fatal(err)
	}
	publication, err := core.BeginFoldPublication(&local, definition.Body, environment, auth.Catalog, nil)
	if err != nil || publication == nil {
		e.Mu.Unlock()
		_ = scope.Finish(err)
		operation.Done()
		t.Fatal("real publication", err)
	}
	publication.Group.Entries = slices.Clone(publication.Group.Entries)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(entry.Result.Type)
	cancel()
	err = publication.Accept(operation.Ctx, auth.Catalog, environment, view.Epoch())
	e.Mu.Unlock()
	_ = scope.Finish(err)
	operation.Done()
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed || registry.Lookup("xslt_owned") != definition || definition.Description.Analysis != analysis || definition.Description.Folds != owner {
		t.Fatal("rejected publication changed accepted ownership", err)
	}
	assert(firstRoot, owner.Current())
	assert(secondRoot, *second.Folds)
	firstResource, secondResource := first.Resource, second.Resource
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("xslt_owned") })
	for _, name := range []string{"xslt_first", "xslt_second"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	if owner.Live() || !first.Retired || !second.Retired || first.Folds != nil || second.Folds != nil || firstResource.Root != nil || secondResource.Root != nil {
		t.Fatal("prepared resources retained mutable execution state")
	}
	assert(firstRoot, firstFolds)
	assert(secondRoot, secondFolds)
	operation, _, err = s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(operation, cycle)
	operation.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
