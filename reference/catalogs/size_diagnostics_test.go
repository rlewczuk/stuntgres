package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSizeDiagnostics(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, input := range []string{"", " \t\n", "+", "-", ".", "+.", "NaN", "Infinity", "-Infinity", "abc"} {
			t.Run(input, func(t *testing.T) {
				sizeBoth(t, p, b, c, fixture.ErrorDiagnostic("SELECT pg_size_bytes($1)", sizeInvalid(input, ""), input), false)
			})
		}
		for _, v := range []struct{ input, unit string }{
			{"1 byte", "byte"}, {"1 KiB", "KiB"}, {"1 EB", "EB"}, {"1MB junk", "MB junk"},
			{"1 k B", "k B"}, {" 1 Bad \t\n", "Bad"}, {"1e", "e"}, {"1e+", "e+"},
			{"1EB", "EB"}, {"1_000kB", "_000kB"}, {"0x10", "x10"}, {"1e2_0MB", "_0MB"}, {"1e100 bad", "bad"},
		} {
			t.Run(v.input, func(t *testing.T) {
				sizeBoth(t, p, b, c, fixture.ErrorDiagnostic("SELECT pg_size_bytes($1)", sizeInvalid(v.input, v.unit), v.input), false)
			})
		}
		for _, input := range []string{"9223372036854775807.5", "-9223372036854775808.5", "8192PB", "-8192.000000000001PB", "1e100"} {
			t.Run(input, func(t *testing.T) {
				sizeBoth(t, p, b, c, fixture.ErrorDiagnostic("SELECT pg_size_bytes($1)", sizeDiagnostic("22003", "bigint out of range", "", ""), input), false)
			})
		}
		for _, input := range []string{"1e1000000000000000000", "1e1000000000000000000 bad"} {
			sizeBoth(t, p, b, c, fixture.ErrorDiagnostic("SELECT pg_size_bytes($1)", sizeDiagnostic("22003", "value overflows numeric format", "", ""), input), false)
		}
		sizeBoth(t, p, b, c, fixture.ErrorDiagnostic("SELECT pg_size_bytes($1)", sizeDiagnostic("22P02", `invalid input syntax for type numeric: "1e 2"`, "", ""), "1e 2 MB"), false)
		// A literal provider failure occurs at Execute, not parameter decoding.
		sizeBoth(t, p, b, c, fixture.ErrorDiagnostic("SELECT pg_size_bytes('1 KiB')", sizeInvalid("1 KiB", "KiB")), false)
	})
}

