package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAccessNamespacesNullDiagnostics(t *testing.T) {
	objectGrantScenario(t, []string{"an_private"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer accessExecutionResetConnection(t, c)
		objects := accessNamespacesSetup(t, p, b, c, run)
		for n, o := range objects {
			if o.function == "has_type_privilege" {
				run(fixture.Exec("GRANT USAGE ON SCHEMA an_private TO og_object_reader", 0))
			}
			for _, call := range accessExecutionCalls(t, p, c, o) {
				held := accessExecutionHeld(t, p, c, call.query)
				formals := strings.Split(call.signature[strings.IndexByte(call.signature, '(')+1:len(call.signature)-1], ",")
				column, values, names := call.query, []string{}, []string{}
				for j, formal := range formals {
					name := "a" + string(rune('1'+j))
					placeholder := "$" + string(rune('1'+j)) + "::" + formal
					column = strings.ReplaceAll(column, placeholder, name)
					values = append(values, placeholder)
					names = append(names, name)
				}
				column += " FROM (VALUES(" + strings.Join(values, ",") + ")) AS s(" + strings.Join(names, ",") + ")"
				for i := range call.args {
					args := append([]any(nil), call.args...)
					args[i] = nil
					s := objectGrantAccessValue(call.query, nil, args...)
					s.CheckMetadata = accessNamespacesMetadata(b)
					run(s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					run(objectGrantAccessValue(column, nil, args...))
					for j, v := range args {
						if j == i {
							continue
						}
						switch v.(type) {
						case string:
							args[j] = "an_missing"
						case int64:
							args[j] = int64(4294967294)
						}
					}
					if i != len(args)-1 {
						args[len(args)-1] = "bad"
					}
					s = objectGrantAccessValue(call.query, nil, args...)
					run(s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					// Typed literal NULL exercises strictness independently of binding parameters.
					literal := strings.Replace(call.query, "$"+string(rune('1'+i)), "NULL", 1)
					// Keep parameter numbering contiguous for the literal route.
					for j := i + 1; j < len(call.args); j++ {
						literal = strings.ReplaceAll(literal, "$"+string(rune('1'+j)), "$"+string(rune('0'+j)))
					}
					valid := append([]any(nil), call.args[:i]...)
					valid = append(valid, call.args[i+1:]...)
					run(objectGrantAccessValue(literal, nil, valid...))
				}
				bad := append([]any(nil), call.args...)
				bad[len(bad)-1] = "ALL"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, accessExecutionError(call.query, "22023", `unrecognized privilege type: "ALL"`, bad...))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RunStep(t, p.Ctx, held, b.Recorder, objectGrantAccessValue(call.query, false, call.args...))
				})
			}
			q := "SELECT " + o.function + "($1::name,$2::text,$3::text) AS v"
			for _, tc := range []struct{ actor, object, token, code, message string }{
				{"an_missing", "an_missing", "bad", "42704", `role "an_missing" does not exist`},
				{"PUBLIC", o.name, o.privilege, "42704", `role "PUBLIC" does not exist`},
				{"og_object_reader", "an_missing", "bad", []string{"3D000", "3F000", "42704", "42704"}[n], []string{`database "an_missing" does not exist`, `schema "an_missing" does not exist`, `type "an_missing" does not exist`, `tablespace "an_missing" does not exist`}[n]},
			} {
				s := accessExecutionError(q, tc.code, tc.message, tc.actor, tc.object, tc.token)
				run(s)
				s.SelectRows = false
				run(s)
			}
			for _, oid := range []int64{0, 4294967294} {
				q := "SELECT " + o.function + "('og_object_reader',$1::oid,$2::text) AS v"
				run(objectGrantAccessValue(q, nil, oid, o.privilege))
				run(accessExecutionError(q, "22023", `unrecognized privilege type: "bad"`, oid, "bad"))
				run(objectGrantAccessValue("SELECT "+o.function+"('postgres',$1::oid,$2::text) AS v", true, oid, o.privilege))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(objectGrantAccessValue("SELECT "+o.function+"($1::oid,$2::text) AS v", nil, oid, o.privilege))
				})
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
			for _, prefix := range []string{"SELECT ", "/* é */ SELECT "} {
				position := "8"
				if prefix != "SELECT " {
					position = "16"
				}
				for _, tc := range []struct{ args, types string }{{"", ""}, {"true", "boolean"}, {"true,true,true,true", "boolean, boolean, boolean, boolean"}, {"true,'x'", "boolean, unknown"}, {"'x',true", "unknown, boolean"}, {"true,'x','x'", "boolean, unknown, unknown"}, {"'x',true,'x'", "unknown, boolean, unknown"}, {"'x','x',true", "unknown, unknown, boolean"}} {
					analysis(prefix+o.function+"("+tc.args+")", fixture.Diagnostic{Code: "42883", Message: "function " + o.function + "(" + tc.types + ") does not exist", Hint: hint, Position: position})
				}
			}
			for _, tc := range []struct{ query, signature string }{
				{`SELECT "` + strings.ToUpper(o.function) + `"('x','y')`, strings.ToUpper(o.function) + "(unknown, unknown)"},
				{"SELECT " + o.function + "_missing('x','y')", o.function + "_missing(unknown, unknown)"},
				{"SELECT " + o.function + "(who => 'x', privilege => 'y')", o.function + "(who => unknown, privilege => unknown)"},
			} {
				analysis(tc.query, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: "8"})
			}
			analysis("SELECT an_missing."+o.function+"('x','y')", fixture.Diagnostic{Code: "3F000", Message: `schema "an_missing" does not exist`, Position: "8"})
			for _, decoration := range []string{"DISTINCT", "ORDER BY", "FILTER"} {
				q := "SELECT " + o.function + "('x','y') FILTER (WHERE TRUE)"
				switch decoration {
				case "DISTINCT":
					q = "SELECT " + o.function + "(DISTINCT 'x','y')"
				case "ORDER BY":
					q = "SELECT " + o.function + "('x','y' ORDER BY 1)"
				}
				analysis(q, fixture.Diagnostic{Code: "42809", Message: decoration + " specified, but " + o.function + " is not an aggregate function", Position: "8"})
			}
			run(objectGrantAccessValue("SELECT "+o.function+"(v,'bad') AS v FROM (VALUES(NULL::text)) AS s(v)", nil))
			run(objectGrantAccessValue("SELECT CASE WHEN FALSE THEN "+o.function+"('an_missing','bad') ELSE FALSE END AS v", false))
			run(objectGrantAccessValue("SELECT pg_catalog."+o.function+"('og_object_reader',$1::oid,$2::text) AS v", false, o.oid, o.privilege))
			// Strictness cannot suppress demanded child evaluation.
			run(accessExecutionError("SELECT "+o.function+"(NULL::name,(1/$1::integer)::oid,'bad') AS v", "22012", "division by zero", 0))
			input := fixture.ImplicitError("SELECT "+o.function+"(NULL::name,$1::oid,'bad') AS v", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
			input.SelectRows = true
			input.ErrorStage = "execute"
			run(input)
		}
	})
}
