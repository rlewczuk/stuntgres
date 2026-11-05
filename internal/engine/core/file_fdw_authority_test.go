package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestFileFDWValidatorAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, grant, option string
		allowed             bool
	}{
		{"inherited filename", "GRANT pg_read_server_files TO private_middle; GRANT private_middle TO private_actor", "filename", true},
		{"inherited program", "GRANT pg_execute_server_program TO private_middle; GRANT private_middle TO private_actor", "program", true},
		{"noninherited filename", "GRANT pg_read_server_files TO private_actor WITH INHERIT FALSE", "filename", false},
		{"file is not program", "GRANT pg_read_server_files TO private_actor", "program", false},
		{"bootstrap superuser is not actor", "", "filename", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, view, installation := fileFDWInstalled(t)
			enginetest.Run(t, e, "CREATE ROLE private_actor")
			enginetest.Run(t, e, "CREATE ROLE private_middle")
			if tc.grant != "" {
				for grant := range strings.SplitSeq(tc.grant, ";") {
					enginetest.Run(t, e, grant)
				}
			}
			session := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, session, "SET ROLE private_actor")
			scope := pgrowlocksUnitScope(t, e, session)
			validator := view.MustProcedure(installation.Members[1])
			options := []catalog.ForeignOption{{Name: tc.option, Value: "/not-a-real-private-file"}}
			before := slices.Clone(options)
			journal := scope.Catalog
			err := scope.ValidateForeignOptions(validator, catalog.ForeignTable, options)
			if tc.allowed {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
					t.Fatal("current role did not control validation", err)
				}
			}
			if scope.Catalog != journal || core.ScopeActiveSQLRoutineDepth(scope) != 0 || !reflect.DeepEqual(options, before) {
				t.Fatal("pure stock validation opened SQL state or borrowed input")
			}
			actor, _ := e.Roles().Lookup("private_actor")
			if scope.Identity.CurrentPrincipal != actor.Ref || scope.Identity.CurrentPrincipal == scope.Op.RoleState().SessionUser {
				t.Fatal("validator retained bootstrap/session authority")
			}
			if err := scope.ValidateForeignOptions(validator, catalog.ForeignAttribute, []catalog.ForeignOption{{Name: "force_null", Value: "true"}}); err != nil {
				t.Fatal("column-only validation checked table source", err)
			}
		})
	}
	t.Run("membership and principal generations are not retained", func(t *testing.T) {
		e, view, installation := fileFDWInstalled(t)
		enginetest.Run(t, e, "CREATE ROLE private_actor")
		enginetest.Run(t, e, "GRANT pg_read_server_files TO private_actor")
		original, _ := e.Roles().Lookup("private_actor")
		validator := view.MustProcedure(installation.Members[1])
		check := func(t *testing.T, allowed bool) {
			session := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, session, "SET ROLE private_actor")
			scope := pgrowlocksUnitScope(t, e, session)
			err := scope.ValidateForeignOptions(validator, catalog.ForeignTable, []catalog.ForeignOption{{Name: "filename", Value: "/x"}})
			if allowed {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
					t.Fatal("revoked privilege survived", err)
				}
			}
		}
		t.Run("granted", func(t *testing.T) { check(t, true) })
		enginetest.Run(t, e, "REVOKE pg_read_server_files FROM private_actor")
		t.Run("revoked", func(t *testing.T) { check(t, false) })
		enginetest.Run(t, e, "DROP ROLE private_actor")
		enginetest.Run(t, e, "CREATE ROLE private_actor")
		recreated, _ := e.Roles().Lookup("private_actor")
		if original.Ref == recreated.Ref {
			t.Fatal("recreated role reused authority generation")
		}
		t.Run("recreated without grant", func(t *testing.T) { check(t, false) })
		enginetest.Run(t, e, "GRANT pg_read_server_files TO private_actor")
		t.Run("recreated with grant", func(t *testing.T) { check(t, true) })
		t.Run("old principal cannot borrow recreated name", func(t *testing.T) {
			session := enginetest.StartSession(t, e, nil)
			scope := pgrowlocksUnitScope(t, e, session)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(scope.Op)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			stale := core.AuthorizationActingAs(auth, original.Ref)
			scope.ForeignActionAuth = &stale
			if err := scope.ValidateForeignOptions(validator, catalog.ForeignTable, []catalog.ForeignOption{{Name: "filename", Value: "/x"}}); err == nil {
				t.Fatal("old role acquired recreated-name authority")
			}
		})
	})
	t.Run("validated candidates retain publication fences", func(t *testing.T) {
		for _, change := range []string{"role", "procedure", "replacement"} {
			t.Run(change, func(t *testing.T) {
				e, _, _ := fileFDWInstalled(t)
				for _, setup := range []string{"CREATE ROLE private_actor", "GRANT pg_read_server_files TO private_actor", "GRANT USAGE,CREATE ON SCHEMA public TO private_actor", "CREATE SERVER private_file_server FOREIGN DATA WRAPPER file_fdw", "GRANT USAGE ON FOREIGN SERVER private_file_server TO private_actor"} {
					enginetest.Run(t, e, setup)
				}
				session := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, session, "SET ROLE private_actor")
				op, err := session.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(op.Done)
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if !scope.Finished {
						scope.Finish(context.Canceled)
					}
				})
				// Own an admitted enclosing statement, including its catalog/root
				// capture, before transferring a construction into its journal.
				enclosing := enginetest.Statement(t, "SELECT 1")
				description, err := scope.Describe(enclosing)
				if err != nil {
					t.Fatal(err)
				}
				scope.Expected = &description
				result, err := scope.Execute(enclosing, 0, nil)
				if err != nil {
					t.Fatal(err)
				}
				row := make([]sqltype.Value, len(result.Rows.Schema()))
				if ok, err := result.Rows.Next(op.Context(), row); err != nil || !ok {
					t.Fatal("enclosing first row", ok, err)
				}
				if ok, err := result.Rows.Next(op.Context(), row); err != nil || ok {
					t.Fatal("enclosing EOF", ok, err)
				}
				if err := result.Rows.Close(); err != nil {
					t.Fatal(err)
				}
				core.ScopeForeignUnlocked(scope)
				statement := enginetest.Statement(t, "CREATE FOREIGN TABLE private_file_candidate(id integer) SERVER private_file_server OPTIONS(filename '/x')").(*parser.ForeignDDL)
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(scope.Op)
				if err != nil {
					e.Mu.Unlock()
					t.Fatal(err)
				}
				stage := e.BeginCatalogStage(auth.Catalog)
				stage.AuthorizeLocked(auth, scope.Op.Session)
				jobs, _, err := fdw.StageTable(tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, statement, scope.Input)
				var intended *catalog.Relation
				for _, descriptor := range stage.Descriptors() {
					if relation, ok := descriptor.(*catalog.Relation); ok && relation.Name() == "private_file_candidate" {
						intended = relation
					}
				}
				e.Mu.Unlock()
				t.Cleanup(func() { e.DiscardCatalogStage(stage) })
				if err != nil || intended == nil || len(jobs) != 1 {
					t.Fatal("genuine stock candidate", err, intended, len(jobs))
				}
				token, err := scope.StageForeignConstruction(stage, intended.Address(), nil)
				if err != nil {
					t.Fatal(err)
				}
				journal := scope.Catalog
				if err := fdw.RunValidations(scope.Op.Context(), scope, jobs); err != nil {
					t.Fatal(err)
				}
				authority := scope.CatalogAuthority
				if authority == nil || authority.Session != session || authority.PublishedRoles != e.RoleStore.Published || authority.Roles != auth.RoleRecord.Revision {
					t.Fatal("membership facts lost original Finish authority")
				}
				if scope.Catalog != journal || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
					t.Fatal("stock validation replaced construction journal")
				}
				if err := token.Advance(0, nil); err != nil {
					t.Fatal(err)
				}
				if err := token.Complete(); err != nil {
					t.Fatal(err)
				}
				if scope.CatalogAuthority != authority {
					t.Fatal("construction replaced original membership publication authority")
				}
				switch change {
				case "role":
					enginetest.Run(t, e, "REVOKE pg_read_server_files FROM private_actor")
				case "procedure":
					enginetest.Run(t, e, "ALTER FUNCTION file_fdw_validator(text[],oid) RENAME TO private_changed_validator")
				case "replacement":
					if err := engine.Reset(context.Background(), e); err != nil {
						t.Fatal(err)
					}
				}
				if err := scope.Finish(nil); err == nil {
					t.Fatal("stale validated candidate published", change)
				}
				current := e.CatalogRead()
				public := current.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
				if current.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: public, Name: "private_file_candidate"}) != (ids.ObjectAddress{}) || e.Storage.Relations[intended.Address()] != nil {
					t.Fatal("failed publication leaked foreign metadata/storage")
				}
			})
		}
	})
	t.Run("selected declaration and statement replacement fence", func(t *testing.T) {
		e, view, installation := fileFDWInstalled(t)
		session := enginetest.StartSession(t, e, nil)
		scope := pgrowlocksUnitScope(t, e, session)
		validator := view.MustProcedure(installation.Members[1])
		copied := *validator
		token := *copied.Foreign
		copied.Foreign = &token
		if err := scope.ValidateForeignOptions(&copied, catalog.ForeignTable, []catalog.ForeignOption{{Name: "filename", Value: "/x"}}); err == nil {
			t.Fatal("foreign token acquired validator runtime")
		}
		if scope.Catalog != nil || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
			t.Fatal("rejected stock identity entered SQL runtime")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		scope.Op.Ctx = ctx
		if err := scope.ValidateForeignOptions(validator, catalog.ForeignTable, nil); !errors.Is(err, context.Canceled) {
			t.Fatal("statement cancellation lost", err)
		}
	})
	t.Run("bound call owns immutable provider and argument recipe", func(t *testing.T) {
		e, view, installation := fileFDWInstalled(t)
		session := enginetest.StartSession(t, e, nil)
		op, err := session.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		query := enginetest.BindSelect(t, e, op, "SELECT file_fdw_validator(ARRAY['filename=/x'],3118::oid)")
		projection, ok := query.Root.(*plan.ProjectNode)
		if !ok || len(projection.Expressions) != 1 {
			t.Fatal("missing genuine bound scalar projection")
		}
		node, ok := projection.Expressions[0].(plan.StockFDWExpr)
		if !ok {
			t.Fatalf("wrong installed carrier %T", projection.Expressions[0])
		}
		procedure := view.MustProcedure(installation.Members[1])
		if node.RoutineReference != procedure || node.Ref.Address != procedure.Address() || len(node.Args) != 2 || node.PlanningFold() != expr.FoldNever {
			t.Fatal("binding lost original provider/operands or cached privileged execution")
		}
		children := slices.Clone(node.Args)
		rebuilt, err := node.Rebuild(node.Info, children)
		if err != nil {
			t.Fatal(err)
		}
		next := rebuilt.(plan.StockFDWExpr)
		if next.RoutineReference != procedure || next.Ref != node.Ref || &next.Args[0] == &node.Args[0] || !reflect.DeepEqual(next.Args, node.Args) {
			t.Fatal("rebuild rewrote original argument backing or provider provenance")
		}
	})
}
