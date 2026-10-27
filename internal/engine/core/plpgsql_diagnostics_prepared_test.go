package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
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
)

func TestPLpgSQLDiagnosticsPreparedOwnership(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, `CREATE FUNCTION pg_catalog.diagnostics_owner(n integer) RETURNS bigint LANGUAGE plpgsql AS $$DECLARE c bigint; o oid; s text; BEGIN IF n>0 THEN PERFORM pg_catalog.diagnostics_owner(n-1); END IF; PERFORM n UNION ALL SELECT NULL; GET DIAGNOSTICS c=ROW_COUNT,o=PG_ROUTINE_OID,s=PG_CONTEXT; RETURN c; END$$`)
	var declaration *catalog.Procedure
	for candidate := range catalog.DefinitionProceduresNamedAt(e.CatalogRead(), nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "diagnostics_owner") {
		declaration = candidate
	}
	if declaration == nil || declaration.RoutineOID() == 0 {
		t.Fatal("missing genuine catalog identity")
	}
	program, syntax := declaration.SQL.PL, declaration.SQL.PLSyntax
	variables := append([]proc.PLVariable(nil), program.Variables...)
	cycle := owner.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, owner, cycle, fn) }
	command(func(s *core.StatementScope) error {
		_, err := s.ParseProtocol("diagnostics_owner", "SELECT pg_catalog.diagnostics_owner(COALESCE(-(7::integer),0)) AS v", nil)
		return err
	})
	registry := core.PayloadOf(owner).PreparedSQL
	definition := registry.Lookup("diagnostics_owner")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Analysis.FoldCallables[declaration.Address()] != declaration {
		t.Fatal("description lost immutable diagnostics declaration")
	}
	if definition.Description.FoldPlanning != nil || definition.Description.RefreshTemporals {
		t.Fatal("published description retained candidate state")
	}
	diagnosticsImmutableRecipe(t, declaration.SQL)
	if registry.Binding("diagnostics_portal") != nil {
		t.Fatal("description retained a portal")
	}
	command(func(s *core.StatementScope) error {
		_, err := s.BindProtocolPortal("diagnostics_portal", "diagnostics_owner", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	accepted := registry.Binding("diagnostics_portal")
	if accepted == nil || accepted.Resource == nil || accepted.Resource.Root == nil || accepted.Folds == nil || accepted.Entry.Body != definition.Body {
		t.Fatal("missing real accepted binding/resource")
	}
	var inspect func(expr.Expr)
	inspect = func(node expr.Expr) {
		switch n := node.(type) {
		case plan.PLLocalExpr:
			t.Fatal("accepted graph retained a lexical frame", n)
		case plan.SQLRoutineExpr:
			if n.Procedure == declaration && n.Procedure.SQL.PL != program {
				t.Fatal("accepted graph replaced immutable program")
			}
		}
		plan.ExpressionChildren(node, inspect)
	}
	for _, node := range accepted.Resource.Root.Expressions {
		inspect(node)
	}
	command(func(s *core.StatementScope) error {
		_, err := s.ExecuteProtocolPortal("diagnostics_portal", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	// Reached recursion and repeated invocation use the same definition; neither
	// invocation may publish a frame, handled record, or live action into it.
	enginetest.SessionSQL(t, owner, "SELECT pg_catalog.diagnostics_owner(2)")
	enginetest.SessionSQL(t, owner, "SELECT pg_catalog.diagnostics_owner(0)")
	if declaration.SQL.PL != program || declaration.SQL.PLSyntax != syntax || !reflect.DeepEqual(variables, program.Variables) || program.ValidateDefinition(declaration.SQL) != nil || registry.Lookup("diagnostics_owner") != definition {
		t.Fatal("invocation mutated retained program/Usage/registry backing")
	}
	before := snapshotFoldDescription(definition.Description)
	bindingFolds := *accepted.Folds
	preparedFoldEntry(t, before, sqltype.Int4, "-7")
	if len(bindingFolds.Entries) == 0 || len(bindingFolds.Dependencies) == 0 || bindingFolds.Generation != before.Accepted.Generation || &bindingFolds.Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("accepted binding lost definition fold generation/backing")
	}
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	func() {
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
		if presentation.Revision() == integer.Revision() || definition.Description.Analysis.Valid(presented, environment) || !definition.Description.Analysis.FoldsValid(presented, environment) {
			t.Fatal("effective presentation revision failed independent analysis/fold fences")
		}
		publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, environment, presented, nil)
		if err != nil || publication == nil {
			t.Fatal("presentation publication", err)
		}
		if err := publication.Accept(op.Ctx, presented, environment, e.Catalog.State.Epoch()); err != nil {
			t.Fatal(err)
		}
		if definition.Description.Folds.Current().Generation != before.Accepted.Generation || &definition.Description.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
			t.Fatal("presentation-only refresh copied executable backing")
		}
		codec := *integer.SQLType
		presentation.SQLType = &codec
		state = catalog.CloneState(auth.Catalog.State())
		state.SetDescriptor(presentation.Address(), &presentation)
		replaced := e.Catalog.Read(state)
		if definition.Description.Analysis.FoldsValid(replaced, environment) || core.FoldDependenciesValid(replaced, environment, bindingFolds.Dependencies) {
			t.Fatal("effective codec generation retained accepted executable authority")
		}
		if p, err := core.BeginFoldPublication(&accepted.Entry.Description, accepted.Entry.Body, environment, replaced, accepted); !errors.Is(err, ids.ErrCatalogReplaced) || p != nil {
			t.Fatal("old binding survived codec replacement", err)
		}
		publication, err = core.BeginFoldPublication(&definition.Description, definition.Body, environment, replaced, nil)
		if err != nil || publication == nil {
			t.Fatal(err)
		}
		retired := definition.Description.Folds.Current()
		if retired.Generation <= before.Accepted.Generation || foldRootsReusable(definition.Description.Folds) || len(publication.Group.Entries) != 0 {
			t.Fatal("codec change failed to retire this definition generation")
		}
		publication.Group.Entries = append([]exec.FoldConstant(nil), before.Accepted.Entries...)
		publication.Group.Dependencies = append([]exec.FoldDependency(nil), before.Accepted.Dependencies...)
		publication.Group.Private, publication.Group.DependenciesPrivate = true, true
		if err := publication.Accept(op.Ctx, replaced, environment, e.Catalog.State.Epoch()); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("stale candidate published", err)
		}
		current := definition.Description.Folds.Current()
		if registry.Lookup("diagnostics_owner") != definition || registry.Binding("diagnostics_portal") != accepted || definition.Description.Analysis != before.Analysis || definition.Description.Folds != before.Folds || current.Generation != retired.Generation || &current.Entries[0] != &before.Accepted.Entries[0] || &current.Dependencies[0] != &before.Accepted.Dependencies[0] || foldRootsReusable(definition.Description.Folds) {
			t.Fatal("rejected publication changed original owner/backing/generation")
		}
		if accepted.Folds.Generation != bindingFolds.Generation || &accepted.Folds.Entries[0] != &bindingFolds.Entries[0] || &accepted.Folds.Dependencies[0] != &bindingFolds.Dependencies[0] {
			t.Fatal("rejection mutated prior accepted binding")
		}
	}()
	if declaration.SQL.PL != program || program.ValidateDefinition(declaration.SQL) != nil || !reflect.DeepEqual(variables, program.Variables) {
		t.Fatal("generation/rejection retained invocation state in declaration")
	}
	diagnosticsImmutableRecipe(t, declaration.SQL)
	command(func(s *core.StatementScope) error { return s.CloseProtocolPortal("diagnostics_portal") })
	command(func(s *core.StatementScope) error { return s.CloseProtocolStatement("diagnostics_owner") })
	if registry.Binding("diagnostics_portal") != nil || registry.Lookup("diagnostics_owner") != nil {
		t.Fatal("closed registry retained resources")
	}
}

// Follow the actual proc/parser recipe graph (including dynamic interfaces), not
// the accepted executable graph. Scalar descriptor/collation internals belong to
// sqltype; recipe functions or runtime owners are never immutable declarations.
func diagnosticsImmutableRecipe(t *testing.T, recipe *proc.Definition) {
	t.Helper()
	type identity struct {
		typ     reflect.Type
		pointer uintptr
	}
	seen := make(map[identity]bool)
	var visit func(reflect.Value)
	visit = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				visit(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			key := identity{v.Type(), v.Pointer()}
			if seen[key] {
				return
			}
			seen[key] = true
			element := v.Type().Elem()
			switch element.PkgPath() {
			case "stuntgres/internal/engine/core", "stuntgres/internal/engine/exec", "stuntgres/internal/engine/plan", "stuntgres/internal/engine/session", "stuntgres/internal/sqlstate":
				t.Fatalf("immutable recipe retained runtime graph/frame/authority/error: %s", v.Type())
			}
			if element.PkgPath() == "stuntgres/internal/engine/proc" || element.PkgPath() == "stuntgres/internal/parser" {
				visit(v.Elem())
			}
		case reflect.Struct:
			pkg := v.Type().PkgPath()
			if pkg != "stuntgres/internal/engine/proc" && pkg != "stuntgres/internal/parser" {
				return
			}
			if v.Type() == reflect.TypeFor[proc.PLFrame]() || v.Type() == reflect.TypeFor[proc.Frame]() || v.Type() == reflect.TypeFor[proc.PLRun]() {
				t.Fatalf("immutable recipe retained invocation %s", v.Type())
			}
			for i := range v.NumField() {
				visit(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := range v.Len() {
				visit(v.Index(i))
			}
		case reflect.Map:
			iter := v.MapRange()
			for iter.Next() {
				visit(iter.Key())
				visit(iter.Value())
			}
		case reflect.Func, reflect.Chan, reflect.UnsafePointer:
			if !v.IsNil() {
				t.Fatalf("immutable recipe retained executable/private capability: %s", v.Type())
			}
		}
	}
	visit(reflect.ValueOf(recipe))
}
