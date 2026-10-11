package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSession(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(c *sql.Conn, s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
					}
					show := func(c *sql.Conn, want string) {
						run(c, fixture.Exec("SHOW application_name", 0))
						run(c, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{want}}))
					}
					show(a, "")
					show(b, "")
					run(a, fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
					run(a, fixture.Exec(`CREATE TABLE set(show TEXT,session INT,"to" BOOL)`, 0))
					run(a, fixture.Exec(`INSERT INTO set VALUES('named',7,true)`, 1))
					run(b, fixture.Select(`SELECT show,session,"to" FROM set`, []string{"show", "session", "to"}, []string{"TEXT", "INT4", "BOOL"}, [][]any{{"named", int64(7), true}}))
					run(a, fixture.Exec("INSERT INTO sentinel VALUES(7)", 1))
					run(a, fixture.Exec("SET SESSION application_name TO 'session-a'", 0))
					run(b, fixture.Exec("SET application_name = 'session-b'", 0))
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
						{"'1_000'", "1_000"}, {"'0x2a'", "0x2a"},
						{"'+0O_1_2'", "+0O_1_2"}, {"'-0b0'", "-0b0"},
					} {
						run(a, fixture.Exec("SET application_name TO "+tc.input, 0))
						show(a, tc.want)
						show(b, "session-b")
					}
					run(a, fixture.Exec(`SET "APPLICATION_NAME" TO quoted`, 0))
					show(a, "quoted")
					run(a, fixture.Select(`SHOW "APPLICATION_NAME"`, []string{"application_name"}, []string{"TEXT"}, [][]any{{"quoted"}}))
					run(a, fixture.Exec("SET application_name TO retained", 0))
				})
			}
		})
	}
}

func TestReferenceSessionDiagnosticsAndPreparedLiveValue(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(c *sql.Conn, s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
					}
					show := func(c *sql.Conn, want string) {
						run(c, fixture.Exec("SHOW application_name", 0))
						run(c, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{want}}))
					}
					show(a, "")
					show(b, "")
					run(a, fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
					run(a, fixture.Exec(`CREATE TABLE set(show TEXT,session INT,"to" BOOL)`, 0))
					run(a, fixture.Exec(`INSERT INTO set VALUES('named',7,true)`, 1))
					run(b, fixture.Select(`SELECT show,session,"to" FROM set`, []string{"show", "session", "to"}, []string{"TEXT", "INT4", "BOOL"}, [][]any{{"named", int64(7), true}}))
					run(a, fixture.Exec("INSERT INTO sentinel VALUES(7)", 1))
					run(a, fixture.Exec("SET SESSION application_name TO 'session-a'", 0))
					run(b, fixture.Exec("SET application_name = 'session-b'", 0))
					show(a, "session-a")
					show(b, "session-b")
					if prepared {
						fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Exec("SET application_name TO retained", 0))
					}
					run(a, fixture.Exec("SET application_name TO retained", 0))
					for _, tc := range []struct {
						query, code, message, position, stage string
						queryRows                             bool
					}{
						{"SET absent_setting TO x", "42704", `unrecognized configuration parameter "absent_setting"`, "", "execute", false},
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
						d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
						step := fixture.ErrorDiagnostic(tc.query, d)
						step.SelectRows = tc.queryRows
						step.ErrorStage = "execute"
						if prepared {
							step.ErrorStage = tc.stage
						}
						run(a, step)
						if tc.queryRows {
							step.SelectRows = false
							run(a, step)
						}
						show(a, "retained")
						show(b, "session-b")
						run(b, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
					step := fixture.ErrorDiagnostic("SET SESSION application_name TO NULL", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "NULL"`, Position: "33"})
					step.ErrorStage = "execute"
					if prepared {
						step.ErrorStage = "prepare"
					}
					run(a, step)
					if prepared {
						stmt, err := a.PrepareContext(p.Ctx, "SHOW application_name")
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						for _, value := range []string{"first", "second"} {
							run(a, fixture.Exec("SET application_name TO "+value, 0))
							var observed string
							if err = stmt.QueryRowContext(p.Ctx).Scan(&observed); err != nil || observed != value {
								t.Fatal(observed, err)
							}
						}
					}
					if backend.Name == "memory" {
						canceled, cancel := context.WithCancel(p.Ctx)
						cancel()
						if _, err = a.ExecContext(canceled, "SET application_name TO lost"); !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					} else {
						fixture.SessionCancel(t, p.Ctx, a, prepared)
					}
					value := "retained"
					if prepared {
						value = "second"
					}
					show(a, value)
					show(b, "session-b")
					run(a, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}

func TestReferenceSessionPhysicalIsolationReuseAndReplacement(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(c *sql.Conn, s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
					}
					show := func(c *sql.Conn, want string) {
						run(c, fixture.Exec("SHOW application_name", 0))
						run(c, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{want}}))
					}
					show(a, "")
					show(b, "")
					run(a, fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
					run(a, fixture.Exec(`CREATE TABLE set(show TEXT,session INT,"to" BOOL)`, 0))
					run(a, fixture.Exec(`INSERT INTO set VALUES('named',7,true)`, 1))
					run(b, fixture.Select(`SELECT show,session,"to" FROM set`, []string{"show", "session", "to"}, []string{"TEXT", "INT4", "BOOL"}, [][]any{{"named", int64(7), true}}))
					run(a, fixture.Exec("INSERT INTO sentinel VALUES(7)", 1))
					run(a, fixture.Exec("SET SESSION application_name TO 'session-a'", 0))
					run(b, fixture.Exec("SET application_name = 'session-b'", 0))
					show(a, "session-a")
					show(b, "session-b")
					c, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					var old any
					if err = c.Raw(func(raw any) error { old = raw; return nil }); err != nil {
						t.Fatal(err)
					}
					run(c, fixture.Exec("SET application_name TO pooled", 0))
					if err = c.Close(); err != nil {
						t.Fatal(err)
					}
					c, err = backend.DB.Conn(p.Ctx)
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
					c, err = backend.DB.Conn(p.Ctx)
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
					run(c, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					var sentinel int64
					if err = c.QueryRowContext(p.Ctx, "SELECT v FROM sentinel").Scan(&sentinel); err != nil || sentinel != 7 {
						t.Fatal("replacement sentinel", sentinel, err)
					}
				})
			}
		})
	}
}
