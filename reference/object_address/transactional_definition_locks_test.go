package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalDefinitionNames(t *testing.T) {
	for _, kind := range []string{"method", "family", "class", "operator", "function"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit_%t", kind, commit), func(t *testing.T) {
				definitionScenario(t, []string{"tx_name_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					runA := definitionRun(t, p, b, a)
					runC := definitionRun(t, p, b, c)
					q := `CREATE ACCESS METHOD tx_name_am TYPE INDEX HANDLER bthandler`
					table, constraint, detail := "pg_am", "pg_am_name_index", `Key (amname)=(tx_name_am) already exists.`
					ns := fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)
					switch kind {
					case "family":
						q = `CREATE OPERATOR FAMILY public.tx_name_family USING btree`
						table = "pg_opfamily"
						constraint = "pg_opfamily_am_name_nsp_index"
						detail = fmt.Sprintf("Key (opfmethod, opfname, opfnamespace)=(403, tx_name_family, %d) already exists.", ns)
					case "class":
						runA(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_name_family USING btree`, 0))
						q = `CREATE OPERATOR CLASS public.tx_name_class FOR TYPE integer USING btree FAMILY public.tx_name_family AS STORAGE integer`
						table = "pg_opclass"
						constraint = "pg_opclass_am_name_nsp_index"
						detail = fmt.Sprintf("Key (opcmethod, opcname, opcnamespace)=(403, tx_name_class, %d) already exists.", ns)
					case "operator", "function":
						runA(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_name_family USING btree`, 0))
						family := definitionID(t, p, a, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='tx_name_family'`)
						if kind == "operator" {
							q = `ALTER OPERATOR FAMILY public.tx_name_family USING btree ADD OPERATOR 1 pg_catalog.<(integer,integer)`
							table = "pg_amop"
							constraint = "pg_amop_fam_strat_index"
							detail = fmt.Sprintf("Key (amopfamily, amoplefttype, amoprighttype, amopstrategy)=(%d, 23, 23, 1) already exists.", family)
						} else {
							q = `ALTER OPERATOR FAMILY public.tx_name_family USING btree ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`
							table = "pg_amproc"
							constraint = "pg_amproc_fam_proc_index"
							detail = fmt.Sprintf("Key (amprocfamily, amproclefttype, amprocrighttype, amprocnum)=(%d, 23, 23, 1) already exists.", family)
						}
					}
					held := definitionHeld(t, p, c, q)
					defer held.Statement.Close()
					ap, cp := 0, 0
					if b.PG {
						ap = fixture.LockPID(t, p.Ctx, a)
						cp = fixture.LockPID(t, p.Ctx, c)
					}
					runA(fixture.Exec(`BEGIN`, 0))
					runA(fixture.Exec(q, 0))
					runC(fixture.Exec(`BEGIN`, 0))
					finished := make(chan struct{})
					await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						result, err := held.Statement.ExecContext(ctx)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					fixture.RelationLockPending(t, p, b, cp, ap, 0, "", finished)
					// A separate committed object must not be overwritten by either publisher.
					definitionRun(t, p, b, b.DB)(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_name_unrelated USING hash`, 0))
					if kind == "family" {
						definitionRun(t, p, b, b.DB)(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_name_family USING hash`, 0))
					}
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					runA(fixture.Exec(end, 0))
					outcome := await()
					if commit {
						d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "`+constraint+`"`)
						d.Detail = detail
						d.Schema = "pg_catalog"
						d.Table = table
						d.Constraint = constraint
						fixture.AssertDiagnostic(t, outcome.Err, &d)
						if outcome.Result != nil {
							t.Fatal("failed contender exposed result")
						}
						runC(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
						runC(fixture.Exec(`ROLLBACK`, 0))
					} else {
						fixture.DeleteReferenceCount(t, outcome.Result, outcome.Err, 0)
						runC(fixture.Exec(`COMMIT`, 0))
					}
					runA(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname='tx_name_unrelated' AND opfmethod=405`, 1))
					runC(fixture.Exec(`BEGIN`, 0))
					runC(fixture.Exec(`ROLLBACK`, 0))
				})
			})
		}
	}
}

func TestReferenceTransactionalDefinitionWaits(t *testing.T) {
	for _, release := range []string{"rollback", "savepoint", "close", "cancel", "timeout"} {
		t.Run(release, func(t *testing.T) {
			definitionScenario(t, []string{"tx_wait_am"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
				c, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				runA := definitionRun(t, p, b, a)
				runC := definitionRun(t, p, b, c)
				q := `CREATE ACCESS METHOD tx_wait_am TYPE INDEX HANDLER bthandler`
				held := definitionHeld(t, p, c, q)
				defer held.Statement.Close()
				ap, cp := 0, 0
				if b.PG {
					ap = fixture.LockPID(t, p.Ctx, a)
					cp = fixture.LockPID(t, p.Ctx, c)
				}
				runA(fixture.Exec(`BEGIN`, 0))
				runA(fixture.Exec(`SAVEPOINT name`, 0))
				runA(fixture.Exec(q, 0))
				runC(fixture.Exec(`BEGIN`, 0))
				if release == "timeout" {
					runC(fixture.Exec(`SET LOCAL lock_timeout='50ms'`, 0))
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				finished := make(chan struct{})
				await := fixture.LockOperation(t, p.Ctx, func(_ context.Context) fixture.RelationOutcome {
					defer close(finished)
					r, err := held.Statement.ExecContext(ctx)
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				// A timeout self-observes through its exact lock error; external cancellation
				// and release require an actual queue witness before touching the holder.
				if release != "timeout" {
					fixture.RelationLockPending(t, p, b, cp, ap, 0, "", finished)
				}
				switch release {
				case "rollback":
					runA(fixture.Exec(`ROLLBACK`, 0))
				case "savepoint":
					runA(fixture.Exec(`ROLLBACK TO name`, 0))
				case "close":
					err := a.Raw(func(raw any) error {
						if err := raw.(driver.Conn).Close(); err != nil {
							return err
						}
						return driver.ErrBadConn
					})
					if !errors.Is(err, driver.ErrBadConn) {
						t.Fatal("physical close failed", err)
					}
				case "cancel":
					if b.PG {
						var ok bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, `SELECT pg_cancel_backend($1)`, cp).Scan(&ok); err != nil || !ok {
							t.Fatal("cancel failed", ok, err)
						}
					} else {
						cancel()
					}
				}
				o := await()
				switch release {
				case "cancel":
					if b.PG {
						d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						definitionWaitDiagnostic(t, b, o.Err, d)
					} else if !errors.Is(o.Err, context.Canceled) {
						t.Fatal(o.Err)
					}
				case "timeout":
					d := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
					definitionWaitDiagnostic(t, b, o.Err, d)
				default:
					fixture.DeleteReferenceCount(t, o.Result, o.Err, 0)
				}
				if release == "cancel" || release == "timeout" {
					runC(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
					runC(fixture.Exec(`ROLLBACK`, 0))
					runA(fixture.Exec(`ROLLBACK`, 0))
					runC(fixture.Exec(q, 0))
				} else {
					runC(fixture.Exec(`COMMIT`, 0))
					if release == "savepoint" {
						runA(fixture.Exec(`COMMIT`, 0))
					}
				}
				runC(definitionCount(`SELECT count(*) FROM pg_am WHERE amname='tx_wait_am'`, 1))
				runC(fixture.Exec(`DROP ACCESS METHOD tx_wait_am`, 0))
				runC(fixture.Exec(q, 0))
				runC(fixture.Exec(`DROP ACCESS METHOD tx_wait_am`, 0))
			})
		})
	}
}

func definitionWaitDiagnostic(t *testing.T, b fixture.Backend, err error, want fixture.Diagnostic) {
	t.Helper()
	if b.PG {
		got := fixture.ExtractDiagnostic(t, err)
		if !regexp.MustCompile(`^while inserting index tuple \([0-9]+,[1-9][0-9]*\) in relation "pg_am_name_index"$`).MatchString(got.Context) {
			t.Fatalf("catalog unique-index wait Context: %q", got.Context)
		}
		want.Context = got.Context
	}
	fixture.AssertDiagnostic(t, err, &want)
}
