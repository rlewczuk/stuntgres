package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The pinned PostgreSQL 18.0 declarations have a legacy defaulted BOOLEAN
// on populate's full identity. Calls below deliberately use only the existing
// two-argument native capability; the physical row type is not that identity.
func TestReferenceObjectGrantJSONRecordAuthority(t *testing.T) {
	families := []struct {
		name, target, oid, formals, result string
		populate, set, strict              bool
	}{
		{"json_populate_record", "pg_catalog.json_populate_record(anyelement,json,boolean)", "3960", "2283 114 16", "2283", true, false, false},
		{"json_populate_recordset", "pg_catalog.json_populate_recordset(anyelement,json,boolean)", "3961", "2283 114 16", "2283", true, true, false},
		{"json_to_record", "pg_catalog.json_to_record(json)", "3204", "114", "2249", false, false, true},
		{"json_to_recordset", "pg_catalog.json_to_recordset(json)", "3205", "114", "2249", false, true, false},
	}
	for _, family := range families {
		t.Run(family.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TYPE og_jr_pair AS (n INT,s TEXT)", 0))
				run(fixture.Exec("CREATE TABLE og_jr_physical(n INT,s TEXT)", 0))
				run(fixture.Exec("CREATE TABLE og_jr_input(id INT,j JSON,js JSON)", 0))
				run(fixture.Exec(`INSERT INTO og_jr_input VALUES(1,'{"n":7,"s":"ok"}','[{"n":7,"s":"ok"},{"n":8}]'),(2,NULL,NULL)`, 2))
				run(fixture.Exec("CREATE TABLE og_jr_sink(n INT,s TEXT)", 0))
				run(fixture.Exec("INSERT INTO og_jr_sink VALUES(99,'sentinel')", 1))
				run(fixture.Exec("GRANT SELECT ON og_jr_input,og_jr_sink TO og_object_reader", 0))
				run(fixture.Exec("GRANT INSERT ON og_jr_sink TO og_object_reader", 0))

				// Independently released formal identity, never the generated composite
				// OID or an invented OUT-expanded signature.
				metadata := "SELECT p.oid::text AS oid,p.proargtypes::text AS args,p.prorettype::text AS result FROM pg_proc p WHERE p.oid=" + family.oid
				identity := fixture.Select(metadata, []string{"oid", "args", "result"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{family.oid, family.formals, family.result}})
				if b.PG {
					run(identity)
				}
				run(fixture.Select("SELECT oid::regprocedure::text AS identity,pronargs,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid="+family.oid,
					[]string{"identity", "pronargs", "proisstrict", "provolatile", "proparallel"}, []string{"TEXT", "INT2", "BOOL", "CHAR", "CHAR"},
					[][]any{{strings.TrimPrefix(family.target, "pg_catalog."), map[bool]int64{true: 3, false: 1}[family.populate], family.strict, "s", "s"}}))
				if b.PG {
					defaults := int64(0)
					var names any
					var expression any
					if family.populate {
						defaults, names, expression = 1, "{base,from_json,use_json_as_text}", "false"
					}
					q := "SELECT prokind::text AS kind,prolang::text AS lang,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,proretset,provariadic::text AS variadic,pronargdefaults,proargnames::text AS names,pg_get_expr(proargdefaults,0) AS defaults,prosrc FROM pg_proc WHERE oid=" + family.oid
					run(fixture.Select(q, []string{"kind", "lang", "proisstrict", "volatility", "parallel", "proretset", "variadic", "pronargdefaults", "names", "defaults", "prosrc"}, []string{"TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "INT2", "TEXT", "TEXT", "TEXT"}, [][]any{{"f", "12", family.strict, "s", "s", family.set, "0", defaults, names, expression, family.name}}))
				}
				pair := func(q string, rows [][]any, args ...any) fixture.Step {
					s := fixture.Select(q, []string{"n", "s"}, []string{"INT4", "TEXT"}, rows, args...)
					s.CheckMetadata = fixture.StringEncodingMetadata(b.PG, s.Columns, s.Types)
					return s
				}
				errorStep := func(q, code, message string, args ...any) fixture.Step {
					s := fixture.ImplicitError(q, fixture.ControlsReferenceDiagnostic(code, message), args...)
					s.SelectRows, s.ErrorStage = true, "execute"
					return s
				}
				input := `{"n":7,"s":"ok"}`
				rows := [][]any{{int64(7), "ok"}}
				column := "i.j"
				if family.set {
					input = `[{"n":7,"s":"ok"},{"n":8}]`
					rows = append(rows, []any{int64(8), nil})
					column = "i.js"
				}
				source := func(argument, base string) string {
					if family.populate {
						return "pg_catalog." + family.name + "(" + base + "," + argument + ")"
					}
					return "pg_catalog." + family.name + "(" + argument + ") AS r(n INT,s TEXT)"
				}
				query := func(argument, base string) string {
					return "SELECT n,s FROM " + source(argument, base) + " ORDER BY n NULLS LAST"
				}
				base := "NULL::og_jr_pair"
				q := query("$1::json", base)
				literal := query("'"+input+"'::json", base)
				rowQuery := "SELECT r.n,r.s FROM og_jr_input i," + source(column, base)
				if family.populate {
					rowQuery += " AS r"
				}
				rowQuery += " WHERE i.id=$1 ORDER BY r.n NULLS LAST"
				view := "og_jr_" + family.name
				run(fixture.Exec("CREATE VIEW "+view+" AS "+literal, 0))
				run(fixture.Exec("GRANT SELECT ON "+view+" TO og_object_reader", 0))
				viewStep := pair("SELECT n,s FROM "+view+" ORDER BY n NULLS LAST", rows)
				viewStep.CheckMetadata = fixture.StringEncodingViewMetadata(b.PG, viewStep.Columns, viewStep.Types)
				cases := []objectGrantProviderCase{
					{pair(literal, rows), true, true},
					{pair(q, rows, input), true, true},
					{pair(rowQuery, rows, int64(1)), true, true},
					{viewStep, true, true},
					// Empty results alone do not establish that initialization vanished.
					{pair(literal+" LIMIT 0", nil), true, true},
				}
				nullRows := [][]any{{nil, nil}}
				if family.set {
					nullRows = nil
				}
				cases = append(cases,
					objectGrantProviderCase{pair(query("NULL::json", base), nullRows), true, false},
					objectGrantProviderCase{pair(q, nullRows, nil), true, false},
					objectGrantProviderCase{pair(rowQuery, nullRows, int64(2)), true, true},
					objectGrantProviderCase{pair("SELECT n,s FROM "+source("'"+input+"'::json", base)+" WHERE FALSE", nil), false, false},
				)
				badShape, message := "[]", "cannot call populate_composite on an array"
				if family.set {
					badShape, message = "{}", "cannot call "+family.name+" on an object"
				}
				cases = append(cases, objectGrantProviderCase{errorStep(q, "22023", message, badShape), true, true})
				malformed := errorStep(q, "22P02", "invalid input syntax for type json", `{"n":`)
				malformed.Diagnostic.Detail = "The input string ended unexpectedly."
				malformed.Diagnostic.Context = "JSON data, line 1: {\"n\":\nunnamed portal parameter $1 = '...'"
				cases = append(cases, objectGrantProviderCase{malformed, false, true})
				if family.populate {
					baseQuery := query("$2::json", "$1::og_jr_pair")
					cases = append(cases, objectGrantProviderCase{pair(baseQuery, rows, nil, input), true, true})
					defaultInput := `{"n":7}`
					defaultRows := [][]any{{int64(7), "keep"}}
					if family.set {
						defaultInput = `[{"n":7},{"n":8}]`
						defaultRows = append(defaultRows, []any{int64(8), "keep"})
					}
					cases = append(cases, objectGrantProviderCase{pair(baseQuery, defaultRows, "(9,keep)", defaultInput), true, true})
					cases = append(cases,
						objectGrantProviderCase{pair(query("'"+input+"'::json", "NULL::og_jr_physical"), rows), true, true},
						objectGrantProviderCase{pair("SELECT n,s FROM pg_catalog."+family.name+"(from_json=>$1::json,base=>NULL::og_jr_pair) ORDER BY n NULLS LAST", rows, input), true, true},
					)
					if !family.set {
						cases = append(cases,
							objectGrantProviderCase{pair(query("NULL::json", "'(9,keep)'::og_jr_pair"), [][]any{{int64(9), "keep"}}), true, true},
							objectGrantProviderCase{pair("SELECT (pg_catalog."+family.name+"(NULL::og_jr_pair,$1::json)).n AS n,(pg_catalog."+family.name+"(NULL::og_jr_pair,$1::json)).s AS s", rows, input), true, true},
						)
					}
					// Preserve the actual composite result codec instead of hiding the
					// polymorphic result under a TEXT cast.
					target := "SELECT pg_catalog." + family.name + "(NULL::og_jr_pair,$1::json) AS r"
					composites := [][]any{{"(7,ok)"}}
					if family.set {
						composites = append(composites, []any{"(8,)"})
					}
					s := fixture.Select(target, []string{"r"}, []string{""}, composites, input)
					s.RawTypes, s.Bag = []string{"RECORD"}, family.set
					cases = append(cases, objectGrantProviderCase{s, true, true})
					empty := fixture.Select(target+" LIMIT 0", []string{"r"}, []string{""}, nil, input)
					empty.RawTypes = []string{"RECORD"}
					cases = append(cases, objectGrantProviderCase{empty, true, true})
					targetFalse := fixture.Select(target+" WHERE FALSE", []string{"r"}, []string{""}, nil, input)
					targetFalse.RawTypes = []string{"RECORD"}
					cases = append(cases, objectGrantProviderCase{targetFalse, true, true})
					nullTargetRows := [][]any{{nil}}
					if family.set {
						nullTargetRows = nil
					}
					nullTarget := fixture.Select(target, []string{"r"}, []string{""}, nullTargetRows, nil)
					nullTarget.RawTypes = []string{"RECORD"}
					cases = append(cases, objectGrantProviderCase{nullTarget, true, false})
				} else if !family.set {
					s := fixture.Select("SELECT pg_catalog.json_to_record(NULL::json) AS r", []string{"r"}, []string{"RECORD"}, [][]any{{nil}})
					cases = append(cases, objectGrantProviderCase{s, true, false})
				}
				controls := []fixture.Step{fixture.Select(`SELECT json_array_length('[1,2]'::json) AS n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}})}
				for _, sibling := range families {
					if sibling.name == family.name {
						continue
					}
					argument := `'{"n":7,"s":"ok"}'::json`
					want := [][]any{{int64(7), "ok"}}
					if sibling.set {
						argument = `'[{"n":7,"s":"ok"},{"n":8}]'::json`
						want = append(want, []any{int64(8), nil})
					}
					call := "pg_catalog." + sibling.name + "(" + argument + ") AS r(n INT,s TEXT)"
					if sibling.populate {
						call = "pg_catalog." + sibling.name + "(NULL::og_jr_pair," + argument + ")"
					}
					controls = append(controls, pair("SELECT n,s FROM "+call+" ORDER BY n NULLS LAST", want))
				}
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, family.target, "function", family.name, cases, controls)

				// The helper's deferred cleanup restores PUBLIC before this separate
				// write witness. Check denied publication, then exact granted recovery.
				defer objectGrantRoutineCleanup(t, p, c, []string{family.target})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+family.target+" FROM PUBLIC", 0))
				insert := "INSERT INTO og_jr_sink(n,s) " + literal
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					s := fixture.ImplicitError(insert, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+family.name))
					s.ErrorStage = "execute"
					run(s)
					check := pair("SELECT n,s FROM og_jr_sink", [][]any{{int64(99), "sentinel"}})
					check.CheckMetadata = fixture.StringEncodingViewMetadata(b.PG, check.Columns, check.Types)
					run(check)
				})
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+family.target+" TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(fixture.Exec(insert, int64(len(rows))))
					want := append(append([][]any(nil), rows...), []any{int64(99), "sentinel"})
					check := pair("SELECT n,s FROM og_jr_sink ORDER BY n", want)
					check.CheckMetadata = fixture.StringEncodingViewMetadata(b.PG, check.Columns, check.Types)
					run(check)
				})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+family.target+" FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					s := fixture.ImplicitError(insert, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+family.name))
					s.ErrorStage = "execute"
					run(s)
					want := append(append([][]any(nil), rows...), []any{int64(99), "sentinel"})
					check := pair("SELECT n,s FROM og_jr_sink ORDER BY n", want)
					check.CheckMetadata = fixture.StringEncodingViewMetadata(b.PG, check.Columns, check.Types)
					run(check)
				})
			})
		})
	}
}
