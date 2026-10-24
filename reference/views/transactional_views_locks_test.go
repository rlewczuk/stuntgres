package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsLocks(t *testing.T) {
	cases := []struct {
		q, relation, holderMode, waitMode   string
		count                               int64
		viewName, materializedName, newKind string
		viewValue                           int64
	}{
		{"CREATE VIEW txv_new AS SELECT id FROM txv_source", "txv_source", "ACCESS EXCLUSIVE", "AccessShareLock", 0, "txv_v", "txv_m", "v", 7},
		{"CREATE MATERIALIZED VIEW txv_new AS SELECT id FROM txv_source", "txv_source", "ACCESS EXCLUSIVE", "AccessShareLock", 1, "txv_v", "txv_m", "m", 7},
		{"CREATE OR REPLACE VIEW txv_v AS SELECT id+1 AS id FROM txv_source", "txv_v", "ACCESS SHARE", "AccessExclusiveLock", 0, "txv_v", "txv_m", "", 8},
		{"ALTER VIEW txv_v RENAME TO txv_new", "txv_v", "ACCESS SHARE", "AccessExclusiveLock", 0, "txv_new", "txv_m", "", 7},
		{"DROP VIEW txv_v", "txv_v", "ACCESS SHARE", "AccessExclusiveLock", 0, "", "txv_m", "", 7},
		{"ALTER MATERIALIZED VIEW txv_m RENAME TO txv_new", "txv_m", "ACCESS SHARE", "AccessExclusiveLock", 0, "txv_v", "txv_new", "", 7},
		{"DROP MATERIALIZED VIEW txv_m", "txv_m", "ACCESS SHARE", "AccessExclusiveLock", 0, "txv_v", "", "", 7},
	}
	for _, tc := range cases {
		for _, action := range []string{"release", "cancel", "timeout"} {
			t.Run(tc.q+"/"+action, func(t *testing.T) {
				transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, worker, holder *sql.Conn, mode string, held bool) {
					base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, worker, b.Recorder, s) }
					base(fixture.Exec("CREATE TABLE txv_source(id INT)", 0))
					base(fixture.Exec("INSERT INTO txv_source VALUES(7)", 1))
					base(fixture.Exec("CREATE VIEW txv_v AS SELECT id FROM txv_source", 0))
					base(fixture.Exec("CREATE MATERIALIZED VIEW txv_m AS SELECT id FROM txv_source", 1))
					originalView := transactionalViewsIdentity(t, p, worker, "txv_v")
					originalMaterialized := transactionalViewsIdentity(t, p, worker, "txv_m")
					var viewDefinition, materializedDefinition string
					if err := worker.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('txv_v'::regclass,true),pg_get_viewdef('txv_m'::regclass,true)").Scan(&viewDefinition, &materializedDefinition); err != nil {
						t.Fatal(err)
					}
					var rewriteCount int64
					if err := worker.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_rewrite WHERE rulename='_RETURN'").Scan(&rewriteCount); err != nil {
						t.Fatal(err)
					}
					observe := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, holder, b.Recorder, s) }
					rows := func(name string, value int64) {
						observe(fixture.Select("SELECT id FROM "+name, []string{"id"}, []string{"INT4"}, [][]any{{value}}))
					}
					absent := func(name string) {
						fixture.QueryTableCreateAbsent(t, observe, name)
						// The relation rowtype and its array are definition companions.
						fixture.QueryTableCreateAbsent(t, observe, "_"+name)
					}
					identity := func(name, kind string, want []int64) {
						got := transactionalViewsIdentity(t, p, holder, name)
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("%s identity got %v want %v", name, got, want)
						}
						observe(fixture.Select("SELECT relkind::TEXT AS kind FROM pg_class WHERE oid=$1", []string{"kind"}, []string{"TEXT"}, [][]any{{kind}}, want[0]))
					}
					definition := func(name, want string) {
						observe(fixture.Select("SELECT pg_get_viewdef('"+name+"'::regclass,true) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{want}}))
					}
					unchanged := func() {
						absent("txv_new")
						rows("txv_source", 7)
						rows("txv_v", 7)
						rows("txv_m", 7)
						identity("txv_v", "v", originalView)
						identity("txv_m", "m", originalMaterialized)
						definition("txv_v", viewDefinition)
						definition("txv_m", materializedDefinition)
						fixture.MaterializedViewPopulation(observe, "txv_m", true)
						observe(fixture.Select("SELECT count(*) FROM pg_rewrite WHERE rulename='_RETURN'", []string{"count"}, []string{"INT8"}, [][]any{{rewriteCount}}))
					}
					effects := func() {
						rows("txv_source", 7)
						if tc.viewName == "" {
							absent("txv_v")
						} else {
							rows(tc.viewName, tc.viewValue)
							identity(tc.viewName, "v", originalView)
							if tc.viewValue == 7 {
								definition(tc.viewName, viewDefinition)
							}
							if tc.viewName != "txv_v" {
								absent("txv_v")
							}
						}
						if tc.materializedName == "" {
							absent("txv_m")
						} else {
							rows(tc.materializedName, 7)
							identity(tc.materializedName, "m", originalMaterialized)
							definition(tc.materializedName, materializedDefinition)
							fixture.MaterializedViewPopulation(observe, tc.materializedName, true)
							if tc.materializedName != "txv_m" {
								absent("txv_m")
							}
						}
						if tc.newKind != "" {
							rows("txv_new", 7)
							observe(fixture.Select("SELECT relkind::TEXT AS kind FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='txv_new'", []string{"kind"}, []string{"TEXT"}, [][]any{{tc.newKind}}))
							definition("txv_new", viewDefinition)
							if tc.newKind == "m" {
								fixture.MaterializedViewPopulation(observe, "txv_new", true)
							}
						} else if tc.viewName != "txv_new" && tc.materializedName != "txv_new" {
							absent("txv_new")
						}
					}
					var x fixture.Executor = worker
					if held {
						stmt, err := worker.PrepareContext(p.Ctx, tc.q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						x = fixture.HeldStatement{Executor: worker, Statement: stmt, SQL: tc.q}
					}
					_, finish := transactionalViewsBegin(t, p, b, worker, mode)
					ended := mode == "autocommit"
					retired := false
					end := func(commit bool) { finish(commit); ended = true }
					// A Conn.Raw retirement while database/sql still owns an active
					// Tx waits for that Tx's lease. End it first even on assertion
					// failure, rather than waiting for the scenario context to expire.
					defer func() {
						if !ended {
							finish(false)
						}
						if action == "timeout" && !retired {
							worker.Raw(func(any) error { return driver.ErrBadConn })
						}
					}()
					htx := fixture.QueryLockBegin(t, p.Ctx, holder)
					defer htx.Rollback()
					if tc.relation == "txv_m" {
						q := "SELECT id FROM txv_m"
						stmt, err := htx.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: htx, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					} else {
						fixture.RunStep(t, p.Ctx, htx, b.Recorder, fixture.Exec("LOCK TABLE "+tc.relation+" IN "+tc.holderMode+" MODE", 0))
					}
					pid, hpid := 0, 0
					var oid int64
					if b.PG {
						pid = fixture.LockPID(t, p.Ctx, worker)
						hpid = fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, tc.relation)
					}
					// Arm only after held preparation, BEGIN, holder and PID probes.
					if action == "timeout" {
						base(fixture.Exec("SET lock_timeout='100ms'", 0))
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					join := fixture.LockOperation(t, p.Ctx, func(context.Context) fixture.RelationOutcome {
						defer close(finished)
						r, err := x.ExecContext(ctx, tc.q)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					if action != "timeout" {
						fixture.RelationLockPending(t, p, b, pid, hpid, oid, tc.waitMode, finished)
						if action == "release" {
							if err := htx.Rollback(); err != nil {
								t.Fatal(err)
							}
						} else if b.PG {
							var signaled bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
								t.Fatal(signaled, err)
							}
						} else {
							cancel()
						}
					}
					out := join()
					if action == "release" {
						if out.Err != nil {
							t.Fatal(out.Err)
						}
						n, err := out.Result.RowsAffected()
						if err != nil || n != tc.count {
							t.Fatal(n, err)
						}
						end(true)
						effects()
					} else {
						if action == "timeout" {
							want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							if tc.relation == "txv_source" {
								if tc.count == 0 {
									want.Position = "39"
								} else if !held {
									want.Position = "52"
								}
							}
							fixture.AssertDiagnostic(t, out.Err, &want)
						} else if b.PG {
							want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
							fixture.AssertDiagnostic(t, out.Err, &want)
						} else if !errors.Is(out.Err, context.Canceled) {
							t.Fatal(out.Err)
						}
						end(false)
						if err := htx.Rollback(); err != nil {
							t.Fatal(err)
						}
						if action == "timeout" {
							// End and retire the timer-armed physical owner before any
							// observation. All subsequent SQL uses the unarmed holder.
							worker.Raw(func(any) error { return driver.ErrBadConn })
							retired = true
						}
						unchanged()
						if action == "timeout" {
							follower := fixture.QueryLockBegin(t, p.Ctx, holder)
							defer follower.Rollback()
							follow := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
								// AX conflicts with a stale AS or AX contribution. MV
								// obtains AX through supported DDL, not LOCK TABLE syntax.
								lockSQL := "LOCK TABLE " + tc.relation + " IN ACCESS EXCLUSIVE MODE"
								if tc.relation == "txv_m" {
									lockSQL = "ALTER MATERIALIZED VIEW txv_m OWNER TO CURRENT_USER"
								}
								if _, err := follower.ExecContext(ctx, lockSQL); err != nil {
									return fixture.RelationOutcome{Err: err}
								}
								r, err := follower.ExecContext(ctx, tc.q)
								return fixture.RelationOutcome{Result: r, Err: err}
							})
							result := follow()
							if result.Err != nil {
								t.Fatal("post-timeout follower", result.Err)
							}
							n, err := result.Result.RowsAffected()
							if err != nil || n != tc.count {
								t.Fatal(n, err)
							}
							if err := follower.Commit(); err != nil {
								t.Fatal(err)
							}
						} else {
							fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(tc.q, tc.count))
						}
						effects()
					}
				})
			})
		}
	}
}
