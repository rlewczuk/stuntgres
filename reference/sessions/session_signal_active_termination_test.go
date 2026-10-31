package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionSignalActiveTermination(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			t.Run("database_sql", func(t *testing.T) {
				sessionSignalActiveTermination(t, p, b, false)
			})
			if b.PG {
				// pq's Exec boundary loses the FATAL. A real second target on
				// the same server preserves the full diagnostic through pgconn.
				t.Run("fatal_protocol", func(t *testing.T) {
					sessionSignalActiveTermination(t, p, b, true)
				})
			}
		})
	}
}

func sessionSignalActiveTermination(t *testing.T, p *fixture.Pair, b fixture.Backend, protocol bool) {
	t.Helper()
	open := func() *sql.Conn {
		t.Helper()
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	owner, caller := open(), open()
	run := func(c *sql.Conn, s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	table := "session_signal_active"
	if protocol {
		table += "_protocol"
	}
	waitTable := table + "_wait"
	run(owner, fixture.Exec("CREATE TABLE "+table+"(v integer)", 0))
	run(owner, fixture.Exec("INSERT INTO "+table+" VALUES(1)", 1))
	run(owner, fixture.Exec("CREATE TABLE "+waitTable+"(v integer)", 0))
	holder := fixture.LockPID(t, p.Ctx, owner)

	var victim *sql.Conn
	var wire *pgconn.PgConn
	var pid int
	if protocol {
		var err error
		wire, err = pgconn.Connect(p.Ctx, fixture.DSN)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { wire.Close(p.Ctx) })
		identity := wire.ExecParams(p.Ctx, "SELECT pg_backend_pid()", nil, nil, nil, nil).Read()
		if identity.Err != nil || len(identity.Rows) != 1 || len(identity.Rows[0]) != 1 {
			t.Fatal("active protocol target identity", identity)
		}
		pid, err = strconv.Atoi(string(identity.Rows[0][0]))
		if err != nil || pid <= 0 {
			t.Fatal(pid, err)
		}
	} else {
		victim = open()
		pid = fixture.LockPID(t, p.Ctx, victim)
	}
	victimExec := func(query string, count int64, tag string) {
		t.Helper()
		if wire == nil {
			run(victim, fixture.Exec(query, count))
			return
		}
		result := wire.ExecParams(p.Ctx, query, nil, nil, nil, nil).Read()
		if result.Err != nil || len(result.Rows) != 0 || result.CommandTag.String() != tag {
			t.Fatal("active protocol setup", query, result)
		}
	}
	victimExec("BEGIN", 0, "BEGIN")
	t.Cleanup(func() {
		if wire != nil {
			if !wire.IsClosed() {
				wire.ExecParams(p.Ctx, "ROLLBACK", nil, nil, nil, nil).Read()
			}
		} else {
			victim.ExecContext(context.Background(), "ROLLBACK")
		}
	})
	victimExec("INSERT INTO "+table+" VALUES(9)", 1, "INSERT 0 1")
	// Both API routes retain the real uncommitted write while the next INSERT
	// queues behind this independent owner's actual relation lock.
	relation := int64(0)
	if b.PG {
		relation = fixture.LockOID(t, p.Ctx, waitTable)
	}
	run(owner, fixture.Exec("BEGIN", 0))
	t.Cleanup(func() { owner.ExecContext(context.Background(), "ROLLBACK") })
	run(owner, fixture.Exec("LOCK TABLE "+waitTable+" IN ACCESS EXCLUSIVE MODE", 0))
	finished := make(chan struct{})
	var terminal *pgconn.Result
	join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		query := "INSERT INTO " + waitTable + " VALUES(9)"
		if wire != nil {
			terminal = wire.ExecParams(ctx, query, nil, nil, nil, nil).Read()
			return fixture.RelationOutcome{Err: terminal.Err}
		}
		result, err := victim.ExecContext(ctx, query)
		return fixture.RelationOutcome{Result: result, Err: err}
	})
	fixture.RelationLockPending(t, p, b, pid, holder, relation, "RowExclusiveLock", finished)
	run(caller, fixture.Select("SELECT pg_terminate_backend($1,1000) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, pid))
	outcome := join()
	if outcome.Result != nil || outcome.Rows != nil {
		t.Fatal("terminated active command published output")
	}
	if b.PG && wire == nil {
		if !errors.Is(outcome.Err, driver.ErrBadConn) {
			t.Fatal("pq active termination boundary", outcome.Err)
		}
	} else {
		// This unprepared INSERT is interrupted while opening its named
		// relation; both native admission and PostgreSQL Parse own token 13.
		fixture.AssertDiagnostic(t, outcome.Err, &fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command", Position: fixture.Position(13)})
	}
	if wire != nil {
		if terminal == nil || len(terminal.Rows) != 0 || terminal.CommandTag.String() != "" {
			t.Fatal("fatal protocol command published output/completion", terminal)
		}
		if !wire.IsClosed() {
			t.Fatal("terminated active protocol target remains open")
		}
	}
	run(caller, fixture.Select("SELECT v FROM "+table, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
	run(caller, fixture.Exec("BEGIN", 0))
	run(caller, fixture.Exec("LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE", 0))
	run(caller, fixture.Exec("COMMIT", 0))
	run(owner, fixture.Exec("ROLLBACK", 0))
	run(caller, fixture.Select("SELECT count(*) FROM "+waitTable, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	run(caller, fixture.Select("SELECT 42", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(42)}}))
}
