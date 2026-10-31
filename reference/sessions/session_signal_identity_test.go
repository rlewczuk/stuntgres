package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionSignalIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ss_identity"})
	databases := []string{"ss_identity_database"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			pid := fixture.LockPID(t, p.Ctx, c)
			run(fixture.Exec("CREATE ROLE ss_identity", 0))
			for _, identity := range []string{"SET ROLE ss_identity", "RESET ROLE", "SET SESSION AUTHORIZATION ss_identity", "RESET SESSION AUTHORIZATION"} {
				run(fixture.Exec(identity, 0))
				if got := fixture.LockPID(t, p.Ctx, c); got != pid {
					t.Fatal(pid, got)
				}
			}
			stmt, e := c.PrepareContext(p.Ctx, "SELECT pg_backend_pid()")
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			check := func() {
				t.Helper()
				var got int
				if e := stmt.QueryRowContext(p.Ctx).Scan(&got); e != nil || got != pid {
					t.Fatal(pid, got, e)
				}
			}
			check()
			if !b.PG {
				var dump bytes.Buffer
				if e := stuntgres.Dump(p.Ctx, p.Native, &dump); e != nil {
					t.Fatal(e)
				}
				if e := stuntgres.Reset(p.Ctx, p.Native); e != nil {
					t.Fatal(e)
				}
				check()
				if e := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); e != nil {
					t.Fatal(e)
				}
				check()
			}
			run(fixture.Exec("CREATE DATABASE ss_identity_database", 0))
			otherPool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, "ss_identity_database", "")
			other, e := otherPool.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer other.Close()
			otherPID := fixture.LockPID(t, p.Ctx, other)
			if otherPID == pid {
				t.Fatal("simultaneous owners share identity", pid)
			}
			run(fixture.Select("SELECT pg_cancel_backend($1) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, otherPID))
			fixture.RunStep(t, p.Ctx, other, recorder, fixture.Select("SELECT 42", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(42)}}))
			run(fixture.Select("SELECT pg_terminate_backend($1,1000) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, otherPID))
			other.Close()
		})
	}
}

func TestReferenceSessionSignalSelf(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			pid := fixture.LockPID(t, p.Ctx, c)
			for _, prepared := range []bool{false, true} {
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}
				sessionSignalSelfQuery(t, p, b, c, prepared, "pg_cancel_backend", want)
				if got := fixture.LockPID(t, p.Ctx, c); got != pid {
					t.Fatal(pid, got)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 42", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(42)}}))
				var execErr error
				var result sql.Result
				if prepared {
					stmt, err := c.PrepareContext(p.Ctx, "SELECT pg_cancel_backend(pg_backend_pid())")
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					if fixture.LockPID(t, p.Ctx, c) != pid {
						t.Fatal("preparation changed or retired the self-cancellation owner")
					}
					result, execErr = stmt.ExecContext(p.Ctx)
					stmt.Close()
				} else {
					result, execErr = c.ExecContext(p.Ctx, "SELECT pg_cancel_backend(pg_backend_pid())")
				}
				if result != nil {
					t.Fatal("self-cancellation Exec published a successful result")
				}
				fixture.AssertDiagnostic(t, execErr, &want)
				if got := fixture.LockPID(t, p.Ctx, c); got != pid {
					t.Fatal(pid, got)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 42", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(42)}}))
			}
			// Every termination route gets a disposable physical victim. The cancellation
			// owner remains pinned as an independently observed surviving peer.
			seen := make(map[int]bool)
			for _, prepared := range []bool{false, true} {
				for _, queryRoute := range []bool{true, false} {
					t.Run(fmt.Sprintf("terminate/prepared=%t/query=%t", prepared, queryRoute), func(t *testing.T) {
						victim, e := b.DB.Conn(p.Ctx)
						if e != nil {
							t.Fatal(e)
						}
						defer victim.Close()
						victimPID := fixture.LockPID(t, p.Ctx, victim)
						if victimPID == pid || seen[victimPID] {
							t.Fatal("self-termination victim is not an independent physical owner", victimPID)
						}
						seen[victimPID] = true
						want := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"}
						if queryRoute {
							sessionSignalSelfQuery(t, p, b, victim, prepared, "pg_terminate_backend", want)
						} else {
							const query = "SELECT pg_terminate_backend(pg_backend_pid())"
							var result sql.Result
							var execErr error
							if prepared {
								stmt, err := victim.PrepareContext(p.Ctx, query)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								if fixture.LockPID(t, p.Ctx, victim) != victimPID {
									t.Fatal("preparation changed or retired the victim")
								}
								result, execErr = stmt.ExecContext(p.Ctx)
							} else {
								result, execErr = victim.ExecContext(p.Ctx, query)
							}
							if result != nil {
								t.Fatal("self-termination Exec published a successful result")
							}
							if b.PG {
								expected := error(driver.ErrBadConn)
								if prepared {
									expected = sql.ErrConnDone
								}
								if !errors.Is(execErr, expected) {
									t.Fatalf("pq self-termination Exec error=%v want %v", execErr, expected)
								}
							} else {
								fixture.AssertDiagnostic(t, execErr, &want)
							}
						}
						var value int
						if e := victim.QueryRowContext(p.Ctx, "SELECT 42").Scan(&value); e == nil {
							t.Fatal("self-terminated connection reused")
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 42", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(42)}}))
						if fixture.LockPID(t, p.Ctx, c) != pid {
							t.Fatal("self-termination retired the unrelated peer")
						}
					})
				}
			}
			if b.PG {
				// pq loses the FATAL at its physical-retirement boundary. Preserve separate
				// complete diagnostic proof on this same pinned oracle using public pgconn.
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("terminate/fatal_protocol/prepared=%t", prepared), func(t *testing.T) {
						sessionSignalSelfFatalProtocol(t, p, prepared)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 42", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(42)}}))
						if fixture.LockPID(t, p.Ctx, c) != pid {
							t.Fatal("fatal protocol probe retired the unrelated peer")
						}
					})
				}
			}
		})
	}
}

