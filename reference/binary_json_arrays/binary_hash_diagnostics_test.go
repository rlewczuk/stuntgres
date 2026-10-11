package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func binaryHashDiagnostic(t *testing.T, p *fixture.Pair, db fixture.Executor, q string, want fixture.Diagnostic) {
	t.Helper()
	recorder := p.MemoryRecord
	if db == p.Postgres {
		recorder = p.PostgresRecord
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			step := fixture.ErrorDiagnostic(q, want)
			step.SelectRows = true
			step.ErrorStage = "execute"
			if prepared {
				step = fixture.Prepared(step)
				step.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, db, recorder, step)
		})
	}
}
func TestReferenceBinaryHashDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE hash_errors(v BYTEA,s TEXT,i INTEGER,b BOOLEAN)", 0))
			for _, populated := range []bool{false, true} {
				if populated {
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO hash_errors VALUES(NULL,NULL,NULL,NULL)", 1))
				}
				for _, name := range binaryHashNames {
					for _, arg := range []struct{ sql, typ string }{{"i", "integer"}, {"b", "boolean"}, {"", ""}, {"v,v", "bytea, bytea"}} {
						q := "SELECT " + name + "(" + arg.sql + ") FROM hash_errors"
						t.Run(fmt.Sprintf("%v/%s/%s", populated, name, arg.typ), func(t *testing.T) {
							binaryHashDiagnostic(t, p, backend.DB, q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function " + name + "(" + arg.typ + ") does not exist", Hint: fixture.BinaryHashHint, Position: "8"})
						})
					}
					if name != "md5" {
						t.Run(fmt.Sprintf("%v/%s/text", populated, name), func(t *testing.T) {
							binaryHashDiagnostic(t, p, backend.DB, "SELECT "+name+"(s) FROM hash_errors", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function " + name + "(text) does not exist", Hint: fixture.BinaryHashHint, Position: "8"})
						})
					}
				}
			}
			cases := []struct {
				q, code, msg, hint string
				pos                int
			}{
				{"SELECT crc32(1)", "42883", "function crc32(integer) does not exist", fixture.BinaryHashHint, 8},
				{"/*é*/ SELECT sha256(1)", "42883", "function sha256(integer) does not exist", fixture.BinaryHashHint, 14},
				{"SELECT public.sha256(v) FROM hash_errors", "42883", "function public.sha256(bytea) does not exist", fixture.BinaryHashHint, 8},
				{"SELECT absent.sha256(v) FROM hash_errors", "3F000", `schema "absent" does not exist`, "", 8},
				{`SELECT "SHA256"(v) FROM hash_errors`, "42883", `function SHA256(bytea) does not exist`, fixture.BinaryHashHint, 8},
				{"SELECT sha256(missing,v) FROM hash_errors", "42703", `column "missing" does not exist`, "", 15},
				{`SELECT sha256('\x0')`, "22023", "invalid hexadecimal data: odd number of digits", "", 15},
				{`SELECT sha256('\xGG')`, "22023", `invalid hexadecimal digit: "G"`, "", 15},
				{`SELECT sha256('\q')`, "22P02", "invalid input syntax for type bytea", "", 15},
				{`/*é*/ SELECT sha256('\xé0')`, "22023", `invalid hexadecimal digit: "é"`, "", 21},
			}
			for _, tc := range cases {
				t.Run(tc.q, func(t *testing.T) {
					binaryHashDiagnostic(t, p, backend.DB, tc.q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.msg, Hint: tc.hint, Position: fixture.Position(tc.pos)})
				})
			}
			q := "SELECT sha256(" + strings.TrimSuffix(strings.Repeat("v,", 101), ",") + ") FROM hash_errors"
			t.Run("argument limit", func(t *testing.T) {
				binaryHashDiagnostic(t, p, backend.DB, q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"})
			})
			s := fixture.Select("SELECT sha256($1)", []string{"sha256"}, []string{"BYTEA"}, [][]any{{binaryHashBytes(t, "b4b18d16ab476c66bc682878f8ab69acd05debf7f8da0c09a7acac462cd5e078")}}, []byte(`\x0`))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
		})
	}
}

func TestReferenceBinaryHashMD5Overloads(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE hash_text(v TEXT)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO hash_text VALUES('abc')", 1))
			for _, tc := range []struct {
				q    string
				want any
				args []any
			}{{"SELECT md5('abc')", binaryHashCrypto[1][0], nil}, {"SELECT md5('')", binaryHashCrypto[0][0], nil}, {"SELECT md5(NULL)", nil, nil}, {"SELECT md5(v) FROM hash_text", binaryHashCrypto[1][0], nil}, {"SELECT md5($1)", binaryHashCrypto[1][0], []any{"abc"}}} {
				t.Run(tc.q, func(t *testing.T) {
					s := fixture.Select(tc.q, []string{"md5"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.args...)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
				})
			}
			q := `SELECT md5('\x0')`
			s := fixture.Select(q, []string{"md5"}, []string{"TEXT"}, [][]any{{"d8e6a5626b620a84d69e8052951a080a"}})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
		})
	}
}

func TestReferenceBinaryHashQuoted(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE hash_quoted(v BYTEA)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO hash_quoted VALUES('abc')", 1))
			run := func(t *testing.T, step fixture.Step) {
				t.Helper()
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
						s := step
						if s.Diagnostic != nil {
							s.SelectRows = true
							s.ErrorStage = "execute"
						}
						if prepared {
							s = fixture.Prepared(s)
							if s.Diagnostic != nil {
								s.ErrorStage = "prepare"
							}
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					})
				}
			}
			row := binaryHashRow(t, 1, 891568578, 910901175)
			for i, name := range binaryHashNames {
				t.Run(name, func(t *testing.T) {
					t.Run("lowercase", func(t *testing.T) {
						run(t, fixture.Select(`SELECT "`+name+`"(v) FROM hash_quoted`, []string{name}, []string{binaryHashTypes[i]}, [][]any{{row[i]}}))
					})
					for _, spelling := range []string{strings.ToUpper(name), strings.ToUpper(name[:1]) + name[1:]} {
						t.Run(spelling, func(t *testing.T) {
							callee := `SELECT "` + spelling + `"(`
							t.Run("signature", func(t *testing.T) {
								run(t, fixture.ErrorDiagnostic(callee+"v) FROM hash_quoted", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function " + spelling + "(bytea) does not exist", Hint: fixture.BinaryHashHint, Position: "8"}))
							})
							t.Run("argument limit", func(t *testing.T) {
								run(t, fixture.ErrorDiagnostic(callee+strings.TrimSuffix(strings.Repeat("v,", 101), ",")+") FROM hash_quoted", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}))
							})
							t.Run("child precedence", func(t *testing.T) {
								run(t, fixture.ErrorDiagnostic(callee+"missing,"+strings.TrimSuffix(strings.Repeat("v,", 100), ",")+") FROM hash_quoted", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "missing" does not exist`, Position: fixture.Position(len(spelling) + 11)}))
							})
						})
					}
				})
			}
		})
	}
}
