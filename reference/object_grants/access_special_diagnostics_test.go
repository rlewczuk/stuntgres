package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessSpecialNullDiagnostics(t *testing.T) {
	accessSpecialScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		for _, call := range accessSpecialCalls(t, p, c) {
			held := accessExecutionHeld(t, p, c, call.query)
			for i := range call.args {
				args := append([]any(nil), call.args...)
				args[i] = nil
				run(objectGrantAccessValue(call.query, nil, args...))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(call.query, nil, args...))
				for j := range args {
					if i != j {
						switch args[j].(type) {
						case string:
							args[j] = "special_missing"
						case int64:
							args[j] = int64(4294967294)
						}
					}
				}
				if i != len(args)-1 {
					args[len(args)-1] = "bad"
				}
				run(objectGrantAccessValue(call.query, nil, args...))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(call.query, nil, args...))
			}
			bad := append([]any(nil), call.args...)
			bad[len(bad)-1] = "ALL"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, accessExecutionError(call.query, "22023", `unrecognized privilege type: "ALL"`, bad...))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(call.query, true, call.args...))
			})
		}
		for _, tc := range []struct{ q, code, message string }{
			{"SELECT has_largeobject_privilege('special_missing',4294967294::oid,'bad')", "42704", `role "special_missing" does not exist`},
			{"SELECT has_parameter_privilege('special_missing','lc_messages','bad')", "22023", `unrecognized privilege type: "bad"`},
			{"SELECT has_parameter_privilege('special_missing','lc_messages','SET')", "42704", `role "special_missing" does not exist`},
			{"SELECT has_largeobject_privilege('PUBLIC',910201::oid,'SELECT')", "42704", `role "PUBLIC" does not exist`},
			{"SELECT has_parameter_privilege('PUBLIC','lc_messages','SET')", "42704", `role "PUBLIC" does not exist`},
			{"SELECT has_largeobject_privilege(4294967294::oid,'bad')", "22023", `unrecognized privilege type: "bad"`},
			{"SELECT has_largeobject_privilege(910201::oid,'SET')", "22023", `unrecognized privilege type: "SET"`},
			{"SELECT has_parameter_privilege('lc_messages','SELECT')", "22023", `unrecognized privilege type: "SELECT"`},
		} {
			run(accessExecutionError(tc.q, tc.code, tc.message))
		}
		analysis := func(q string, d fixture.Diagnostic) {
			s := fixture.ImplicitError(q, d)
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			for _, rows := range []bool{false, true} {
				s.SelectRows = rows
				run(s)
			}
		}
		hint := "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, f := range []string{"has_largeobject_privilege", "has_parameter_privilege"} {
			for _, prefix := range []string{"SELECT ", "/* é */ SELECT "} {
				// Argument and FILTER analysis still precede formal resolution
				// and unknown-literal coercion.
				for _, suffix := range []string{
					"('x','x',special_missing_column)",
					"('x','x',true) FILTER (WHERE special_missing_column)",
				} {
					q := prefix + f + suffix
					pos := len([]rune(q[:strings.Index(q, "special_missing_column")])) + 1
					analysis(q, fixture.Diagnostic{Code: "42703", Message: `column "special_missing_column" does not exist`, Position: strconv.Itoa(pos)})
				}
				for _, tc := range []struct{ args, types string }{
					{"", ""},
					{"true", "boolean"},
					{"true,true,true,true", "boolean, boolean, boolean, boolean"},
					{"true,'x'", "boolean, unknown"},
					{"'x',true", "unknown, boolean"},
					{"true,'x','x'", "boolean, unknown, unknown"},
					{"'x',true,'x'", "unknown, boolean, unknown"},
					{"'x','x',true", "unknown, unknown, boolean"},
				} {
					pos := "8"
					if prefix != "SELECT " {
						pos = "16"
					}
					analysis(prefix+f+"("+tc.args+")", fixture.Diagnostic{Code: "42883", Message: "function " + f + "(" + tc.types + ") does not exist", Hint: hint, Position: pos})
				}
			}
			for _, tc := range []struct{ q, sig string }{{`SELECT "` + strings.ToUpper(f) + `"('x','SET')`, strings.ToUpper(f) + "(unknown, unknown)"}, {"SELECT " + f + "_missing('x','SET')", f + "_missing(unknown, unknown)"}, {"SELECT " + f + "(who => 'x', privilege => 'SET')", f + "(who => unknown, privilege => unknown)"}} {
				analysis(tc.q, fixture.Diagnostic{Code: "42883", Message: "function " + tc.sig + " does not exist", Hint: hint, Position: "8"})
			}
			analysis("SELECT special_missing."+f+"('x','SET')", fixture.Diagnostic{Code: "3F000", Message: `schema "special_missing" does not exist`, Position: "8"})
			args := "'x'::text,'SET'::text"
			if f == "has_largeobject_privilege" {
				args = "1::oid,'SELECT'::text"
			}
			for _, decoration := range []string{"DISTINCT", "ORDER BY", "FILTER"} {
				q := "SELECT " + f + "(" + args + ") FILTER (WHERE TRUE)"
				if decoration == "DISTINCT" {
					q = "SELECT " + f + "(DISTINCT " + args + ")"
				}
				if decoration == "ORDER BY" {
					q = "SELECT " + f + "(" + args + " ORDER BY 1)"
				}
				analysis(q, fixture.Diagnostic{Code: "42809", Message: decoration + " specified, but " + f + " is not an aggregate function", Position: "8"})
			}
		}
		// Typed targets must not invent overloads, even with unknown siblings.
		// Check both released actor axes independently.
		for _, tc := range []struct{ args, types string }{
			{"'og_object_reader'::name", "name"},
			{"1::oid", "oid"},
		} {
			for _, target := range []struct{ function, arg, typ string }{
				{"has_largeobject_privilege", "'x'::text", "text"},
				{"has_parameter_privilege", "1::oid", "oid"},
			} {
				sig := target.function + "(" + tc.types + ", " + target.typ + ", unknown)"
				analysis("SELECT "+target.function+"("+tc.args+","+target.arg+",'x')", fixture.Diagnostic{Code: "42883", Message: "function " + sig + " does not exist", Hint: hint, Position: "8"})
			}
		}
		// Once all formal axes are viable, the required OID input codec
		// still diagnoses the unknown target at its own source position.
		for _, q := range []string{
			"SELECT has_largeobject_privilege('x','SELECT')",
			"SELECT has_largeobject_privilege('og_object_reader','x','SELECT')",
		} {
			analysis(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "x"`, Position: strconv.Itoa(strings.Index(q, "'x'") + 1)})
		}
		for _, tc := range []struct{ q, sig string }{{"SELECT has_largeobject_privilege('x'::text,'SELECT'::text)", "has_largeobject_privilege(text, text)"}, {"SELECT has_parameter_privilege(1::oid,'SET'::text)", "has_parameter_privilege(oid, text)"}} {
			analysis(tc.q, fixture.Diagnostic{Code: "42883", Message: "function " + tc.sig + " does not exist", Hint: hint, Position: "8"})
		}
		run(objectGrantAccessValue("SELECT has_largeobject_privilege(v,'bad') AS v FROM (VALUES(NULL::oid)) AS s(v)", nil))
		run(objectGrantAccessValue("SELECT has_parameter_privilege(v,'bad') AS v FROM (VALUES(NULL::text)) AS s(v)", nil))
		run(accessExecutionError("SELECT has_largeobject_privilege(NULL::oid,(1/0)::text)", "22012", "division by zero"))
		run(accessExecutionError("SELECT has_parameter_privilege(NULL::text,(1/0)::text)", "22012", "division by zero"))
	})
}
