package core_test

import (
	"context"
	"errors"
	"reflect"
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"testing"
)

func sqlExecutionPreparedOwnership(t *testing.T) {
	e := rootengine.New()
	owner := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, "CREATE PROCEDURE pg_catalog.sql_ownership_child(INOUT x integer) LANGUAGE SQL AS 'SELECT x+1'")
	enginetest.SessionSQL(t, owner, "CREATE FUNCTION pg_catalog.sql_ownership_wrapper(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM x; CALL pg_catalog.sql_ownership_child(x); RETURN x; END$$")
	var declaration *catalog.Procedure
	for candidate := range catalog.DefinitionProceduresNamedAt(e.CatalogRead(), nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "sql_ownership_wrapper") {
		declaration = candidate
	}
	if declaration == nil {
		t.Fatal("missing immutable declaration")
	}
	program := declaration.SQL.PL
	formals := append([]proc.Formal(nil), declaration.SQL.Formals...)
	cycle := owner.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, owner, cycle, fn) }
	command(func(s *core.StatementScope) error {
		_, err := s.ParseProtocol("sqlownership", "SELECT pg_catalog.sql_ownership_wrapper(COALESCE(-(7::integer),0)) AS v", nil)
		return err
	})
	registry := core.PayloadOf(owner).PreparedSQL
	definition := registry.Lookup("sqlownership")
	analysis := definition.Description.Analysis
	if analysis == nil || analysis.FoldCallables[declaration.Address()] != declaration {
		t.Fatal("description did not retain original checked declaration")
	}
	// Prepare owns syntax/signature/captures, not an activation or portal graph.
	if registry.Binding("sqlownership_portal") != nil || program != declaration.SQL.PL || program.ValidateDefinition(declaration.SQL) != nil {
		t.Fatal("Prepare installed execution state")
	}
	command(func(s *core.StatementScope) error {
		_, err := s.BindProtocolPortal("sqlownership_portal", "sqlownership", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	accepted := registry.Binding("sqlownership_portal")
	if accepted == nil || accepted.Resource == nil || accepted.Resource.Root == nil || accepted.Entry.Body != definition.Body || accepted.Folds == nil {
		t.Fatal("missing actual accepted graph owner")
	}
	var inspect func(expr.Expr)
	inspect = func(node expr.Expr) {
		switch n := node.(type) {
		case plan.PLLocalExpr:
			t.Fatal("prepared graph retained reached lexical value", n)
		case plan.SQLRoutineExpr:
			if n.Procedure == declaration && (n.Procedure.SQL.PL != program || len(n.Args) != 1 || len(n.InputMap) != 1) {
				t.Fatal("prepared invocation retained output association or competing program")
			}
		}
		plan.ExpressionChildren(node, inspect)
	}
	for _, node := range accepted.Resource.Root.Expressions {
		inspect(node)
	}
	command(func(s *core.StatementScope) error {
		_, err := s.ExecuteProtocolPortal("sqlownership_portal", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	if declaration.SQL.PL != program || !reflect.DeepEqual(formals, declaration.SQL.Formals) || program.ValidateDefinition(declaration.SQL) != nil || registry.Lookup("sqlownership") != definition {
		t.Fatal("execution mutated published backing or replaced definition owner")
	}
	originalFolds := snapshotFoldDescription(definition.Description)
	originalAccepted := *accepted.Folds
	originalBody, originalAnalysis := accepted.Entry.Body, accepted.Entry.Description.Analysis
	originalRoot := accepted.Resource.Root
	sqlExecutionPreparedGenerations(t, e, owner, definition, accepted)
	enginetest.SessionSQL(t, owner, "CREATE OR REPLACE FUNCTION pg_catalog.sql_ownership_wrapper(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM x; CALL pg_catalog.sql_ownership_child(x); PERFORM 1; RETURN x; END$$")
	if preparedRebindValid(t, e, owner, analysis) {
		t.Fatal("changed program failed to invalidate prior declaration capture")
	}
	if accepted.Entry.Body != originalBody || accepted.Entry.Description.Analysis != originalAnalysis || accepted.Resource.Root != originalRoot || accepted.Folds.Generation != originalAccepted.Generation || &accepted.Folds.Entries[0] != &originalAccepted.Entries[0] || &definition.Description.Folds.Current().Entries[0] != &originalFolds.Accepted.Entries[0] || declaration.SQL.PL != program || !reflect.DeepEqual(formals, declaration.SQL.Formals) {
		t.Fatal("replacement mutated old accepted artifacts")
	}
	// A rejected metadata refresh must preserve the description actually used
	// for that attempt, as well as the original immutable body/program.
	ast, description := sequencePreparedDescription(t, e, owner, "SELECT pg_catalog.sql_ownership_wrapper(COALESCE(-(7::integer),0)) AS v")
	preparedFoldExecute(t, e, owner, ast, &description)
	beforeRejection := snapshotFoldDescription(description)
	saved := description.Results
	description.Results = nil
	if result, err := sequencePreparedExecute(e, owner, ast, &description); err == nil {
		if result.Rows != nil {
			result.Rows.Close()
		}
		t.Fatal("invalid public signature was published")
	}
	description.Results = saved
	if description.Analysis != beforeRejection.Analysis || description.Folds != beforeRejection.Folds || description.Folds.Current().Generation != beforeRejection.Accepted.Generation || &description.Folds.Current().Entries[0] != &beforeRejection.Accepted.Entries[0] {
		t.Fatal("rejected refresh changed its affected description or accepted backing")
	}
	preparedFoldEntry(t, beforeRejection, sqltype.Int4, "-7")
	if declaration.SQL.PL != program || program.ValidateDefinition(declaration.SQL) != nil {
		t.Fatal("rejected publication changed original program")
	}
	command(func(s *core.StatementScope) error { return s.CloseProtocolPortal("sqlownership_portal") })
	command(func(s *core.StatementScope) error { return s.CloseProtocolStatement("sqlownership") })
	if registry.Binding("sqlownership_portal") != nil || registry.Lookup("sqlownership") != nil {
		t.Fatal("prepared resources retained after close")
	}
}

// Exercise semantic revisions against the actual registry definition and its
// accepted portal, without publishing a synthetic catalog into the engine.
func sqlExecutionPreparedGenerations(t *testing.T, e *core.Engine, owner *session.Session, definition *core.PreparedDefinition, accepted *core.PreparedBinding) {
	t.Helper()
	description := &definition.Description
	before := snapshotFoldDescription(*description)
	preparedFoldEntry(t, before, sqltype.Int4, "-7")
	if accepted.Folds == nil || len(accepted.Folds.Entries) == 0 || len(accepted.Folds.Dependencies) == 0 || accepted.Entry.Description.Folds != description.Folds {
		t.Fatal("PL SQL/CALL portal lacks composed accepted fold ownership")
	}
	bindingFolds := *accepted.Folds
	if bindingFolds.Generation != before.Accepted.Generation || &bindingFolds.Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("portal did not accept the definition's immutable generation")
	}
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		t.Fatal(err)
	}
	integer := auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
	presentation := *integer
	presentation.ObjectRevision = integer.Revision() + 1
	state := catalog.CloneState(auth.Catalog.State())
	state.SetDescriptor(presentation.Address(), &presentation)
	presented := e.Catalog.Read(state)
	if presentation.Revision() == integer.Revision() || description.Analysis.Valid(presented, environment) || !description.Analysis.FoldsValid(presented, environment) {
		t.Fatal("presentation revision did not distinguish analysis from executable validity")
	}
	publication, err := core.BeginFoldPublication(description, definition.Body, environment, presented, nil)
	if err != nil || publication == nil {
		t.Fatal("presentation publication", err)
	}
	if err := publication.Accept(op.Ctx, presented, environment, e.Catalog.State.Epoch()); err != nil {
		t.Fatal("unchanged codec rejected", err)
	}
	if description.Folds.Current().Generation != before.Accepted.Generation || &description.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || !foldRootsReusable(description.Folds) {
		t.Fatal("presentation-only revision replaced executable backing")
	}
	// The same address/revision with a distinct executable codec must retire
	// this exact definition and reject this exact portal's fixed fold input.
	codec := *integer.SQLType
	presentation.SQLType = &codec
	state = catalog.CloneState(auth.Catalog.State())
	state.SetDescriptor(presentation.Address(), &presentation)
	replaced := e.Catalog.Read(state)
	if description.Analysis.FoldsValid(replaced, environment) || core.FoldDependenciesValid(replaced, environment, bindingFolds.Dependencies) {
		t.Fatal("effective codec replacement left the PL accepted artifact valid")
	}
	if portal, err := core.BeginFoldPublication(&accepted.Entry.Description, accepted.Entry.Body, environment, replaced, accepted); !errors.Is(err, ids.ErrCatalogReplaced) || portal != nil {
		t.Fatal("codec replacement admitted old portal", err)
	}
	publication, err = core.BeginFoldPublication(description, definition.Body, environment, replaced, nil)
	if err != nil || publication == nil {
		t.Fatal("codec invalidation publication", err)
	}
	retired := description.Folds.Current()
	if retired.Generation <= before.Accepted.Generation || foldRootsReusable(description.Folds) || len(publication.Group.Entries) != 0 {
		t.Fatal("codec replacement did not retire demanded root folds")
	}
	// Give the rejected attempt distinct candidate backing. A publisher that
	// incorrectly installs it cannot pass an equality-only value assertion.
	publication.Group.Entries = append([]exec.FoldConstant(nil), before.Accepted.Entries...)
	publication.Group.Dependencies = append([]exec.FoldDependency(nil), before.Accepted.Dependencies...)
	publication.Group.Private, publication.Group.DependenciesPrivate = true, true
	if err := publication.Accept(op.Ctx, replaced, environment, e.Catalog.State.Epoch()); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("stale codec publication was not rejected", err)
	}
	current := description.Folds.Current()
	if registry := core.PayloadOf(owner).PreparedSQL; registry.Lookup("sqlownership") != definition || registry.Binding("sqlownership_portal") != accepted {
		t.Fatal("rejection replaced registry ownership")
	}
	if description.Analysis != before.Analysis || description.Folds != before.Folds || description.Folds.Analysis != before.Analysis || current.Generation != retired.Generation || &current.Entries[0] != &before.Accepted.Entries[0] || &current.Dependencies[0] != &before.Accepted.Dependencies[0] || foldRootsReusable(description.Folds) {
		t.Fatal("rejected publication changed its actual owner or resurrected backing")
	}
	if accepted.Folds.Generation != bindingFolds.Generation || &accepted.Folds.Entries[0] != &bindingFolds.Entries[0] || &accepted.Folds.Dependencies[0] != &bindingFolds.Dependencies[0] {
		t.Fatal("invalidation or rejection mutated prior accepted portal backing")
	}
	preparedFoldEntry(t, before, sqltype.Int4, "-7")
}
