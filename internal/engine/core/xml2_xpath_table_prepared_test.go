package core_test

import (
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
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestXML2XPathTablePreparedOwnership(t *testing.T) {
	for _, failedExecution := range []bool{false, true} {
		name := "retired_definition"
		if failedExecution {
			name = "failed_execution"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE EXTENSION xml2")
			enginetest.SessionSQL(t, s, "BEGIN")
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("xt_definition", "SELECT * FROM xpath_table('id','doc','\"xt_absent'::text || COALESCE(-(7::integer),0)::text || '\"','/doc/a','true') AS x(id integer,a integer)", nil)
				return err
			})
			registry := core.PayloadOf(s).PreparedSQL
			definition := registry.Lookup("xt_definition")
			if definition == nil || definition.Description.Folds == nil {
				t.Fatal("missing actual prepared definition")
			}
			owner := definition.Description.Folds
			if owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
				t.Fatal("Parse evaluated dynamic source children")
			}
			command(func(scope *core.StatementScope) error {
				_, err := scope.BindProtocolPortal("xt_binding", "xt_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
				return err
			})
			binding := registry.Binding("xt_binding")
			if binding == nil || binding.Entry.Description.Folds != owner || binding.Resource == nil || binding.Resource.Root == nil {
				t.Fatal("missing actual binding graph")
			}
			graph := binding.Resource.Root
			accepted := owner.Current()
			if len(accepted.Entries) == 0 || binding.Folds == nil || binding.Folds.Generation != accepted.Generation || &binding.Folds.Entries[0] != &accepted.Entries[0] {
				t.Fatal("binding did not share accepted immutable backing")
			}
			child := enginetest.PreparedFoldEntry(t, definition.Description, sqltype.Int4, "-7")
			provider, ok := child.Callable.(*catalog.Procedure)
			if !ok || provider.Volatility != expr.Immutable || child.Source == (expr.FoldSourceOccurrence{}) || child.Frame != child.Source.Frame || len(child.Inputs) == 0 {
				t.Fatal("missing genuine immutable source-child provenance")
			}
			if _, err := exec.ValidateStoredRoutineDescriptor(e.CatalogRead(), provider); err != nil {
				t.Fatal(err)
			}
			inputSeven := false
			for _, input := range child.Inputs {
				if input.Value.Type() != nil && input.Value.Type().ID() == sqltype.Int4 {
					value, err := input.Value.Integer()
					if err == nil && value == 7 {
						inputSeven = true
					}
				}
			}
			if !inputSeven {
				t.Fatal("actual accepted unary child lost typed input seven")
			}
			childIndex := -1
			for i, entry := range accepted.Entries {
				if enginetest.SamePublishedFold(entry, child) {
					childIndex = i
					break
				}
			}
			if childIndex < 0 || &binding.Folds.Entries[childIndex].Inputs[0] != &child.Inputs[0] {
				t.Fatal("binding lost actual child input backing")
			}
			inputs := append([]exec.FoldInput(nil), child.Inputs...)
			var captured *plan.InstalledXML2XPathTableExpr
			plan.VisitExecutableQuery(graph, func(plan.Node) {}, func(node expr.Expr) {
				plan.WalkExpression(node, func(child expr.Expr) {
					if call, ok := child.(plan.InstalledXML2XPathTableExpr); ok {
						copy := call
						captured = &copy
					}
				})
			})
			if captured == nil {
				t.Fatal("actual accepted graph lost installed table")
			}
			if err := stored.ValidateInstalledXML2XPathTable(*captured, e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			children := append([]expr.Expr(nil), captured.Args...)
			assertCapture := func() {
				t.Helper()
				if !reflect.DeepEqual(children, captured.Args) || captured.RoutineReference != captured.Procedure || !enginetest.SamePublishedFold(accepted.Entries[childIndex], child) || &accepted.Entries[childIndex].Inputs[0] != &child.Inputs[0] || !reflect.DeepEqual(child.Inputs, inputs) {
					t.Fatal("terminal work mutated saved accepted graph/backing")
				}
			}
			if failedExecution {
				err := enginetest.PreparedFoldProtocolTry(t, e, s, cycle, false, func(scope *core.StatementScope) error {
					_, err := scope.ExecuteProtocolPortal("xt_binding", 0, func([]session.ProtocolField) error { t.Fatal("failing source published a row"); return nil }, nil, nil, core.CopyProtocolHooks{})
					return err
				})
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42P01" {
					t.Fatal("actual source failure", err)
				}
				assertCapture()
				enginetest.SessionSQL(t, s, "ROLLBACK")
				return
			}
			var publication *core.FoldPublication
			op, err := s.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err == nil {
				var environment core.PreparedEnvironment
				environment, err = e.PreparedEnvironmentLocked(auth, true)
				if err == nil {
					publication, err = core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
				}
			}
			e.Mu.Unlock()
			op.Done()
			if err != nil || publication == nil {
				t.Fatal("actual next publication", err)
			}
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("xt_definition") })
			if owner.Live() || binding.Retired || binding.Resource.Root != graph {
				t.Fatal("definition retirement changed retained binding")
			}
			retired := owner.Current()
			op, err = s.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err = e.AuthorizationLocked(op)
			if err == nil {
				var environment core.PreparedEnvironment
				environment, err = e.PreparedEnvironmentLocked(auth, true)
				if err == nil {
					err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
				}
			}
			e.Mu.Unlock()
			op.Done()
			after := owner.Current()
			if !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed || after.Generation != retired.Generation || binding.Resource.Root != graph || binding.Folds.Generation != accepted.Generation || &binding.Folds.Entries[0] != &accepted.Entries[0] {
				t.Fatal("rejected publication changed retired owner or accepted graph", err)
			}
			assertCapture()
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("xt_binding") })
			if !binding.Retired {
				t.Fatal("portal close retained resource")
			}
			assertCapture()
			enginetest.SessionSQL(t, s, "ROLLBACK")
		})
	}
}
