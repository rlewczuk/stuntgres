package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceMACOperationsResolution(t *testing.T) {
	macOperationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		check := func(q, code, message, hint, marker string, args ...any) {
			t.Helper()
			d := fixture.ControlsReferenceDiagnostic(code, message)
			d.Hint = hint
			d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, marker)]) + 1)
			t.Run(q, func(t *testing.T) {
				s := fixture.ErrorDiagnostic(q, d, args...)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			})
		}
		ambiguousOp := "Could not choose a best candidate operator. You might need to add explicit type casts."
		missingOp := "No operator matches the given name and argument types. You might need to add explicit type casts."
		missingFunc := "No function matches the given name and argument types. You might need to add explicit type casts."
		ambiguousFunc := "Could not choose a best candidate function. You might need to add explicit type casts."
		for _, op := range []string{"&", "|"} {
			for _, widths := range [][2]string{{"macaddr", "macaddr8"}, {"macaddr8", "macaddr"}} {
				sig := widths[0] + " " + op + " " + widths[1]
				check("SELECT NULL::"+widths[0]+" "+op+" NULL::"+widths[1], "42725", "operator is not unique: "+sig, ambiguousOp, op)
			}
		}
		check("/*é*/ SELECT NULL::macaddr & NULL::macaddr8", "42725", "operator is not unique: macaddr & macaddr8", ambiguousOp, "&")
		for _, tc := range []struct{ expression, signature, marker string }{
			{"NULL::macaddr # NULL::macaddr", "macaddr # macaddr", "#"},
			{"NULL::macaddr8 # NULL::macaddr8", "macaddr8 # macaddr8", "#"},
			{"NULL::macaddr << 1", "macaddr << integer", "<<"},
			{"NULL::macaddr8 >> 1", "macaddr8 >> integer", ">>"},
			{"NULL::macaddr & NULL::text", "macaddr & text", "&"},
			{"1 | NULL::macaddr8", "integer | macaddr8", "|"},
		} {
			check("SELECT "+tc.expression, "42883", "operator does not exist: "+tc.signature, missingOp, tc.marker)
		}
		check("SELECT ~ NULL::text", "42883", "operator does not exist: ~ text", "No operator matches the given name and argument type. You might need to add an explicit type cast.", "~")
		for _, tc := range []struct{ expression, signature string }{
			{"trunc(NULL::macaddr, 1)", "trunc(macaddr, integer)"},
			{"trunc(NULL::macaddr8, 1)", "trunc(macaddr8, integer)"},
			{"macaddr8_set7bit(NULL::text)", "macaddr8_set7bit(text)"},
			{"macaddr8_set7bit(1)", "macaddr8_set7bit(integer)"},
			{"macaddr8_set7bit()", "macaddr8_set7bit()"},
			{"macaddr8_set7bit(NULL::macaddr8,NULL::macaddr8)", "macaddr8_set7bit(macaddr8, macaddr8)"},
		} {
			check("/*é*/ SELECT "+tc.expression, "42883", "function "+tc.signature+" does not exist", missingFunc, strings.Split(tc.expression, "(")[0])
		}
		for _, expression := range []string{"trunc(NULL)", "trunc('12:34:56:78:90:ab')", "trunc($1)"} {
			var args []any
			if strings.Contains(expression, "$1") {
				args = []any{nil}
			}
			check("SELECT "+expression, "42725", "function trunc(unknown) is not unique", ambiguousFunc, "trunc", args...)
		}
		for _, call := range []struct{ name, arg string }{
			{"trunc", "NULL::macaddr"}, {"trunc", "NULL::macaddr8"}, {"macaddr8_set7bit", "NULL::macaddr8"},
			{"trunc", "4.9"},
		} {
			for _, decoration := range []struct{ args, suffix, message string }{
				{"DISTINCT " + call.arg, "", "DISTINCT specified, but " + call.name + " is not an aggregate function"},
				{call.arg + " ORDER BY 1", "", "ORDER BY specified, but " + call.name + " is not an aggregate function"},
				{call.arg, " FILTER (WHERE true)", "FILTER specified, but " + call.name + " is not an aggregate function"},
				{call.arg, " OVER ()", "OVER specified, but " + call.name + " is not a window function nor an aggregate function"},
			} {
				check("SELECT "+call.name+"("+decoration.args+")"+decoration.suffix, "42809", decoration.message, "", call.name)
			}
		}
		// Input errors retain the literal's position rather than the call's position.
		for _, tc := range []struct{ q, typ string }{
			{"SELECT trunc('bad'::macaddr)", "macaddr"},
			{"SELECT macaddr8_set7bit('bad'::macaddr8)", "macaddr8"},
		} {
			check(tc.q, "22P02", "invalid input syntax for type "+tc.typ+": \"bad\"", "", "'bad'")
		}
		// Known-width operands supply the type of unknown parameter occurrences.
		macOperationsValue(t, p, b, "macaddr '12:34:56:78:90:ab' & $1", "MACADDR", "02:30:02:50:90:00", "0f:f0:aa:55:ff:00")
		macOperationsValue(t, p, b, "$1 | macaddr8 '80:00:00:00:00:00:00:01'", "MACADDR8", "80:00:00:ff:fe:00:00:01", "00:00:00:00:00:00")
		// Unrelated same-spelling candidates force ordinary catalog ranking.
		// They must not erase the two MAC candidates' mixed-width ambiguity.
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE FUNCTION public.mac_ops_text(text,text) RETURNS integer LANGUAGE SQL AS 'SELECT 42'", 0))
		for _, op := range []string{"&", "|"} {
			run(fixture.Exec("CREATE OPERATOR public."+op+" (LEFTARG=text,RIGHTARG=text,FUNCTION=public.mac_ops_text)", 0))
			for _, widths := range [][2]string{{"macaddr", "macaddr8"}, {"macaddr8", "macaddr"}} {
				check("SELECT NULL::"+widths[0]+" "+op+" NULL::"+widths[1], "42725", "operator is not unique: "+widths[0]+" "+op+" "+widths[1], ambiguousOp, op)
			}
		}
		macOperationsValue(t, p, b, "macaddr '12:34:56:78:90:ab' & macaddr '0f:f0:aa:55:ff:00'", "MACADDR", "02:30:02:50:90:00")
		macOperationsValue(t, p, b, "macaddr8 '12:34:56:78:90:ab:cd:ef' | macaddr8 '0f:f0:aa:55:ff:00:33:cc'", "MACADDR8", "1f:f4:fe:7d:ff:ab:ff:ef")
		// An exact custom mixed signature wins before implicit viability ranking.
		for _, widths := range [][2]string{{"macaddr", "macaddr8"}, {"macaddr8", "macaddr"}} {
			name := "public.mac_ops_" + widths[0] + "_" + widths[1]
			run(fixture.Exec("CREATE FUNCTION "+name+"("+widths[0]+","+widths[1]+") RETURNS integer LANGUAGE SQL AS 'SELECT 42'", 0))
			for _, op := range []string{"&", "|"} {
				run(fixture.Exec("CREATE OPERATOR public."+op+" (LEFTARG="+widths[0]+",RIGHTARG="+widths[1]+",FUNCTION="+name+")", 0))
				macOperationsValue(t, p, b, "NULL::"+widths[0]+" "+op+" NULL::"+widths[1], "INT4", int64(42))
			}
		}
	})
}
