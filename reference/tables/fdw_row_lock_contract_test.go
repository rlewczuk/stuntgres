package reference_test

import (
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwRowLockAdmissionContracts(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, variant := range []string{"no_refetch", "no_transaction", "no_identity", "enum", "weak", "empty", "duplicate", "arity", "value"} {
			t.Run("contract/"+fdwRowLockModeName(prepared)+"/"+variant, func(t *testing.T) {
				fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
					q := "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE"
					if b.PG {
						// postgres_fdw owns its own valid capability set; user Go
						// method-set contract violations have no SQL oracle equivalent.
						fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, q, prepared, [][]any{{int64(1), "one", true}}))
						fixture.LockExec(t, p.Ctx, source, "DELETE FROM fdw_source.data WHERE rid=1", 1)
						return
					}
					message := ""
					capability := false
					switch variant {
					case "no_refetch", "no_transaction", "no_identity":
						run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_mark_"+variant, 0))
						capability = true
						switch variant {
						case "no_refetch":
							message = "foreign row marking requires a refetch callback"
						case "no_transaction":
							message = "foreign row locking requires transaction support"
						case "no_identity":
							message = "foreign row mark scan does not supply row identity"
						}
					case "enum":
						locker.planHook = func(p stuntgres.ForeignRowMarkPlan) stuntgres.ForeignRowMarkPlan {
							p.Type = stuntgres.ForeignRowMarkType(255)
							return p
						}
						message = "stuntgres: invalid foreign row mark type"
					case "weak":
						locker.planHook = func(p stuntgres.ForeignRowMarkPlan) stuntgres.ForeignRowMarkPlan {
							p.Type = stuntgres.ForeignRowMarkShare
							return p
						}
						message = "stuntgres: foreign row mark weakens the requested lock strength"
					case "empty":
						locker.planHook = func(p stuntgres.ForeignRowMarkPlan) stuntgres.ForeignRowMarkPlan {
							p.Identity = stuntgres.ForeignUpdateTargets{}
							return p
						}
						message = "stuntgres: foreign row mark requires an identity descriptor"
					case "duplicate":
						locker.planHook = func(p stuntgres.ForeignRowMarkPlan) stuntgres.ForeignRowMarkPlan {
							p.Identity.Names = []string{"rid", "rid"}
							return p
						}
						message = "stuntgres: invalid foreign row identity descriptor"
					case "arity":
						locker.identityHook = func(id stuntgres.ForeignRowIdentity) stuntgres.ForeignRowIdentity { return id[:1] }
						message = "stuntgres: foreign row identity width does not match its descriptor"
					case "value":
						locker.identityHook = func(id stuntgres.ForeignRowIdentity) stuntgres.ForeignRowIdentity { id[0] = struct{}{}; return id }
						message = "stuntgres: foreign row identity contains a non-driver value"
					}
					run(fixture.Exec("BEGIN", 0))
					defer fdwRowLockRollback(t, c)
					before := locker.transactions
					o := fdwRowLockFailedQuery(p.Ctx, c, q, prepared)
					if capability {
						fdwRowLockDiagnostic(t, o, "0A000", message)
					} else {
						if o.Rows != nil || o.Result != nil || o.Err == nil || o.Err.Error() != message {
							t.Fatalf("contract failure %#v, want %q", o, message)
						}
						var state interface{ SQLState() string }
						if errors.As(o.Err, &state) {
							t.Fatal("ordinary contract failure acquired a SQLSTATE", o.Err)
						}
						if o.Stmt != nil {
							o.Stmt.Close()
						}
					}
					if variant != "no_identity" && variant != "arity" && variant != "value" && locker.transactions != before {
						t.Fatal("invalid capability selection began a source transaction")
					}
					// Admission/identity validation must not acquire a real row lock.
					fixture.LockExec(t, p.Ctx, source, "DELETE FROM fdw_source.data WHERE rid=1", 1)
					run(fixture.Exec("ROLLBACK", 0))
				})
			})
		}
	}
}