func TestReferenceSizeSignatures(t *testing.T) {
	sizeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, v := range []struct{ expr, typ string }{
			{"1", "integer"}, {"1::smallint", "smallint"}, {"'1'", "unknown"}, {"NULL", "unknown"}, {"$1", "unknown"},
			{"NULL::integer", "integer"}, {"NULL::smallint", "smallint"},
		} {
			for _, prefix := range []struct{ sql, pos string }{{"", "8"}, {"/*é🙂*/ ", "15"}} {
				q := prefix.sql + "SELECT pg_size_pretty(" + v.expr + ")"
				var args []any
				if v.expr == "$1" {
					args = []any{int64(1024)}
				}
				t.Run(q, func(t *testing.T) {
					sizeBoth(t, p, b, c, fixture.ErrorDiagnostic(q, sizeDiagnostic("42725", "function pg_size_pretty("+v.typ+") is not unique", sizeAmbiguousHint, prefix.pos), args...), true)
				})
			}
		}
		for _, fn := range []string{"pg_size_bytes", "pg_size_pretty"} {
			bad := []struct{ expr, typ string }{{"", ""}, {"1,2", "integer, integer"}, {"true", "boolean"}, {"NULL::boolean", "boolean"}}
			if fn == "pg_size_bytes" {
				bad = append(bad, struct{ expr, typ string }{"1", "integer"}, struct{ expr, typ string }{"1::bigint", "bigint"}, struct{ expr, typ string }{"1.0", "numeric"}, struct{ expr, typ string }{"'1'::bytea", "bytea"})
			} else {
				bad = append(bad, struct{ expr, typ string }{"'1'::text", "text"}, struct{ expr, typ string }{"1::real", "real"}, struct{ expr, typ string }{"1::double precision", "double precision"})
			}
			for _, v := range bad {
				q := "SELECT " + fn + "(" + v.expr + ")"
				t.Run(q, func(t *testing.T) {
					sizeBoth(t, p, b, c, fixture.ErrorDiagnostic(q, sizeDiagnostic("42883", "function "+fn+"("+v.typ+") does not exist", sizeMissingHint, "8")), true)
				})
				if v.typ != "" && !strings.Contains(v.typ, ",") {
					q = "SELECT " + fn + "(NULL::" + v.typ + ")"
					sizeBoth(t, p, b, c, fixture.ErrorDiagnostic(q, sizeDiagnostic("42883", "function "+fn+"("+v.typ+") does not exist", sizeMissingHint, "8")), true)
				}
			}
			arg, typ := "'1 MB'", "unknown"
			if fn == "pg_size_pretty" {
				arg, typ = "1024::bigint", "bigint"
			}
			for _, spelling := range []string{"public." + fn, strings.ToUpper(fn), strings.Replace(fn, "pg", "Pg", 1)} {
				name := spelling
				if !strings.Contains(spelling, ".") {
					name = `"` + spelling + `"`
				}
				q := "SELECT " + name + "(" + arg + ")"
				sizeBoth(t, p, b, c, fixture.ErrorDiagnostic(q, sizeDiagnostic("42883", "function "+spelling+"("+typ+") does not exist", sizeMissingHint, "8")), true)
			}
			sizeBoth(t, p, b, c, fixture.ErrorDiagnostic("SELECT size_absent."+fn+"("+arg+")", sizeDiagnostic("3F000", `schema "size_absent" does not exist`, "", "8")), true)
			for _, operator := range []string{"=>", ":="} {
				q := "SELECT " + fn + "(value " + operator + " " + arg + ")"
				sizeBoth(t, p, b, c, fixture.ErrorDiagnostic(q, sizeDiagnostic("42883", "function "+fn+"(value => "+typ+") does not exist", sizeMissingHint, "8")), true)
			}
			for _, name := range []string{fn, `"` + strings.ToUpper(fn) + `"`} {
				q := "SELECT " + name + "(" + strings.TrimSuffix(strings.Repeat("1,", 101), ",") + ")"
				sizeBoth(t, p, b, c, fixture.ErrorDiagnostic(q, sizeDiagnostic("54023", "cannot pass more than 100 arguments to a function", "", "8")), true)
				q = "SELECT " + name + "(missing," + strings.TrimSuffix(strings.Repeat("1,", 100), ",") + ")"
				pos := fixture.Position(9 + len(name))
				sizeBoth(t, p, b, c, fixture.ErrorDiagnostic(q, sizeDiagnostic("42703", `column "missing" does not exist`, "", pos)), true)
				q = "SELECT " + name + "('bad'::bigint," + strings.TrimSuffix(strings.Repeat("1,", 100), ",") + ")"
				sizeBoth(t, p, b, c, fixture.ErrorDiagnostic(q, sizeDiagnostic("22P02", `invalid input syntax for type bigint: "bad"`, "", pos)), true)
			}
		}
		for _, q := range []string{"SELECT pg_size_bytes(VARIADIC '1 MB')", `SELECT "pg_size_bytes"('1 MB')`, "SELECT pg_catalog.pg_size_bytes('1 MB')"} {
			sizeBoth(t, p, b, c, fixture.Select(q, []string{"pg_size_bytes"}, []string{"INT8"}, [][]any{{int64(1048576)}}), false)
		}
		for _, q := range []string{"SELECT pg_size_pretty(VARIADIC 1024::bigint)", "SELECT pg_size_pretty(VARIADIC 1024::numeric)", `SELECT "pg_size_pretty"(1024::bigint)`, "SELECT pg_catalog.pg_size_pretty(1024::bigint)"} {
			sizeBoth(t, p, b, c, fixture.Select(q, []string{"pg_size_pretty"}, []string{"TEXT"}, [][]any{{"1024 bytes"}}), false)
		}
		for _, expr := range []string{"'1 MB'::text", "'1 MB'::varchar(8)", "'1 MB'::character(8)", "'1 MB'::name", "'1'::\"char\""} {
			want := int64(1048576)
			if expr == "'1'::\"char\"" {
				want = 1
			}
			s := fixture.Select("SELECT pg_size_bytes("+expr+")", []string{"pg_size_bytes"}, []string{"INT8"}, [][]any{{want}})
			// Internal CHAR input already exists, but its implicit TEXT cast
			// is not a native coercion edge. Do not add an adjacent cast.
			if expr == "'1'::\"char\"" && !b.PG {
				s = fixture.ErrorDiagnostic(s.Query, sizeDiagnostic("42883", `function pg_size_bytes("char") does not exist`, sizeMissingHint, "8"))
			}
			sizeBoth(t, p, b, c, s, expr == "'1'::\"char\"" && !b.PG)
		}
		for _, v := range []struct{ q, message, pos string }{
			{"SELECT pg_size_bytes($1),pg_size_pretty($1)", "function pg_size_pretty(text) does not exist", "26"},
			{"SELECT pg_size_pretty($1::bigint),pg_size_bytes($1)", "function pg_size_bytes(bigint) does not exist", "35"},
			{"SELECT pg_size_pretty('bad'::bigint)", `invalid input syntax for type bigint: "bad"`, "23"},
		} {
			code, hint := "42883", sizeMissingHint
			if strings.Contains(v.q, "'bad'") {
				code, hint = "22P02", ""
			}
			var args []any
			if strings.Contains(v.q, "$1") {
				args = []any{"1 MB"}
			}
			sizeBoth(t, p, b, c, fixture.ErrorDiagnostic(v.q, sizeDiagnostic(code, v.message, hint, v.pos), args...), true)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN size_i8 AS bigint", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN size_num AS numeric", 0))
		sizeBoth(t, p, b, c, fixture.Select("SELECT pg_size_pretty(1024::size_i8),pg_size_pretty(10.5000::size_num)", []string{"pg_size_pretty", "pg_size_pretty"}, []string{"TEXT", "TEXT"}, [][]any{{"1024 bytes", "10.5000 bytes"}}), false)
	})
}
