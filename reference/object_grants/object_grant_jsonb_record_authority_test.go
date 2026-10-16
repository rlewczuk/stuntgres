package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// JSONB has two populate inputs and no legacy JSON BOOLEAN default. The shared
// demand contract is pinned to PostgreSQL 18.0; physical result shape does not
// replace the released declaration's strictness or set-returning contract.
func TestReferenceObjectGrantJSONBRecordAuthority(t *testing.T) {
	families := []struct {
		name, target                 string
		populate, set, strict, valid bool
	}{
		{"jsonb_populate_record", "pg_catalog.jsonb_populate_record(anyelement,jsonb)", true, false, false, false},
		{"jsonb_populate_recordset", "pg_catalog.jsonb_populate_recordset(anyelement,jsonb)", true, true, false, false},
		{"jsonb_to_record", "pg_catalog.jsonb_to_record(jsonb)", false, false, true, false},
		{"jsonb_to_recordset", "pg_catalog.jsonb_to_recordset(jsonb)", false, true, false, false},
		{"jsonb_populate_record_valid", "pg_catalog.jsonb_populate_record_valid(anyelement,jsonb)", true, false, false, true},
	}
	for _, family := range families {
		t.Run(family.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO og_object_other", 0))
				run(fixture.Exec("CREATE TYPE og_jbr_pair AS (n INTEGER,s TEXT)", 0))
				run(fixture.Exec("CREATE TABLE og_jbr_physical(n INTEGER,s TEXT)", 0))
				run(fixture.Exec("CREATE TABLE og_jbr_input(id INTEGER,j JSONB,js JSONB)", 0))
				run(fixture.Exec(`INSERT INTO og_jbr_input VALUES(1,'{"n":7,"s":"ok"}','[{"n":7,"s":"ok"},{"n":8}]'),(2,NULL,NULL)`, 2))
				run(fixture.Exec("CREATE TABLE og_jbr_sink(n INTEGER,s TEXT)", 0))
				run(fixture.Exec("INSERT INTO og_jbr_sink VALUES(99,'sentinel')", 1))
				run(fixture.Exec("GRANT SELECT ON og_jbr_input,og_jbr_sink TO og_object_reader,og_object_other", 0))
				run(fixture.Exec("GRANT INSERT ON og_jbr_sink TO og_object_reader", 0))

				// Full released pg_proc contract is independent of the supported native
				// projection. Do not add catalog columns to make this boundary pass.
				full := "SELECT p.oid::text AS oid,p.proargtypes::text AS args,p.prorettype::text AS result,p.proname,p.prokind::text AS kind,p.pronargs,p.prolang::text AS lang,p.proisstrict,p.proretset,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.provariadic::text AS variadic,p.pronargdefaults,p.proargnames::text AS names,pg_get_expr(p.proargdefaults,0) AS defaults,p.prosrc FROM pg_proc p WHERE p.oid IN (3209,3475,3490,3491,6338) ORDER BY p.oid"
				metadata := fixture.Select(full,
					[]string{"oid", "args", "result", "proname", "kind", "pronargs", "lang", "proisstrict", "proretset", "volatility", "parallel", "variadic", "pronargdefaults", "names", "defaults", "prosrc"},
					[]string{"TEXT", "TEXT", "TEXT", "NAME", "TEXT", "INT2", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "INT2", "TEXT", "TEXT", "TEXT"},
					[][]any{
						{"3209", "2283 3802", "2283", "jsonb_populate_record", "f", int64(2), "12", false, false, "s", "s", "0", int64(0), nil, nil, "jsonb_populate_record"},
						{"3475", "2283 3802", "2283", "jsonb_populate_recordset", "f", int64(2), "12", false, true, "s", "s", "0", int64(0), nil, nil, "jsonb_populate_recordset"},
						{"3490", "3802", "2249", "jsonb_to_record", "f", int64(1), "12", true, false, "s", "s", "0", int64(0), nil, nil, "jsonb_to_record"},
						{"3491", "3802", "2249", "jsonb_to_recordset", "f", int64(1), "12", false, true, "s", "s", "0", int64(0), nil, nil, "jsonb_to_recordset"},
						{"6338", "2283 3802", "16", "jsonb_populate_record_valid", "f", int64(2), "12", false, false, "s", "s", "0", int64(0), nil, nil, "jsonb_populate_record_valid"},
					})
				if !b.PG {
					metadata = fixture.ImplicitError(full, fixture.Diagnostic{Code: "42703", Message: "column p.proargtypes does not exist", Position: fixture.Position(strings.Index(full, "p.proargtypes") + 1)})
					metadata.SelectRows, metadata.ErrorStage = true, "execute"
					if prepared {
						metadata.ErrorStage = "prepare"
					}
				}
				run(metadata)
				run(fixture.Select("SELECT oid::text AS oid,proname,pronamespace::text AS namespace,proowner>0 AS owned,pronargs,prorettype::text AS result,provolatile,proisstrict,proparallel,oid::regprocedure::text AS identity FROM pg_proc WHERE oid IN (3209,3475,3490,3491,6338) ORDER BY oid",
					[]string{"oid", "proname", "namespace", "owned", "pronargs", "result", "provolatile", "proisstrict", "proparallel", "identity"},
					[]string{"TEXT", "NAME", "TEXT", "BOOL", "INT2", "TEXT", "CHAR", "BOOL", "CHAR", "TEXT"},
					[][]any{
						{"3209", "jsonb_populate_record", "11", true, int64(2), "2283", "s", false, "s", "jsonb_populate_record(anyelement,jsonb)"},
						{"3475", "jsonb_populate_recordset", "11", true, int64(2), "2283", "s", false, "s", "jsonb_populate_recordset(anyelement,jsonb)"},
						{"3490", "jsonb_to_record", "11", true, int64(1), "2249", "s", true, "s", "jsonb_to_record(jsonb)"},
						{"3491", "jsonb_to_recordset", "11", true, int64(1), "2249", "s", false, "s", "jsonb_to_recordset(jsonb)"},
						{"6338", "jsonb_populate_record_valid", "11", true, int64(2), "16", "s", false, "s", "jsonb_populate_record_valid(anyelement,jsonb)"},
					}))
				pair := func(q string, rows [][]any, args ...any) fixture.Step {
					s := fixture.Select(q, []string{"n", "s"}, []string{"INT4", "TEXT"}, rows, args...)
					s.CheckMetadata = fixture.StringEncodingMetadata(b.PG, s.Columns, s.Types)
					return s
				}
				boolean := func(q string, rows [][]any, args ...any) fixture.Step {
					return fixture.Select(q, []string{"valid"}, []string{"BOOL"}, rows, args...)
				}
				failure := func(q, code, message string, args ...any) fixture.Step {
					s := fixture.ImplicitError(q, fixture.ControlsReferenceDiagnostic(code, message), args...)
					s.SelectRows, s.ErrorStage = true, "execute"
					return s
				}
				call := func(argument, base string) string {
					if family.populate {
						return "pg_catalog." + family.name + "(" + base + "," + argument + ")"
					}
					return "pg_catalog." + family.name + "(" + argument + ")"
				}
				source := func(argument, base string) string {
					s := call(argument, base)
					if !family.populate {
						return s + " AS r(n INTEGER,s TEXT)"
					}
					return s + " AS r"
				}
				query := func(argument, base string) string {
					if family.valid {
						return "SELECT " + call(argument, base) + " AS valid"
					}
					return "SELECT n,s FROM " + source(argument, base) + " ORDER BY n NULLS LAST"
				}
				base, input, column := "NULL::og_jbr_pair", `{"n":7,"s":"ok"}`, "i.j"
				rows := [][]any{{int64(7), "ok"}}
				selectResult := pair
				if family.set {
					input, column = `[{"n":7,"s":"ok"},{"n":8}]`, "i.js"
					rows = append(rows, []any{int64(8), nil})
				}
				if family.valid {
					rows, selectResult = [][]any{{true}}, boolean
				}
				q, literal := query("$1::jsonb", base), query("'"+input+"'::jsonb", base)
				rowQuery := "SELECT r.n,r.s FROM og_jbr_input i CROSS JOIN LATERAL " + source(column, base) + " WHERE i.id=$1 ORDER BY r.n NULLS LAST"
				if family.valid {
					rowQuery = "SELECT " + call(column, base) + " AS valid FROM og_jbr_input i WHERE i.id=$1"
				}
				// A real stored recipe owned by a different principal with independent
				// EXECUTE must not supply the reader's routine authority.
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+family.target+" TO og_object_other", 0))
				defer func() { run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+family.target+" FROM og_object_other", 0)) }()
				cases := []objectGrantProviderCase{
					{selectResult(literal, rows), true, true},
					{selectResult(q, rows, input), true, true},
					{selectResult(rowQuery, rows, int64(1)), true, true},
					{selectResult(literal+" LIMIT 0", nil), true, true},
				}
				for _, view := range []struct{ name, option string }{
					{"og_jbr_owner", ""}, {"og_jbr_invoker", " WITH(security_invoker=true)"},
				} {
					run(fixture.Exec("CREATE VIEW "+view.name+view.option+" AS "+literal, 0))
					run(fixture.Exec("ALTER VIEW "+view.name+" OWNER TO og_object_other", 0))
					run(fixture.Exec("GRANT SELECT ON "+view.name+" TO og_object_reader", 0))
					fields := "n,s"
					if family.valid {
						fields = "valid"
					}
					s := selectResult("SELECT "+fields+" FROM "+view.name, rows)
					if !family.valid {
						s.Query += " ORDER BY n NULLS LAST"
						s.CheckMetadata = fixture.StringEncodingViewMetadata(b.PG, s.Columns, s.Types)
					}
					cases = append(cases, objectGrantProviderCase{s, true, true})
				}
				nullRows := [][]any{{nil, nil}}
				if family.set {
					nullRows = nil
				}
				if family.valid {
					nullRows = [][]any{{nil}}
				}
				cases = append(cases,
					objectGrantProviderCase{selectResult(query("NULL::jsonb", base), nullRows), true, true},
					objectGrantProviderCase{selectResult(q, nullRows, nil), true, true},
					objectGrantProviderCase{selectResult(rowQuery, nullRows, int64(2)), true, true})
				if family.valid {
					cases = append(cases,
						objectGrantProviderCase{boolean(query("$1::jsonb", base), [][]any{{false}}, `{"n":"bad"}`), true, true},
						objectGrantProviderCase{boolean(query("NULL::jsonb", "'(8,base)'::og_jbr_pair"), [][]any{{true}}), true, true},
						objectGrantProviderCase{boolean(literal+" WHERE FALSE", nil), true, true},
						objectGrantProviderCase{boolean("SELECT valid FROM "+call("'"+input+"'::jsonb", base)+" AS r(valid) WHERE FALSE", nil), false, true})
					cases = append(cases,
						objectGrantProviderCase{boolean("SELECT valid FROM "+call("$1::jsonb", base)+" AS r(valid)", [][]any{{true}}, input), true, true},
						objectGrantProviderCase{boolean("SELECT valid FROM "+call("NULL::jsonb", base)+" AS r(valid)", [][]any{{nil}}), true, true},
						objectGrantProviderCase{boolean("SELECT valid FROM "+call("'"+input+"'::jsonb", base)+" AS r(valid) LIMIT 0", nil), true, true},
						objectGrantProviderCase{boolean("SELECT r.valid FROM og_jbr_input i CROSS JOIN LATERAL "+call("i.j", base)+" AS r(valid) WHERE i.id=$1", [][]any{{true}}, int64(1)), true, true})
					// Reuse the existing genuine domain CHECK-expression error fixture:
					// an exception raised inside CHECK is hard, not a saved false result.
					run(fixture.Exec("CREATE DOMAIN og_jbr_cast_check AS TEXT CONSTRAINT og_jbr_cast_check CHECK(VALUE::INTEGER>0)", 0))
					run(fixture.Exec("CREATE TYPE og_jbr_checked AS (n og_jbr_cast_check)", 0))
					cases = append(cases, objectGrantProviderCase{failure(query("$1::jsonb", "NULL::og_jbr_checked"), "22P02", `invalid input syntax for type integer: "bad"`, `{"n":"bad"}`), true, true})
					// The ordinary sibling still throws for the conversion valid softens.
					run(failure(`SELECT n,s FROM jsonb_populate_record(NULL::og_jbr_pair,$1::jsonb)`, "22P02", `invalid input syntax for type integer: "bad"`, `{"n":"bad"}`))
				} else {
					falseQuery := "SELECT n,s FROM " + source("'"+input+"'::jsonb", base) + " WHERE FALSE"
					cases = append(cases, objectGrantProviderCase{pair(falseQuery, nil), false, true})
					if family.set {
						cases = append(cases, objectGrantProviderCase{pair(query("'[]'::jsonb", base), nil), true, true})
					}
					badInput := `{"n":"bad"}`
					if family.set {
						badInput = `[{"n":7},{"n":"bad"}]`
					}
					cases = append(cases, objectGrantProviderCase{failure(q, "22P02", `invalid input syntax for type integer: "bad"`, badInput), true, true})
				}
				malformed := failure(q, "22P02", "invalid input syntax for type json", `{"n":}`)
				malformed.Diagnostic.Detail = `Expected JSON value, but found "}".`
				malformed.Diagnostic.Context = "JSON data, line 1: {\"n\":}\nunnamed portal parameter $1 = '...'"
				cases = append(cases, objectGrantProviderCase{malformed, false, true})
				if family.populate {
					cases = append(cases, objectGrantProviderCase{selectResult(query("$2::jsonb", "$1::og_jbr_pair"), rows, nil, input), true, true})
					retainedInput, retainedRows := `{"n":8}`, [][]any{{int64(8), "base"}}
					if family.set {
						retainedInput = `[{"n":8}]`
					}
					if family.valid {
						retainedRows = [][]any{{true}}
					}
					cases = append(cases,
						objectGrantProviderCase{selectResult(query("$2::jsonb", "$1::og_jbr_pair"), retainedRows, "(9,base)", retainedInput), true, true},
						objectGrantProviderCase{selectResult(query("'"+input+"'::jsonb", "NULL::og_jbr_physical"), rows), true, true})
					if !family.valid {
						target := "SELECT " + call("$1::jsonb", base) + " AS r"
						composites := [][]any{{"(7,ok)"}}
						if family.set {
							composites = append(composites, []any{"(8,)"})
						}
						for _, suffix := range []string{"", " WHERE FALSE", " LIMIT 0"} {
							want := composites
							if suffix != "" {
								want = nil
							}
							s := fixture.Select(target+suffix, []string{"r"}, []string{""}, want, input)
							s.RawTypes, s.Bag = []string{"RECORD"}, family.set
							cases = append(cases, objectGrantProviderCase{s, true, true})
						}
						if !family.set {
							cases = append(cases, objectGrantProviderCase{pair(query("NULL::jsonb", "'(8,base)'::og_jbr_pair"), [][]any{{int64(8), "base"}}), true, true})
						}
					}
				} else if family.strict {
					cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT pg_catalog.jsonb_to_record(NULL::jsonb) AS r", []string{"r"}, []string{"RECORD"}, [][]any{{nil}}), true, true})
				}
				controls := []fixture.Step{
					fixture.Select(`SELECT jsonb_array_length('[1,2]'::jsonb) AS n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}),
					pair(`SELECT n,s FROM json_to_record('{"n":7,"s":"ok"}'::json) AS r(n INTEGER,s TEXT)`, [][]any{{int64(7), "ok"}}),
				}
				for _, sibling := range families {
					if sibling.name == family.name {
						continue
					}
					argument := `'{"n":7,"s":"ok"}'::jsonb`
					want := [][]any{{int64(7), "ok"}}
					if sibling.set {
						argument = `'[{"n":7,"s":"ok"},{"n":8}]'::jsonb`
						want = append(want, []any{int64(8), nil})
					}
					invocation := "pg_catalog." + sibling.name + "(" + argument + ") AS r(n INTEGER,s TEXT)"
					if sibling.populate {
						invocation = "pg_catalog." + sibling.name + "(NULL::og_jbr_pair," + argument + ")"
					}
					if sibling.valid {
						controls = append(controls, boolean("SELECT "+invocation+" AS valid", [][]any{{true}}))
					} else {
						controls = append(controls, pair("SELECT n,s FROM "+invocation+" ORDER BY n NULLS LAST", want))
					}
				}
				objectGrantProviderLifecycle(t, p, b, c, run, family.target, "function", family.name, cases, controls)

				if !family.valid {
					// Real write consumer: denied initialization cannot publish a prefix.
					defer objectGrantRoutineCleanup(t, p, c, []string{family.target})
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+family.target+" FROM PUBLIC", 0))
					insert := "INSERT INTO og_jbr_sink(n,s) " + literal
					checkSink := func(want [][]any) {
						s := pair("SELECT n,s FROM og_jbr_sink ORDER BY n", want)
						s.CheckMetadata = fixture.StringEncodingViewMetadata(b.PG, s.Columns, s.Types)
						run(s)
					}
					denied := func(want [][]any) {
						fixture.RoleGrantActor(run, "og_object_reader", func() {
							s := fixture.ImplicitError(insert, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+family.name))
							s.ErrorStage = "execute"
							run(s)
							checkSink(want)
						})
					}
					denied([][]any{{int64(99), "sentinel"}})
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+family.target+" TO og_object_reader", 0))
					want := append(append([][]any(nil), rows...), []any{int64(99), "sentinel"})
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						run(fixture.Exec(insert, int64(len(rows))))
						checkSink(want)
					})
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+family.target+" FROM og_object_reader", 0))
					denied(want)
				}
			})
		})
	}
}
