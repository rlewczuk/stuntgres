package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseDropForce(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		t.Run(mode, func(t *testing.T) {
			const a, b = "dd_force_a", "dd_force_b"
			names := []string{a, b}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, admin, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					backend.Recorder = record
					c := &databaseCreateCase{t, p, backend, admin, &names}
					ordinary := "DROP DATABASE " + a
					force := ordinary + " WITH (FORCE)"
					plain, forced := databaseDropPrepare(c, mode, ordinary), databaseDropPrepare(c, mode, force)
					c.create(a, "", false)
					c.create(b, "", false)
					peer, peerRaw, peerRecord := c.pool(b, "")
					fixture.RunStep(t, p.Ctx, peer, peerRecord, fixture.Exec("CREATE TABLE force_value(v integer)", 0))
					fixture.RunStep(t, p.Ctx, peer, peerRecord, fixture.Exec("INSERT INTO force_value VALUES(22)", 1))
					victims, raw, victimRecord := c.pool(a, "")
					victims.SetMaxIdleConns(0)
					one, err := victims.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { one.Close() })
					fixture.RunStep(t, p.Ctx, one, victimRecord, fixture.Exec("CREATE TABLE force_value(v integer)", 0))
					fixture.RunStep(t, p.Ctx, one, victimRecord, fixture.Exec("INSERT INTO force_value VALUES(11)", 1))
					checkBusy := func(n int64) {
						t.Helper()
						fixture.DatabaseReferenceOwners(t, p, admin, record, a, n)
						want := databaseCreateDiagnostic("55006", "database \""+a+"\" is being accessed by other users")
						want.Detail = "There is 1 other session using the database."
						if n == 2 {
							want.Detail = "There are 2 other sessions using the database."
						}
						plain.run(c, fixture.ErrorDiagnostic(ordinary, want))
						fixture.RunStep(t, p.Ctx, one, victimRecord, fixture.Select("SELECT v FROM force_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
					}
					checkBusy(1)
					two, err := victims.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { two.Close() })
					checkBusy(2)
					fixture.RunStep(t, p.Ctx, one, victimRecord, fixture.Exec("BEGIN", 0))
					fixture.RunStep(t, p.Ctx, one, victimRecord, fixture.Exec("INSERT INTO force_value VALUES(99)", 1))
					fixture.RunStep(t, p.Ctx, one, victimRecord, fixture.Exec("CREATE TEMP TABLE force_temp(v integer)", 0))
					fixture.RunStep(t, p.Ctx, one, victimRecord, fixture.Exec("DECLARE force_cursor CURSOR FOR SELECT v FROM force_value", 0))
					fixture.RunStep(t, p.Ctx, one, victimRecord, fixture.Exec("PREPARE force_prepared AS SELECT 7", 0))
					fixture.RunStep(t, p.Ctx, one, victimRecord, fixture.Select("SELECT pg_advisory_lock(99181)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
					fixture.RunStep(t, p.Ctx, two, victimRecord, fixture.Exec("LISTEN force_notice", 0))
					// A real independently held row lock parks the second physical command.
					fixture.RunStep(t, p.Ctx, one, victimRecord, fixture.Exec("LOCK TABLE force_value IN ACCESS EXCLUSIVE MODE", 0))
					holder, pid := fixture.LockPID(t, p.Ctx, one), fixture.LockPID(t, p.Ctx, two)
					relation := int64(0)
					if backend.PG {
						if err := one.QueryRowContext(p.Ctx, "SELECT 'force_value'::regclass::oid").Scan(&relation); err != nil {
							t.Fatal(err)
						}
					}
					done := make(chan struct{})
					join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(done)
						result, err := two.ExecContext(ctx, "INSERT INTO force_value VALUES(77)")
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					fixture.RelationLockPending(t, p, backend, pid, holder, relation, "RowExclusiveLock", done)
					forced.run(c, fixture.Exec(force, 0))
					outcome := join()
					if outcome.Err == nil || outcome.Result != nil || outcome.Rows != nil {
						t.Fatal("terminated blocked command published", outcome)
					}
					if backend.PG {
						if !errors.Is(outcome.Err, driver.ErrBadConn) {
							t.Fatal(outcome.Err)
						}
					} else {
						want := databaseCreateDiagnostic("57P01", "terminating connection due to administrator command")
						want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
						want.Position = "13"
						fixture.AssertDiagnostic(t, outcome.Err, &want)
					}
					if _, err := one.ExecContext(p.Ctx, "SELECT 1"); err == nil {
						t.Fatal("FORCE left original physical owner usable")
					}
					c.absent(a)
					one.Close()
					two.Close()
					databaseDropClose(t, victims, raw)
					c.create(a, "", false)
					// The same held FORCE statement must resolve the new incarnation, not its
					// terminated owners. A raw physical connection preserves the FATAL fields.
					var native driver.Conn
					var wire *pgconn.PgConn
					if backend.PG {
						u, err := url.Parse(fixture.DSN)
						if err != nil {
							t.Fatal(err)
						}
						u.Path = "/" + a
						u.RawPath = ""
						wire, err = pgconn.Connect(p.Ctx, u.String())
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { wire.Close(context.Background()) })
					} else {
						native, err = p.NativeConnector.WithDatabase(a).Connect(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { native.Close() })
					}
					fixture.DatabaseReferenceOwners(t, p, admin, record, a, 1)
					forced.run(c, fixture.Exec(force, 0))
					var fatal error
					if wire != nil {
						fatal = wire.ExecParams(p.Ctx, "SELECT 1", nil, nil, nil, nil).Read().Err
					} else {
						_, fatal = native.(driver.ExecerContext).ExecContext(p.Ctx, "SELECT 1", nil)
					}
					want := databaseCreateDiagnostic("57P01", "terminating connection due to administrator command")
					want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
					fixture.AssertDiagnostic(t, fatal, &want)
					c.create(a, "", false)
					fresh, freshRaw, freshRecord := c.pool(a, "")
					fixture.RunStep(t, p.Ctx, fresh, freshRecord, fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('force_value','force_temp')", []string{"relname"}, []string{"NAME"}, nil))
					fixture.RunStep(t, p.Ctx, peer, peerRecord, fixture.Select("SELECT v FROM force_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
					databaseDropClose(t, fresh, freshRaw, peer, peerRaw)
				})
			}
		})
	}
}