// Native eager execution reports the SQL diagnostic before publishing a cursor.
// pq streams one true row, then reports cancellation normally or loses FATAL as
// driver.ErrBadConn. The separate public pgconn witness retains full fatal proof.
func sessionSignalSelfQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, function string, want fixture.Diagnostic) {
	t.Helper()
	query := "SELECT " + function + "(pg_backend_pid())"
	var rows *sql.Rows
	var err error
	if prepared {
		pid := fixture.LockPID(t, p.Ctx, c)
		stmt, e := c.PrepareContext(p.Ctx, query)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		if fixture.LockPID(t, p.Ctx, c) != pid {
			t.Fatal("preparation changed or retired the self-signal owner")
		}
		rows, err = stmt.QueryContext(p.Ctx)
	} else {
		rows, err = c.QueryContext(p.Ctx, query)
	}
	if !b.PG {
		if rows != nil {
			rows.Close()
			t.Fatal("eager interrupted command published a cursor")
		}
		fixture.AssertDiagnostic(t, err, &want)
		return
	}
	if err != nil || rows == nil {
		t.Fatal("PostgreSQL streaming self-signal did not return its observed cursor", err)
	}
	defer rows.Close()
	signalMetadata(t, rows)
	columns, e := rows.Columns()
	if e != nil || len(columns) != 1 || columns[0] != function {
		t.Fatal(columns, e)
	}
	if !rows.Next() {
		t.Fatal("missing PostgreSQL pre-interrupt true row", rows.Err())
	}
	var sent bool
	if e := rows.Scan(&sent); e != nil || !sent {
		t.Fatal(sent, e)
	}
	if rows.Next() {
		t.Fatal("self-signal published an unexpected successful row")
	}
	err = rows.Err()
	if closeErr := rows.Close(); closeErr != nil {
		t.Fatal("self-signal cursor Close", closeErr)
	}
	if b.PG && want.Code == "57P01" {
		if !errors.Is(err, driver.ErrBadConn) {
			t.Fatalf("pq self-termination Query error=%v want driver.ErrBadConn", err)
		}
	} else {
		fixture.AssertDiagnostic(t, err, &want)
	}
}

// Like the fixture's fatal-timeout witness, this uses a fresh public protocol
// connection to the existing pinned server, never a second oracle or SQL shim.
func sessionSignalSelfFatalProtocol(t *testing.T, p *fixture.Pair, prepared bool) {
	t.Helper()
	conn, err := pgconn.Connect(p.Ctx, fixture.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(p.Ctx)
	const query = "SELECT pg_terminate_backend(pg_backend_pid())"
	if prepared {
		if _, err := conn.Prepare(p.Ctx, "session_signal_self_terminate", query, nil); err != nil {
			t.Fatal(err)
		}
		quiet := conn.ExecParams(p.Ctx, "SELECT 42", nil, nil, nil, nil).Read()
		if quiet.Err != nil || len(quiet.Rows) != 1 || len(quiet.Rows[0]) != 1 || string(quiet.Rows[0][0]) != "42" {
			t.Fatal("preparation was not effect-free", quiet)
		}
	}
	var result *pgconn.Result
	if prepared {
		result = conn.ExecPrepared(p.Ctx, "session_signal_self_terminate", nil, nil, nil).Read()
	} else {
		result = conn.ExecParams(p.Ctx, query, nil, nil, nil, nil).Read()
	}
	fields := result.FieldDescriptions
	if len(fields) != 1 || fields[0].Name != "pg_terminate_backend" || fields[0].DataTypeOID != 16 || fields[0].Format != 0 {
		t.Fatal("fatal protocol BOOL metadata", fields)
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) != 1 || string(result.Rows[0][0]) != "t" {
		t.Fatal("fatal protocol partial row", result.Rows)
	}
	if result.CommandTag.String() != "" {
		t.Fatal("self-termination published command completion", result.CommandTag)
	}
	fixture.AssertDiagnostic(t, result.Err, &fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"})
	if !conn.IsClosed() {
		t.Fatal("self-termination left protocol connection open")
	}
}
