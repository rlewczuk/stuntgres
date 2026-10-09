package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceFormatDiagnostics(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		hint := `For a single "%" use "%%".`
		for _, pattern := range []string{"%", "%1", "%1$", "%-", "%*", "%*1", "%*1$"} {
			t.Run(pattern, func(t *testing.T) {
				d := controlsReferenceDiagnostic("22023", "unterminated format() type specifier")
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
				d := controlsReferenceDiagnostic("22023", `unrecognized format() type specifier "`+tc.bad+`"`)
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
				formatReferenceFailure(t, p, b, c, "/*é🙂*/ SELECT "+tc.call, controlsReferenceDiagnostic(tc.code, tc.message), false)
			})
		}
	})
}

func TestReferenceFormatSignatures(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ call, signature string }{
			{"format()", "format()"}, {"format(1)", "format(integer)"},
			{"format(true)", "format(boolean)"}, {`"FORMAT"('x')`, "FORMAT(unknown)"},
			{"public.format('x')", "public.format(unknown)"},
			{"format(NULL,absent_format_child())", "absent_format_child()"},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.call, func(t *testing.T) {
					q := prefix + "SELECT " + tc.call
					d := controlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					pos := 8 + len([]rune(prefix))
					if strings.Contains(tc.call, "absent_format_child") {
						pos = len([]rune(q[:strings.Index(q, "absent_format_child")])) + 1
					}
					d.Position = referencePosition(pos)
					formatReferenceFailure(t, p, b, c, q, d, true)
				})
			}
		}
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			q := prefix + "SELECT sprintf('%s','x')"
			native := controlsReferenceDiagnostic("0A000", "functions are unsupported")
			native.Position = referencePosition(8 + len([]rune(prefix)))
			postgres := controlsReferenceDiagnostic("42883", "function sprintf(unknown, unknown) does not exist")
			postgres.Position = native.Position
			postgres.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, prepared := range []bool{false, true} {
				step := referenceErrorDiagnostic(q, native)
				step.postgresDiagnostic = &postgres
				if b.pg {
					step.state = postgres.Code
				}
				step.selectRows, step.errorStage = true, "execute"
				if prepared {
					step = referencePrepared(step)
					step.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, c, b.recorder, step)
			}
		}
		for _, operand := range []string{"1", "NULL", "'x'::text"} {
			t.Run("variadic-"+operand, func(t *testing.T) {
				q := "SELECT format('%s',VARIADIC " + operand + ")"
				d := controlsReferenceDiagnostic("42804", "VARIADIC argument must be an array")
				d.Position = referencePosition(len([]rune(q[:strings.Index(q, operand+")")])) + 1)
				formatReferenceFailure(t, p, b, c, q, d, true)
			})
		}
		t.Run("namespace", func(t *testing.T) {
			d := controlsReferenceDiagnostic("3F000", `schema "format_missing" does not exist`)
			d.Position = "8"
			formatReferenceFailure(t, p, b, c, "SELECT format_missing.format('x')", d, true)
		})
		for _, n := range []int{100, 101} {
			t.Run(referencePosition(n)+"-arguments", func(t *testing.T) {
				q := "SELECT format('ok'" + strings.Repeat(",NULL", n-1) + ")"
				if n == 100 {
					formatReferenceBoth(t, p, b, c, referenceSelect(q, []string{"format"}, []string{"TEXT"}, [][]any{{"ok"}}))
				} else {
					d := controlsReferenceDiagnostic("54023", "cannot pass more than 100 arguments to a function")
					d.Position = "8"
					formatReferenceFailure(t, p, b, c, q, d, true)
				}
			})
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE format(format text)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO format VALUES('column')", 1))
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT format AS format FROM format", []string{"format"}, []string{"TEXT"}, [][]any{{"column"}}))
	})
}

func TestReferenceFormatParameters(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT format('%s',$1)", "42P18", "could not determine data type of parameter $1", ""},
			{"SELECT format('%*s',$1,'x')", "42P18", "could not determine data type of parameter $1", ""},
			{"SELECT format(NULL,$1)", "42P18", "could not determine data type of parameter $1", ""},
			{"SELECT format('%s',$1),$1", "42P08", "could not determine data type of parameter $1", "20"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := controlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = tc.position
				s := referencePrepared(referenceErrorDiagnostic(tc.q, d))
				s.selectRows, s.errorStage = true, "prepare"
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			})
		}
		q := "SELECT format($1,$2::text) AS rendered"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal("quiet format Prepare", err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct {
			pattern string
			value   any
			want    any
		}{
			{"%s", "x", "x"}, {"%L", nil, "NULL"}, {"%%", "unused", "%"}, {"", "x", ""},
		} {
			referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"rendered"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.pattern, tc.value))
		}
		d := controlsReferenceDiagnostic("22023", `unrecognized format() type specifier "d"`)
		d.Hint = `For a single "%" use "%%".`
		s := referenceErrorDiagnostic(q, d, "%d", "x")
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"rendered"}, []string{"TEXT"}, [][]any{{"after"}}, "%s", "after"))
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT format($1)", []string{"format"}, []string{"TEXT"}, [][]any{{"literal"}}, "literal"))
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "late-any-direct", true: "late-any-prepared"}[prepared], func(t *testing.T) {
				d := controlsReferenceDiagnostic("42P08", "could not determine data type of parameter $1")
				d.Position = "23"
				s := referenceErrorDiagnostic("SELECT $1,format('%s',$1)", d, "x")
				s.selectRows, s.errorStage = true, "execute"
				if prepared {
					s = referencePrepared(s)
					s.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			})
		}
		formatReferenceBoth(t, p, b, c, referenceSelect("SELECT format('|%*s|',$1::integer,$2::text)", []string{"format"}, []string{"TEXT"}, [][]any{{"|   x|"}}, 4, "x"))
	})
}
