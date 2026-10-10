package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func stringPadFailure(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, q string, d referenceDiagnostic, analysis bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := referenceErrorDiagnostic(q, d)
		s.selectRows = true
		s.errorStage = "execute"
		if prepared {
			s = referencePrepared(s)
			if analysis {
				s.errorStage = "prepare"
			}
		}
		referenceRunStep(t, p.ctx, c, b.recorder, s)
	}
}

func TestReferenceStringPadDiagnostics(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		cases := []struct{ call, signature string }{
			{"lpad()", "lpad()"}, {"lpad('a')", "lpad(unknown)"}, {"lpad('a',2,'x','y')", "lpad(unknown, integer, unknown, unknown)"},
			{"rpad()", "rpad()"}, {"rpad('a')", "rpad(unknown)"}, {"rpad('a',2,'x','y')", "rpad(unknown, integer, unknown, unknown)"},
			{"repeat()", "repeat()"}, {"repeat('a')", "repeat(unknown)"}, {"repeat('a',2,3)", "repeat(unknown, integer, integer)"},
			{"reverse()", "reverse()"}, {"reverse('a','b')", "reverse(unknown, unknown)"}, {"reverse(1)", "reverse(integer)"},
			{`"REVERSE"('a')`, "REVERSE(unknown)"}, {"lpad(NULL,2,true)", "lpad(unknown, integer, boolean)"},
		}
		for _, family := range []struct {
			name  string
			arity int
		}{{"lpad", 2}, {"lpad", 3}, {"rpad", 2}, {"rpad", 3}, {"repeat", 2}, {"reverse", 1}} {
			for slot := range family.arity {
				wrong := []struct{ sql, typ string }{{"1", "integer"}, {"true", "boolean"}, {"'a'::bytea", "bytea"}, {"1::numeric", "numeric"}}
				if slot == 1 {
					wrong = []struct{ sql, typ string }{{"2::bigint", "bigint"}, {"2::numeric", "numeric"}, {"2::double precision", "double precision"}, {"'2'::text", "text"}, {"true", "boolean"}, {"'a'::bytea", "bytea"}, {"2147483648", "bigint"}}
				}
				for _, w := range wrong {
					if family.name == "reverse" && w.typ == "bytea" {
						continue
					}
					args, types := make([]string, family.arity), make([]string, family.arity)
					for i := range args {
						args[i] = "'a'"
						types[i] = "unknown"
						if i == 1 {
							args[i] = "2"
							types[i] = "integer"
						}
					}
					args[slot] = w.sql
					types[slot] = w.typ
					cases = append(cases, struct{ call, signature string }{family.name + "(" + strings.Join(args, ",") + ")", family.name + "(" + strings.Join(types, ", ") + ")"})
				}
			}
		}
		for _, tc := range cases {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.call, func(t *testing.T) {
					d := controlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					d.Position = referencePosition(8 + len([]rune(prefix)))
					stringPadFailure(t, p, b, c, prefix+"SELECT "+tc.call, d, true)
				})
			}
		}
		for _, tc := range []struct {
			q, code, message string
			position         int
		}{
			{"SELECT lpad('a','bad')", "22P02", `invalid input syntax for type integer: "bad"`, 17},
			{"SELECT repeat('a','2147483648')", "22003", `value "2147483648" is out of range for type integer`, 19},
			{"SELECT reverse(missing)", "42703", `column "missing" does not exist`, 16},
			{"SELECT no_pad_schema.reverse('a')", "3F000", `schema "no_pad_schema" does not exist`, 8},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.q, func(t *testing.T) {
					d := controlsReferenceDiagnostic(tc.code, tc.message)
					d.Position = referencePosition(tc.position + len([]rune(prefix)))
					stringPadFailure(t, p, b, c, prefix+tc.q, d, true)
				})
			}
		}
		for _, name := range []string{"lpad", "rpad", "repeat"} {
			for _, tc := range []struct{ arg, code, message string }{
				{"bad", "22P02", `invalid input syntax for type integer: "bad"`},
				{"2147483648", "22003", `value "2147483648" is out of range for type integer`},
			} {
				t.Run(name+tc.arg, func(t *testing.T) {
					q := "SELECT " + name + "($1,$2) AS v"
					stmt, err := c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					d := controlsReferenceDiagnostic(tc.code, tc.message)
					d.Context = "unnamed portal parameter $2 = '...'"
					s := referenceErrorDiagnostic(q, d, "a", tc.arg)
					s.selectRows = true
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, b.recorder, s)
				})
			}
		}
		for _, tc := range []struct{ arg, message string }{
			{string([]byte{'a', 0}), `invalid byte sequence for encoding "UTF8": 0x00`},
			{string([]byte{0xff}), `invalid byte sequence for encoding "UTF8": 0xff`},
		} {
			t.Run(tc.message, func(t *testing.T) {
				q := "SELECT reverse($1)"
				d := controlsReferenceDiagnostic("22021", tc.message)
				d.Context = "unnamed portal parameter $1"
				s := referenceErrorDiagnostic(q, d, tc.arg)
				s.selectRows = true
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, c, b.recorder, s)
				referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
			})
		}
	})
}

