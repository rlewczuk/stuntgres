package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Complete the actual RI dispatcher before revoking authority. Unlike a unique
// wait inside the action, Finish must now reject with the retained child-owner
// call and diagnostic after the live owner phase has restored the caller.
func TestRIRoutinePublicationCurrentAuthority(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, event := range []string{"delete", "update"} {
			t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit]+"/"+event, func(t *testing.T) {
				e := engine.New()
				admin, finish := enginetest.RoleOperation(t, e, "postgres")
				defer finish()
				for _, query := range []string{
					"CREATE ROLE ri_publication_owner LOGIN", "CREATE ROLE ri_publication_actor LOGIN",
					"GRANT USAGE,CREATE ON SCHEMA public TO ri_publication_owner",
					"GRANT USAGE ON SCHEMA public TO ri_publication_actor",
					"CREATE SEQUENCE public.ri_publication_steps START 1",
					"CREATE TABLE public.ri_publication_parent(id integer PRIMARY KEY)",
					"CREATE TABLE public.ri_publication_child(id integer,pid integer DEFAULT json_array_length('[1]'::json) REFERENCES public.ri_publication_parent(id) ON DELETE SET DEFAULT ON UPDATE SET DEFAULT, CHECK(nextval('public.ri_publication_steps'::regclass)>0))",
					"CREATE UNIQUE INDEX ri_publication_unique ON public.ri_publication_child(pid)",
					"INSERT INTO public.ri_publication_parent VALUES(1),(2)",
					"INSERT INTO public.ri_publication_child VALUES(10,2)",
					"ALTER TABLE public.ri_publication_child OWNER TO ri_publication_owner",
					"REVOKE CREATE ON SCHEMA public FROM ri_publication_owner",
					"GRANT USAGE ON SEQUENCE public.ri_publication_steps TO ri_publication_owner",
					"GRANT SELECT,DELETE,UPDATE ON public.ri_publication_parent TO ri_publication_actor",
					"REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC",
					"GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO ri_publication_owner,ri_publication_actor",
				} {
					enginetest.RoleExecute(t, e, admin, query)
				}
				actor, actorDone := enginetest.RoleOperation(t, e, "ri_publication_actor")
				defer actorDone()
				transactionOpen := explicit
				if explicit {
					enginetest.RoleExecute(t, e, actor, "BEGIN")
					defer func() {
						if transactionOpen {
							enginetest.RoleExecute(t, e, actor, "ROLLBACK")
						}
					}()
				}
				childRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "ri_publication_child")
				child := e.CatalogRead().MustRelation(childRef.Address)
				original := child.ColumnMeta.Defaults[child.Attributes[1]]
				manifest := append([]ids.ObjectRef(nil), original.Dependencies...)
				routine, err := exec.ExpressionRoutineDescriptor(original.Expression)
				if err != nil || routine == nil || routine.Name() != "json_array_length" || routine.Revision() == 0 {
					t.Fatal("missing actual immutable DEFAULT routine", routine, err)
				}
				var originalCall ids.ObjectRef
				for _, ref := range manifest {
					if ref.Address == routine.Address() {
						originalCall = ref
					}
				}
				if originalCall.Domain() != e.Catalog.Domain || originalCall.Epoch != e.CatalogRead().Epoch() || originalCall.Address.ClassID != ids.ProcedureClassID {
					t.Fatal("missing defining DEFAULT callable lifetime", originalCall)
				}
				sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "ri_publication_steps")
				e.Mu.Lock()
				generator := e.Storage.Sequences[sequence.Address]
				seed := *generator
				oldChild := e.Storage.Relations[childRef.Address].Versions[0]
				e.Mu.Unlock()
				if seed.Last != 1 || !seed.Called {
					t.Fatal("seed must consume one genuine CHECK", seed)
				}
				scope, err := e.StartStatement(actor)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(context.Canceled)
				caller, err := e.ResolveLogin("ri_publication_actor")
				if err != nil || caller == child.Owner {
					t.Fatal("initiator must differ from actual child owner")
				}
				query := "DELETE FROM public.ri_publication_parent WHERE id=2"
				if event == "update" {
					query = "UPDATE public.ri_publication_parent SET id=3 WHERE id=2"
				}
				result, err := scope.Execute(enginetest.Statement(t, query), 0, nil)
				if err != nil || result.Count != 1 || result.Rows != nil {
					t.Fatal("actual parent mutation", result, err)
				}
				if err := core.ScopeCompleteCommandEffects(scope, false, false); err != nil {
					t.Fatal("actual SET DEFAULT command completion", err)
				}
				if scope.ForeignActionAuth != nil || scope.RoutineAuth.State.CurrentUser != caller {
					t.Fatal("actual RI dispatcher did not restore initiating caller")
				}
				rows := scope.Stage.Rows[childRef.Address]
				if len(rows) != 1 || rows[0].Row != oldChild.Row || rows[0].Base != oldChild.Version {
					t.Fatal("SET DEFAULT did not stage the actual child lineage", rows, oldChild)
				}
				pid, err := rows[0].Values[1].Integer()
				if err != nil || pid != 1 {
					t.Fatal("actual callable DEFAULT was not consumed", rows[0], err)
				}
				found := false
				for _, program := range core.ScopeRoutinePrograms(scope) {
					for _, call := range core.CommandRoutineProgramAdmission(&program).Calls {
						if call.Reference != originalCall {
							continue
						}
						if !core.CommandRoutineProgramRi(&program) || call.Routine != routine || call.Routine.Revision() != routine.Revision() || call.Consumer != child || call.Actor != child.Owner {
							t.Fatal("consumed DEFAULT lost original manifest/child-owner phase", call)
						}
						found = true
					}
				}
				if !found {
					t.Fatal("actual folded DEFAULT did not retain its original consumed call")
				}
				e.Mu.Lock()
				before := *generator
				e.Mu.Unlock()
				if before.Last != seed.Last+1 || !before.Called {
					t.Fatal("actual child candidate must consume one CHECK", seed, before)
				}
				// The initiator keeps EXECUTE: consulting the restored caller in
				// place of the recorded child owner would incorrectly permit Finish.
				enginetest.RoleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM ri_publication_owner")
				nullCountUnitDiagnostic(t, scope.Finish(nil), sqlstate.Error{
					Code: "42501", Message: "permission denied for function json_array_length",
					Context: `SQL statement "UPDATE ONLY "public"."ri_publication_child" SET "pid" = DEFAULT WHERE $1 OPERATOR(pg_catalog.=) "pid""`,
				})
				// Full diagnostic equality above also forbids a duplicate RI frame.
				e.Mu.Lock()
				after := *generator
				sameGenerator := e.Storage.Sequences[sequence.Address] == generator
				e.Mu.Unlock()
				if !sameGenerator || after != before {
					t.Fatal("final publication replayed volatile CHECK state", before, after)
				}
				if !scope.Finished || scope.ReplacementRead || scope.ForeignActionAuth != nil || len(core.ScopeRoutinePrograms(scope)) != 0 || len(scope.RoutineDependencies) != 0 || scope.RoutineAuth.State.CurrentUser == caller {
					t.Fatal("failed Finish retained statement admission resources")
				}
				if explicit {
					actor.Session.Transactions.Mu.Lock()
					installed := len(actor.Session.Transactions.Writes) != 0 || len(actor.Session.Transactions.Deletes) != 0
					actor.Session.Transactions.Mu.Unlock()
					if installed {
						t.Fatal("denied explicit statement installed private parent/child changes")
					}
					enginetest.RoleExecute(t, e, actor, "ROLLBACK")
					transactionOpen = false
				}
				uniqueClaimsEmpty(t, e)
				current := e.CatalogRead().MustRelation(childRef.Address)
				if !reflect.DeepEqual(current.ColumnMeta.Defaults[current.Attributes[1]], original) || !reflect.DeepEqual(current.ColumnMeta.Defaults[current.Attributes[1]].Dependencies, manifest) {
					t.Fatal("RI publication mutated defining DEFAULT manifest")
				}
				routineWaitRows(t, admin.Session, "SELECT id FROM public.ri_publication_parent ORDER BY id", [][]int64{{1}, {2}})
				routineWaitRows(t, admin.Session, "SELECT id,pid FROM public.ri_publication_child", [][]int64{{10, 2}})
				identity := enginetest.RoleExecute(t, e, actor, "SELECT current_user,json_array_length('[1]'::json)")
				var value [2]sqltype.Value
				more, err := identity.Rows.Next(context.Background(), value[:])
				name, nameErr := value[0].Text()
				length, lengthErr := value[1].Integer()
				if err != nil || !more || nameErr != nil || name != "ri_publication_actor" || lengthErr != nil || length != 1 {
					t.Fatal("failed RI publication changed caller identity or caller EXECUTE", value, err, nameErr, lengthErr)
				}
				identity.Rows.Close()
			})
		}
	}
}
