package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFormatDiagnostics(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hint := `For a single "%" use "%%".`
		for _, pattern := range []string{"%", "%1", "%1$", "%-", "%*", "%*1", "%*1$"} {
			t.Run(pattern, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22023", "unterminated format() type specifier")
				d.Hint = hint
				formatReferenceFailure(t, p, b, c, "SELECT format('"+pattern+"')", d, false)
			})
		}
		for _, tc := range []struct{ pattern, bad string }{
			{"%d", "d"}, {"%i", "i"}, {"%l", "l"}, {"%S", "S"},
			{"%5%", "%"}, {"%1$%", "%"}, {"%+s", "+"}, {"% s", " "},
			{"%.2s", "."}, {"%é", "é"}, {"%🙂", "🙂"}, {"%1-s", "-"},
			{"%*d", "d"}, {"%1$*2$d", "d"},
		} {
			t.Run(tc.pattern, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22023", `unrecognized format() type specifier "`+tc.bad+`"`)
				d.Hint = hint
				for _, args := range []string{"", ",'bad'", ",NULL,NULL"} {
					t.Run(args, func(t *testing.T) {
						formatReferenceFailure(t, p, b, c, "/*é🙂*/ SELECT format('"+tc.pattern+"'"+args+")", d, false)
					})
				}
			})
		}
		for _, tc := range []struct{ call, code, message string }{
			{"format('%0$s','x')", "22023", "format specifies argument 0, but arguments are numbered from 1"},
			{"format('%*0$s',5,'x')", "22023", "format specifies argument 0, but arguments are numbered from 1"},
			{"format('%*1s',5,'x')", "22023", `width argument position must be ended by "$"`},
			{"format('%s')", "22023", "too few arguments for format()"},
			{"format('%2$s','x')", "22023", "too few arguments for format()"},
			{"format('%*s')", "22023", "too few arguments for format()"},
			{"format('%*s',5)", "22023", "too few arguments for format()"},
			{"format('%*3$s','x',5)", "22023", "too few arguments for format()"},
			{"format('%2147483648s','x')", "22003", "number is out of range"},
			{"format('%2147483648$s','x')", "22003", "number is out of range"},
			{"format('%*2147483648$s',5,'x')", "22003", "number is out of range"},
			{"format('%2147483648')", "22003", "number is out of range"},
			{"format('%*2147483648')", "22003", "number is out of range"},
			{"format('%*s','bad','x')", "22P02", `invalid input syntax for type integer: "bad"`},
			{"format('%*s','bad')", "22P02", `invalid input syntax for type integer: "bad"`},
			{"format('%*s',2147483648::bigint,'x')", "22003", `value "2147483648" is out of range for type integer`},
			{"format('%*s',5.0::numeric,'x')", "22P02", `invalid input syntax for type integer: "5.0"`},
			{"format('%*s',true,'x')", "22P02", `invalid input syntax for type integer: "t"`},
			{"format('%*s',(-2147483648)::integer,'x')", "22003", "number is out of range"},
			{"format('%*I',(-2147483648)::integer,NULL)", "22004", "null values cannot be formatted as an SQL identifier"},
			{"format('%I',NULL)", "22004", "null values cannot be formatted as an SQL identifier"},
		} {
			t.Run(tc.call, func(t *testing.T) {
				formatReferenceFailure(t, p, b, c, "/*é🙂*/ SELECT "+tc.call, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), false)
			})
		}
	})
}