func TestReferenceStringPadResourceLimits(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			name string
			n    int
		}{{"lpad", 268435455}, {"rpad", 268435455}, {"repeat", 1073741820}} {
			for _, threeArgs := range []bool{false, true} {
				if tc.name == "repeat" && threeArgs {
					continue
				}
				label := tc.name + "/two-argument"
				d := controlsReferenceDiagnostic("54000", "requested length too large")
				if tc.name != "repeat" {
					d.Context = "SQL function \"" + tc.name + "\" statement 1"
				}
				q := "SELECT " + tc.name + "($1,$2) AS v"
				args := []any{"x", tc.n}
				constant := "SELECT " + tc.name + "('x'," + referencePosition(tc.n) + ")"
				if threeArgs {
					label = tc.name + "/three-argument"
					d.Context = ""
					q = "SELECT " + tc.name + "($1,$2,$3) AS v"
					args = append(args, "x")
					constant = "SELECT " + tc.name + "('x'," + referencePosition(tc.n) + ",'x')"
				}
				t.Run(label, func(t *testing.T) {
					stmt, err := c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					s := referenceErrorDiagnostic(q, d, args...)
					s.selectRows = true
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, b.recorder, s)
					stringPadFailure(t, p, b, c, constant, d, false)
				})
			}
		}
	})
}

func TestReferenceStringPadCollation(t *testing.T) {
	stringPadScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{referenceExec(`CREATE TABLE sp_coll(id int,a text COLLATE "C",b text COLLATE "POSIX")`, 0), referenceExec("INSERT INTO sp_coll VALUES(1,'ab','x'),(2,NULL,'x')", 2)} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		for _, tc := range []struct {
			expr string
			rows [][]any
		}{
			{"lpad(a,4,b)", [][]any{{"xxab"}, {nil}}}, {"rpad(a,4,b)", [][]any{{"abxx"}, {nil}}},
			{"repeat(a||b,2)", [][]any{{"abxabx"}, {nil}}}, {"reverse(a||b)", [][]any{{"xba"}, {nil}}},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				stringPadBoth(t, p, b, c, referenceSelect("SELECT "+tc.expr+" AS v FROM sp_coll ORDER BY id", []string{"v"}, []string{"TEXT"}, tc.rows))
				d := controlsReferenceDiagnostic("42P22", "could not determine which collation to use for string comparison")
				d.Hint = "Use the COLLATE clause to set the collation explicitly."
				stringPadFailure(t, p, b, c, "SELECT starts_with("+tc.expr+",'x') FROM sp_coll WHERE id=1", d, false)
			})
		}
		for _, expr := range []string{`lpad(NULL::text COLLATE "C",2,'x' COLLATE "POSIX")`, `rpad('a' COLLATE "C",2,'x' COLLATE "POSIX")`, `repeat(('a' COLLATE "C")||('x' COLLATE "POSIX"),2)`, `reverse(('a' COLLATE "C")||('x' COLLATE "POSIX"))`} {
			t.Run(expr, func(t *testing.T) {
				q := "SELECT " + expr
				d := controlsReferenceDiagnostic("42P21", `collation mismatch between explicit collations "C" and "POSIX"`)
				d.Position = referencePosition(len([]rune(q[:strings.LastIndex(q, "COLLATE")])) + 1)
				stringPadFailure(t, p, b, c, q, d, true)
			})
		}
		q := `SELECT reverse('\x00'::bytea COLLATE "C")`
		d := controlsReferenceDiagnostic("42804", `collations are not supported by type bytea`)
		d.Position = "30"
		stringPadFailure(t, p, b, c, q, d, true)
	})
}
