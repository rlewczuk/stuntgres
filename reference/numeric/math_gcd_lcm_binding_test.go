package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathGcdLcmWidthsAndResolution(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathGcdLcmBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, w := range []struct{ cast, typ, min, max, half, message string }{
			{"integer", "INT4", "-2147483648", "2147483647", "1073741824", "integer out of range"},
			{"bigint", "INT8", "-9223372036854775808", "9223372036854775807", "4611686018427387904", "bigint out of range"},
		} {
			call := func(name, a, c string) string {
				return "SELECT " + name + "('" + a + "'::" + w.cast + ",'" + c + "'::" + w.cast + ") AS result"
			}
			for _, pair := range [][2]string{{w.min, "0"}, {"0", w.min}, {w.min, w.min}} {
				referenceMathGcdLcmStep(t, p, b, referenceMathGcdLcmFailure(call("gcd", pair[0], pair[1]), "22003", w.message))
			}
			for _, divisor := range []string{"1", "-1", w.half, "-" + w.half, w.max} {
				want := int64(1)
				if divisor == w.half || divisor == "-"+w.half {
					want, _ = strconv.ParseInt(w.half, 10, 64)
				}
				for _, pair := range [][2]string{{w.min, divisor}, {divisor, w.min}} {
					referenceMathGcdLcmStep(t, p, b, fixture.Select(call("gcd", pair[0], pair[1]), []string{"result"}, []string{w.typ}, [][]any{{want}}))
				}
			}
			for _, pair := range [][2]string{{w.min, "0"}, {"0", w.min}, {"0", "0"}} {
				referenceMathGcdLcmStep(t, p, b, fixture.Select(call("lcm", pair[0], pair[1]), []string{"result"}, []string{w.typ}, [][]any{{int64(0)}}))
			}
			for _, pair := range [][2]string{{w.min, "1"}, {"1", w.min}, {w.min, "-1"}, {"-1", w.min}, {w.min, w.min}, {w.max, "2"}, {"2", w.max}} {
				referenceMathGcdLcmStep(t, p, b, referenceMathGcdLcmFailure(call("lcm", pair[0], pair[1]), "22003", w.message))
			}
			max, _ := strconv.ParseInt(w.max, 10, 64)
			referenceMathGcdLcmStep(t, p, b, fixture.Select(call("lcm", w.max, w.max), []string{"result"}, []string{w.typ}, [][]any{{max}}))
		}
		for _, tc := range []struct {
			args, typ string
			g, l      any
		}{
			{"288484263558,29893644334", "INT8", int64(6835958), int64(1261541684539134)},
			{"330,462", "INT4", int64(66), int64(2310)},
			{"12::integer,18::bigint", "INT8", int64(6), int64(36)},
			{"12::bigint,18::integer", "INT8", int64(6), int64(36)},
			{"12::smallint,18::integer", "INT4", int64(6), int64(36)},
			{"12::smallint,18::bigint", "INT8", int64(6), int64(36)},
			{"12::smallint,18::numeric", "NUMERIC", "6", "36"},
			{"12::integer,18.00", "NUMERIC", "6.00", "36.00"},
			{"12.00,18::bigint", "NUMERIC", "6.00", "36.00"},
			{"9223372036854775808,2", "NUMERIC", "2", "9223372036854775808"},
		} {
			referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd("+tc.args+") AS g,lcm("+tc.args+") AS l", []string{"g", "l"}, []string{tc.typ, tc.typ}, [][]any{{tc.g, tc.l}}))
		}
		referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT gcd(61866666,6410818) AS g", []string{"g"}, []string{"INT4"}, [][]any{{int64(1466)}}))
		for _, name := range []string{"gcd", "lcm"} {
			want := int64(6)
			if name == "lcm" {
				want = 36
			}
			for _, spelling := range []string{name, strings.ToUpper(name), `"` + name + `"`, "pg_catalog." + name, `pg_catalog."` + name + `"`} {
				referenceMathGcdLcmStep(t, p, b, fixture.Select("SELECT "+spelling+"(12,18) AS result", []string{"result"}, []string{"INT4"}, [][]any{{want}}))
			}
		}
	})
}

func TestReferenceMathGcdLcmDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathGcdLcmBackends(t, p, func(t *testing.T, b fixture.Backend) {
		for _, name := range []string{"gcd", "lcm"} {
			for _, tc := range []struct{ args, signature string }{{"NULL,NULL", "unknown, unknown"}, {"'12','18'", "unknown, unknown"}, {"$1,$2", "unknown, unknown"}, {"1::smallint,2::smallint", "smallint, smallint"}, {"1::smallint,NULL", "smallint, unknown"}} {
				q := "SELECT " + name + "(" + tc.args + ")"
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42725", Message: "function " + name + "(" + tc.signature + ") is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "8"})
				s.SelectRows = true
				if !strings.Contains(tc.args, "$") {
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
			}
			for _, tc := range []struct{ spelling, args, signature string }{
				{name, "", ""}, {name, "1", "integer"}, {name, "1,2,3", "integer, integer, integer"},
				{name, "1.0::float8,2.0::float8", "double precision, double precision"},
				{name, "1::real,2::real", "real, real"}, {name, "'1'::text,'2'::text", "text, text"},
				{name, "true,false", "boolean, boolean"}, {name, "x => 1,y => 2", "x => integer, y => integer"},
				{name, "VARIADIC ARRAY[1,2]", "integer[]"}, {"public." + name, "1,2", "integer, integer"},
				{`"` + strings.ToUpper(name) + `"`, "1,2", "integer, integer"},
			} {
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + "SELECT " + tc.spelling + "(" + tc.args + ")"
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "function " + strings.ReplaceAll(tc.spelling, `"`, "") + "(" + tc.signature + ") does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: strconv.Itoa(8 + len([]rune(prefix)))})
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					s.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
				}
			}
			for _, peer := range []struct{ sql, word string }{{"1", "integer"}, {"1::bigint", "bigint"}, {"1.0", "numeric"}, {"NULL::integer", "integer"}} {
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + "SELECT " + name + "('oops'," + peer.sql + ")"
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("invalid input syntax for type %s: %q", peer.word, "oops"), Position: strconv.Itoa(12 + len([]rune(prefix)))})
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					s.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
				}
			}
			unresolved := fixture.ImplicitError("SELECT "+name+"(NULL::integer,gcd(1::text,2::text))", fixture.Diagnostic{Code: "42883", Message: "function gcd(text, text) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "26"})
			unresolved.SelectRows, unresolved.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, unresolved)
			unresolved.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(unresolved))
			for _, q := range []string{"SELECT " + name + "(NULL::integer,1/0)", "SELECT " + name + "(NULL::integer,lcm('-2147483648'::integer,1))"} {
				code, message := "22012", "division by zero"
				if strings.Contains(q, "lcm(") && !strings.Contains(q, "1/0") {
					code, message = "22003", "integer out of range"
				}
				referenceMathGcdLcmStep(t, p, b, referenceMathGcdLcmFailure(q, code, message))
			}
		}
	})
}
