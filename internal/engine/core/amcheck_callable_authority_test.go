package core_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Exercise the operational restricted invocation and real callable hosts, not
// just the authority selector. The inspected relation is deliberately not the
// relation whose owner supplies the restricted frame.
func TestAmcheckCallableAuthorityPrecedence(t *testing.T) {
	for _, host := range []string{"sequence", "lo_get", "lo_put", "pgrowlocks"} {
		for _, actor := range []string{"am_owner", "am_caller", "am_ri", "am_rule"} {
			for _, queued := range []bool{false, true} {
				if queued && host != "sequence" && host != "pgrowlocks" {
					continue // Large-object calls have no relation-lock wait.
				}
				name := host + "/" + actor
				if queued {
					name += "/queued"
				}
				t.Run(name, func(t *testing.T) {
					e, index, _ := amcheckUnitRoot(t, false)
					for _, role := range []string{"am_owner", "am_caller", "am_ri", "am_rule", "am_access"} {
						enginetest.Run(t, e, "CREATE ROLE "+role)
					}
					enginetest.Run(t, e, "ALTER TABLE t OWNER TO am_owner")
					enginetest.Run(t, e, "CREATE SEQUENCE callable_seq")
					enginetest.Run(t, e, "CREATE TABLE callable_rows(n INT)")
					enginetest.Run(t, e, "INSERT INTO callable_rows VALUES(1)")
					enginetest.Run(t, e, "SELECT lo_from_bytea(920191::oid,decode('736563726574','hex'))")
					enginetest.Run(t, e, "GRANT USAGE ON SEQUENCE callable_seq TO am_access")
					enginetest.Run(t, e, "GRANT SELECT ON callable_rows TO am_access")
					enginetest.Run(t, e, "GRANT SELECT,UPDATE ON LARGE OBJECT 920191 TO am_access")
					enginetest.Run(t, e, "GRANT am_access TO "+actor)
					if queued && actor != "am_owner" {
						// Admit with the owner's privilege, then remove it only
						// after the actual host is visibly waiting. The enclosing
						// actor remains privileged through readmission.
						enginetest.Run(t, e, "GRANT am_access TO am_owner")
					}
					pgrowlocksUnitInstalled(t, e)
					pg := pgrowlocksUnitExpression(t, e)
					parent := e.Catalog.State.MustRelation(index.SQLIndex.Parent)
					seq := enginetest.CatalogRef(t, e, e.CatalogRead(), "callable_seq")
					table := enginetest.CatalogRef(t, e, e.CatalogRead(), "callable_rows")
					reader, ticket := blockingUnitOwner(t, e)
					sessionSQL(t, reader, "SET ROLE am_caller")
					scope := pgrowlocksUnitScope(t, e, reader)
					scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: pg.Ref, Revision: pg.Procedure.Revision()}}
					// Admit a genuine singleton query before entering the
					// restricted recipe, just as an installed scalar call does.
					e.Mu.Lock()
					source, err := e.CaptureQuerySourcesLocked(scope, nil, true)
					e.Mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					defer source.Close()
					e.Mu.Lock()
					caller, err := e.AuthorizationLocked(scope.Op)
					e.Mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					ri, _ := e.Roles().Lookup("am_ri")
					rule, _ := e.Roles().Lookup("am_rule")
					riAuth := core.AuthorizationActingAs(caller, ri.Ref)
					ruleAuth := core.AuthorizationActingAs(caller, rule.Ref)
					scope.ForeignActionAuth = &riAuth
					scratch := core.ScopeRuleScratch(scope)
					scratch.RuleAction.Auth = &ruleAuth
					core.ScopeInstallRuleScratch(scope, scratch)
					beforeRole := scope.Op.RoleState()
					beforeCatalog := scope.Catalog
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					scope.Op.Ctx = ctx
					var value int64
					var object functions.LargeObjectResult
					var rows []sqltype.Value
					invoke := func() error {
						return scope.WithAmcheckOwner(parent, func(_ *exec.Context) error {
							var callErr error
							switch host {
							case "sequence":
								value, callErr = scope.WriteHost().Sequence(functions.SequenceRequest{Name: "nextval", Ref: seq})
							case "lo_get":
								object, callErr = scope.WriteHost().LargeObject(functions.LargeObjectRequest{Name: "lo_get", OID: 920191, Length: -1})
							case "lo_put":
								object, callErr = scope.WriteHost().LargeObject(functions.LargeObjectRequest{Name: "lo_put", OID: 920191, Data: "changed"})
							case "pgrowlocks":
								rows, callErr = scope.WriteHost().Pgrowlocks("public.callable_rows", pg.Usage.Type)
							}
							return callErr
						})
					}
					resource := lock.Resource{ObjectRef: seq}
					mode := lock.RowExclusive
					if host == "pgrowlocks" {
						resource.ObjectRef, mode = table, lock.AccessShare
					}
					if queued {
						_, holder := blockingUnitOwner(t, e)
						amcheckUnitGrant(t, e, holder, resource, lock.AccessExclusive)
						done, stopped := make(chan error, 1), make(chan struct{})
						go func() { defer close(stopped); done <- invoke() }()
						defer func() {
							cancel()
							select {
							case <-stopped:
							case <-time.After(5 * time.Second):
								t.Error("callable worker did not stop")
							}
						}()
						amcheckUnitQueue(t, e, ticket, resource, mode, done)
						if actor != "am_owner" {
							enginetest.Run(t, e, "REVOKE am_access FROM am_owner")
						}
						e.EndLockOwner(holder)
						err = amcheckUnitJoin(t, done)
					} else {
						err = invoke()
					}
					allowed := actor == "am_owner"
					if allowed {
						if err != nil {
							t.Fatal("restricted owner permission refused", err)
						}
						switch host {
						case "sequence":
							if value != 1 {
								t.Fatal("unexpected nextval", value)
							}
						case "lo_get":
							if object.Data != "secret" {
								t.Fatal("missing authorized contents", object)
							}
						case "lo_put":
							if object.OID != 920191 || scope.Catalog == beforeCatalog {
								t.Fatal("authorized write missing", object)
							}
						case "pgrowlocks":
							if len(rows) != 0 {
								t.Fatal("unlocked table returned locks", rows)
							}
						}
					} else {
						message := "permission denied for large object 920191"
						if host == "sequence" {
							message = "permission denied for sequence callable_seq"
						} else if host == "pgrowlocks" {
							message = "permission denied for table callable_rows"
						}
						amcheckUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: message})
						if value != 0 || object != (functions.LargeObjectResult{}) || rows != nil || scope.Catalog != beforeCatalog {
							t.Fatal("denied host returned contents or staged effects", value, object, rows)
						}
					}
					if scope.Op.Restricted != nil || scope.Op.RoleState() != beforeRole || scope.ForeignActionAuth != &riAuth || core.ScopeRuleScratch(scope).RuleAction.Auth != &ruleAuth {
						t.Fatal("enclosing authority not restored")
					}
					e.Mu.Lock()
					restored, restoreErr := scope.RoutineAuthorizationLocked()
					write, writeErr := core.ScopeWriteAuthorizationLocked(scope, e)
					e.Mu.Unlock()
					if restoreErr != nil || writeErr != nil || restored.State.CurrentUser != ri.Ref || write.State.CurrentUser != ri.Ref {
						t.Fatal("ordinary RI authority changed", restoreErr, writeErr)
					}
					scope.ForeignActionAuth = nil
					e.Mu.Lock()
					write, writeErr = core.ScopeWriteAuthorizationLocked(scope, e)
					e.Mu.Unlock()
					scope.ForeignActionAuth = &riAuth
					if writeErr != nil || write.State.CurrentUser != rule.Ref {
						t.Fatal("ordinary rule authority changed", writeErr)
					}
					if host == "lo_get" || host == "lo_put" {
						address := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 920191}
						e.Mu.Lock()
						pending := e.PrivateCatalogLocked(scope.PendingCatalog()).Get(address).(*catalog.LargeObject)
						published := e.Catalog.State.Get(address).(*catalog.LargeObject)
						e.Mu.Unlock()
						want := "secret"
						if host == "lo_put" && allowed {
							want = "changed"
						}
						data, readErr := pending.Content.Read(0, -1)
						if readErr != nil || data != want {
							t.Fatal("wrong private large-object effect", data, readErr)
						}
						data, readErr = published.Content.Read(0, -1)
						if readErr != nil || data != "secret" {
							t.Fatal("callable changed published contents", data, readErr)
						}
					}
					// A denied nextval must not consume a sequence value or set
					// lastval. Inspect by a real subsequent superuser call.
					if host == "sequence" {
						want := int64(1)
						if allowed {
							want = 2
						}
						sequenceScalar(t, sessionStart(t, e, nil), "SELECT nextval('callable_seq')", want)
						if !allowed && core.PayloadOf(reader).Sequences.Last() != (ids.ObjectAddress{}) {
							t.Fatal("denied sequence changed session memory")
						}
					}
					if host == "pgrowlocks" && e.Locks.HasRelationMode(ticket, resource, lock.AccessShare) {
						t.Fatal("pgrowlocks retained scoped lock")
					}
				})
			}
		}
	}
}
