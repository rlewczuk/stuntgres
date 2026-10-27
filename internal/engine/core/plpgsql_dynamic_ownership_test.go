package core_test

import (
	"reflect"
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPLpgSQLDynamicSQLOwnership(t *testing.T) {
	e := rootengine.New()
	owner := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, "CREATE FUNCTION pg_catalog.ds_ownership(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN EXECUTE 'SELECT $1::integer' INTO x USING x; RETURN x; END$$")
	var declaration *catalog.Procedure
	for candidate := range catalog.DefinitionProceduresNamedAt(e.CatalogRead(), nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "ds_ownership") {
		declaration = candidate
	}
	if declaration == nil {
		t.Fatal("missing declaration")
	}
	program := declaration.SQL.PL
	formals := append([]proc.Formal(nil), declaration.SQL.Formals...)
	syntax := declaration.SQL.PLSyntax
	instruction := program.Root.Statements[0].Block.Statements[0].Dynamic
	cycle := owner.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, owner, cycle, fn) }
	command(func(s *core.StatementScope) error {
		_, err := s.ParseProtocol("dsownership", "SELECT pg_catalog.ds_ownership(COALESCE(-(7::integer),0)) AS v", nil)
		return err
	})
	registry := core.PayloadOf(owner).PreparedSQL
	definition := registry.Lookup("dsownership")
	analysis := definition.Description.Analysis
	if analysis == nil || analysis.FoldCallables[declaration.Address()] != declaration || registry.Binding("dsportal") != nil {
		t.Fatal("prepare retained activation or wrong declaration")
	}
	command(func(s *core.StatementScope) error {
		_, err := s.BindProtocolPortal("dsportal", "dsownership", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	accepted := registry.Binding("dsportal")
	if accepted == nil || accepted.Resource == nil || accepted.Resource.Root == nil || accepted.Folds == nil || accepted.Entry.Body != definition.Body {
		t.Fatal("missing composed accepted owner")
	}
	var inspect func(expr.Expr)
	inspect = func(node expr.Expr) {
		switch n := node.(type) {
		case plan.PLLocalExpr:
			t.Fatal("retained invocation lexical frame", n)
		case plan.SQLRoutineExpr:
			if n.Procedure == declaration && n.Procedure.SQL.PL != program {
				t.Fatal("replaced immutable PL program")
			}
		}
		plan.ExpressionChildren(node, inspect)
	}
	for _, node := range accepted.Resource.Root.Expressions {
		inspect(node)
	}
	command(func(s *core.StatementScope) error {
		_, err := s.ExecuteProtocolPortal("dsportal", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	if declaration.SQL.PL != program || declaration.SQL.PLSyntax != syntax || program.Root.Statements[0].Block.Statements[0].Dynamic != instruction || !reflect.DeepEqual(formals, declaration.SQL.Formals) || program.ValidateDefinition(declaration.SQL) != nil || registry.Lookup("dsownership") != definition {
		t.Fatal("execution published computed syntax/value/plan")
	}
	before := snapshotFoldDescription(definition.Description)
	oldAccepted := *accepted.Folds
	body, oldAnalysis, root := accepted.Entry.Body, accepted.Entry.Description.Analysis, accepted.Resource.Root
	sqlExecutionPreparedGenerations(t, e, owner, definition, accepted, "dsownership", "dsportal")
	enginetest.SessionSQL(t, owner, "CREATE OR REPLACE FUNCTION pg_catalog.ds_ownership(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN EXECUTE 'SELECT $1::integer+1' INTO x USING x; RETURN x; END$$")
	if preparedRebindValid(t, e, owner, analysis) {
		t.Fatal("changed dynamic definition remained valid")
	}
	if accepted.Entry.Body != body || accepted.Entry.Description.Analysis != oldAnalysis || accepted.Resource.Root != root || accepted.Folds.Generation != oldAccepted.Generation || &accepted.Folds.Entries[0] != &oldAccepted.Entries[0] || &definition.Description.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || declaration.SQL.PL != program {
		t.Fatal("replacement mutated prior owner/backing")
	}
	ast, description := sequencePreparedDescription(t, e, owner, "SELECT pg_catalog.ds_ownership(COALESCE(-(7::integer),0)) AS v")
	preparedFoldExecute(t, e, owner, ast, &description)
	beforeRejection := snapshotFoldDescription(description)
	saved := description.Results
	description.Results = nil
	if result, err := sequencePreparedExecute(e, owner, ast, &description); err == nil {
		if result.Rows != nil {
			result.Rows.Close()
		}
		t.Fatal("accepted forged result publication")
	}
	description.Results = saved
	if description.Analysis != beforeRejection.Analysis || description.Folds != beforeRejection.Folds || description.Folds.Current().Generation != beforeRejection.Accepted.Generation || &description.Folds.Current().Entries[0] != &beforeRejection.Accepted.Entries[0] || declaration.SQL.PL != program || program.ValidateDefinition(declaration.SQL) != nil {
		t.Fatal("rejected publication changed source or accepted backing")
	}
	// The changed command returns -6; old accepted fold evidence remains -7.
	preparedFoldEntry(t, before, sqltype.Int4, "-7")
	command(func(s *core.StatementScope) error { return s.CloseProtocolPortal("dsportal") })
	command(func(s *core.StatementScope) error { return s.CloseProtocolStatement("dsownership") })
	if registry.Binding("dsportal") != nil || registry.Lookup("dsownership") != nil {
		t.Fatal("closed owner retained resources")
	}
}
