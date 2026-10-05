//go:build reference

package stuntgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReferenceSession(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"memory", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
				t.Run(backend.name, func(t *testing.T) {
					a, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(c *sql.Conn, s referenceStep) {
						s.prepared = prepared
						referenceRunStep(t, p.ctx, c, backend.record, s)
					}
					show := func(c *sql.Conn, want string) {
						run(c, referenceExec("SHOW application_name", 0))
						run(c, referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{want}}))
					}
					show(a, "")
					show(b, "")
					run(a, referenceExec("CREATE TABLE sentinel(v INT)", 0))
					run(a, referenceExec(`CREATE TABLE set(show TEXT,session INT,"to" BOOL)`, 0))
					run(a, referenceExec(`INSERT INTO set VALUES('named',7,true)`, 1))
					run(b, referenceSelect(`SELECT show,session,"to" FROM set`, []string{"show", "session", "to"}, []string{"TEXT", "INT4", "BOOL"}, [][]any{{"named", int64(7), true}}))
					run(a, referenceExec("INSERT INTO sentinel VALUES(7)", 1))
					run(a, referenceExec("SET SESSION application_name TO 'session-a'", 0))
					run(b, referenceExec("SET application_name = 'session-b'", 0))
					show(a, "session-a")
					show(b, "session-b")
					for _, tc := range []struct{ input, want string }{
						{"DEFAULT", ""}, {"'DEFAULT'", "DEFAULT"}, {"true", "true"},
						{"+00042", "42"}, {"-00042", "-42"}, {"-00000", "0"},
						{"+0002147483647", "2147483647"}, {"-0002147483647", "-2147483647"},
						{"0002147483648", "0002147483648"}, {"+0002147483648", "0002147483648"},
						{"-0002147483648", "-0002147483648"}, {"+0002147483649", "0002147483649"},
						{"MixedCase", "mixedcase"}, {"'é'", `\xc3\xa9`},
						{"'" + strings.Repeat("a", 64) + "'", strings.Repeat("a", 63)},
						{"'" + strings.Repeat("a", 62) + "é'", strings.Repeat("a", 62)},
						{"'a\n\tb'", `a\x0a\x09b`}, {`"to"`, "to"},
						{`E'a\nb'`, `a\x0ab`},
					} {
						run(a, referenceExec("SET application_name TO "+tc.input, 0))
						show(a, tc.want)
						show(b, "session-b")
					}
					run(a, referenceExec(`SET "APPLICATION_NAME" TO quoted`, 0))
					show(a, "quoted")
					run(a, referenceSelect(`SHOW "APPLICATION_NAME"`, []string{"application_name"}, []string{"TEXT"}, [][]any{{"quoted"}}))
					run(a, referenceExec("SET application_name TO retained", 0))
					for _, tc := range []struct {
						query, code, message, position, stage string
						queryRows                             bool
					}{
						{"SET SESSION absent_setting TO x", "42704", `unrecognized configuration parameter "absent_setting"`, "", "execute", false},
						{"SHOW absent_setting", "42704", `unrecognized configuration parameter "absent_setting"`, "", "prepare", true},
						{`SET "a""b" TO x`, "42704", `unrecognized configuration parameter "a"b"`, "", "execute", false},
						{`SHOW "a""b"`, "42704", `unrecognized configuration parameter "a"b"`, "", "prepare", true},
						{`SET "a\b" TO x`, "42704", `unrecognized configuration parameter "a\b"`, "", "execute", false},
						{`SHOW "a\b"`, "42704", `unrecognized configuration parameter "a\b"`, "", "prepare", true},
						{"SET application_name TO 'a','b'", "22023", "SET application_name takes only one argument", "", "execute", false},
						{"SET absent_setting TO 'a','b'", "22023", "SET absent_setting takes only one argument", "", "execute", false},
						{`SET "APPLICATION_NAME" TO 'a','b'`, "22023", "SET APPLICATION_NAME takes only one argument", "", "execute", false},
						{"SET application_name TO NULL", "42601", `syntax error at or near "NULL"`, "25", "prepare", false},
						{"SET application_name TO $1", "42601", `syntax error at or near "$1"`, "25", "prepare", false},
						{"SET application_name TO TO", "42601", `syntax error at or near "TO"`, "25", "prepare", false},
						{"CREATE TABLE invalid_to(to BOOL)", "42601", `syntax error at or near "to"`, "25", "prepare", false},
					} {
						// This exact SQL places NULL at character 25.
						d := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
						step := referenceErrorDiagnostic(tc.query, d)
						step.selectRows = tc.queryRows
						step.errorStage = "execute"
						if prepared {
							step.errorStage = tc.stage
						}
						run(a, step)
						show(a, "retained")
						show(b, "session-b")
						run(b, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
					step := referenceErrorDiagnostic("SET SESSION application_name TO NULL", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "NULL"`, Position: "33"})
					step.errorStage = "execute"
					if prepared {
						step.errorStage = "prepare"
					}
					run(a, step)
					if prepared {
						stmt, err := a.PrepareContext(p.ctx, "SHOW application_name")
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						run(a, referenceExec("SET application_name TO changed", 0))
						var value string
						if err = stmt.QueryRowContext(p.ctx).Scan(&value); err != nil || value != "changed" {
							t.Fatal(value, err)
						}
					}
					if backend.name == "memory" {
						canceled, cancel := context.WithCancel(p.ctx)
						cancel()
						if _, err = a.ExecContext(canceled, "SET application_name TO lost"); !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					} else {
						referenceSessionCancel(t, p.ctx, a, prepared)
					}
					value := "retained"
					if prepared {
						value = "changed"
					}
					show(a, value)
					show(b, "session-b")
					run(a, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					c, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					var old any
					if err = c.Raw(func(raw any) error { old = raw; return nil }); err != nil {
						t.Fatal(err)
					}
					run(c, referenceExec("SET application_name TO pooled", 0))
					if err = c.Close(); err != nil {
						t.Fatal(err)
					}
					c, err = backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					if err = c.Raw(func(raw any) error {
						if raw != old {
							t.Fatal("pool reuse changed physical identity")
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					show(c, "pooled")
					if err = c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
						t.Fatal(err)
					}
					_ = c.Close()
					c, err = backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					if err = c.Raw(func(raw any) error {
						if raw == old {
							t.Fatal("physical replacement retained identity")
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					show(c, "")
					show(b, "session-b")
					run(c, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}

// PostgreSQL statement cancellation preserves its session. lib/pq cancellation
// through a Go context can instead close the transport, and a fast SET can win
// that race even with a pre-canceled context. Use server-side cancellation after
// an observed admission barrier, not a timing assumption about either driver.
func referenceSessionCancel(t *testing.T, parent context.Context, conn *sql.Conn, prepared bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	var pid int
	if err := conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	const query = "SELECT pg_sleep(600)"
	var stmt *sql.Stmt
	if prepared {
		var err error
		stmt, err = conn.PrepareContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
	}
	result := make(chan error, 1)
	go func() {
		var err error
		if stmt != nil {
			_, err = stmt.ExecContext(ctx)
		} else {
			_, err = conn.ExecContext(ctx, query)
		}
		result <- err
	}()
	for {
		var active bool
		err := referenceAdmin.QueryRowContext(ctx,
			"SELECT COALESCE((SELECT state = 'active' AND query = $2 FROM pg_stat_activity WHERE pid = $1), FALSE)",
			pid, query).Scan(&active)
		if err != nil {
			cancel()
			statementErr := <-result
			t.Fatalf("cancellation admission barrier: %v; statement: %v", err, statementErr)
		}
		if active {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("statement completed before cancellation admission: %v", err)
		default:
		}
	}
	var signaled bool
	if err := referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
		cancel()
		statementErr := <-result
		t.Fatalf("cancel backend: signaled=%t error=%v; statement: %v", signaled, err, statementErr)
	}
	err := <-result
	referenceAssertDiagnostic(t, err, &referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014",
		Message: "canceling statement due to user request",
	})
}