func TestReferenceFormatSignatures(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ call, signature string }{
			{"format()", "format()"}, {"format(1)", "format(integer)"},
			{"format(true)", "format(boolean)"}, {`"FORMAT"('x')`, "FORMAT(unknown)"},
			{"public.format('x')", "public.format(unknown)"},
			{"format(NULL,absent_format_child())", "absent_format_child()"},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.call, func(t *testing.T) {
					q := prefix + "SELECT " + tc.call
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					pos := 8 + len([]rune(prefix))
					if strings.Contains(tc.call, "absent_format_child") {
						pos = len([]rune(q[:strings.Index(q, "absent_format_child")])) + 1
					}
					d.Position = fixture.Position(pos)
					formatReferenceFailure(t, p, b, c, q, d, true)
				})
			}
		}
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			q := prefix + "SELECT sprintf('%s','x')"
			native := fixture.ControlsReferenceDiagnostic("0A000", "functions are unsupported")
			native.Position = fixture.Position(8 + len([]rune(prefix)))
			postgres := fixture.ControlsReferenceDiagnostic("42883", "function sprintf(unknown, unknown) does not exist")
			postgres.Position = native.Position
			postgres.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic(q, native)
				step.PostgresDiagnostic = &postgres
				if b.PG {
					step.State = postgres.Code
				}
				step.SelectRows, step.ErrorStage = true, "execute"
				if prepared {
					step = fixture.Prepared(step)
					step.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}
		}
		for _, operand := range []string{"1", "NULL", "'x'::text"} {
			t.Run("variadic-"+operand, func(t *testing.T) {
				q := "SELECT format('%s',VARIADIC " + operand + ")"
				d := fixture.ControlsReferenceDiagnostic("42804", "VARIADIC argument must be an array")
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, operand+")")])) + 1)
				formatReferenceFailure(t, p, b, c, q, d, true)
			})
		}
		t.Run("namespace", func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic("3F000", `schema "format_missing" does not exist`)
			d.Position = "8"
			formatReferenceFailure(t, p, b, c, "SELECT format_missing.format('x')", d, true)
		})
		for _, n := range []int{100, 101} {
			t.Run(fixture.Position(n)+"-arguments", func(t *testing.T) {
				q := "SELECT format('ok'" + strings.Repeat(",NULL", n-1) + ")"
				if n == 100 {
					formatReferenceBoth(t, p, b, c, fixture.Select(q, []string{"format"}, []string{"TEXT"}, [][]any{{"ok"}}))
				} else {
					d := fixture.ControlsReferenceDiagnostic("54023", "cannot pass more than 100 arguments to a function")
					d.Position = "8"
					formatReferenceFailure(t, p, b, c, q, d, true)
				}
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE format(format text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO format VALUES('column')", 1))
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT format AS format FROM format", []string{"format"}, []string{"TEXT"}, [][]any{{"column"}}))
	})
}

func TestReferenceFormatParameters(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT format('%s',$1)", "42P18", "could not determine data type of parameter $1", ""},
			{"SELECT format('%*s',$1,'x')", "42P18", "could not determine data type of parameter $1", ""},
			{"SELECT format(NULL,$1)", "42P18", "could not determine data type of parameter $1", ""},
			{"SELECT format('%s',$1),$1", "42P08", "could not determine data type of parameter $1", "20"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = tc.position
				s := fixture.Prepared(fixture.ErrorDiagnostic(tc.q, d))
				s.SelectRows, s.ErrorStage = true, "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		q := "SELECT format($1,$2::text) AS rendered"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("quiet format Prepare", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			pattern string
			value   any
			want    any
		}{
			{"%s", "x", "x"}, {"%L", nil, "NULL"}, {"%%", "unused", "%"}, {"", "x", ""},
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"rendered"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.pattern, tc.value))
		}
		d := fixture.ControlsReferenceDiagnostic("22023", `unrecognized format() type specifier "d"`)
		d.Hint = `For a single "%" use "%%".`
		s := fixture.ErrorDiagnostic(q, d, "%d", "x")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"rendered"}, []string{"TEXT"}, [][]any{{"after"}}, "%s", "after"))
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT format($1)", []string{"format"}, []string{"TEXT"}, [][]any{{"literal"}}, "literal"))
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "late-any-direct", true: "late-any-prepared"}[prepared], func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42P08", "could not determine data type of parameter $1")
				d.Position = "23"
				s := fixture.ErrorDiagnostic("SELECT $1,format('%s',$1)", d, "x")
				s.SelectRows, s.ErrorStage = true, "execute"
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		formatReferenceBoth(t, p, b, c, fixture.Select("SELECT format('|%*s|',$1::integer,$2::text)", []string{"format"}, []string{"TEXT"}, [][]any{{"|   x|"}}, 4, "x"))
	})
}
