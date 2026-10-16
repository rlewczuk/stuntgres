package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantAddressAuthority(t *testing.T) {
	// Fixed release identities and independent type-integer answers. OUT slots
	// never participate in the three-input grant identity. These are INTERNAL
	// STABLE declarations, not SQL wrappers with a warm inlining exception.
	for _, declaration := range []struct {
		name, signature, typ, value string
		oid                         int64
		columns, types              []string
		row                         []any
	}{
		{"pg_identify_object", "oid,oid,integer", "RECORD", "(type,pg_catalog,int4,integer)", 3839, []string{"type", "schema", "name", "identity"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, []any{"type", "pg_catalog", "int4", "integer"}},
		{"pg_identify_object_as_address", "oid,oid,integer", "RECORD", "(type,{integer},{})", 3382, []string{"type", "object_names", "object_args"}, []string{"TEXT", "_TEXT", "_TEXT"}, []any{"type", "{integer}", "{}"}},
		{"pg_get_object_address", "text,text[],text[]", "RECORD", "(1247,23,0)", 3954, []string{"classid", "objid", "objsubid"}, []string{"OID", "OID", "INT4"}, []any{int64(1247), int64(23), int64(0)}},
		{"pg_describe_object", "oid,oid,integer", "TEXT", "type integer", 3537, []string{"pg_describe_object"}, []string{"TEXT"}, []any{"type integer"}},
	} {
		t.Run(declaration.name, func(t *testing.T) {
			objectGrantScenario(t, []string{"og_addr_auth"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				d := declaration
				inverse, record := d.name == "pg_get_object_address", d.typ == "RECORD"
				target := "pg_catalog." + d.name + "(" + d.signature + ")"
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_addr_input(id INT,c OID,o OID,s INT,k TEXT,n TEXT[],a TEXT[])", 0))
				run(fixture.Exec("INSERT INTO og_addr_input VALUES(1,1247,23,0,'type','{integer}','{}'),(2,NULL,NULL,NULL,NULL,NULL,NULL)", 2))
				run(fixture.Exec("GRANT SELECT ON og_addr_input TO og_object_reader", 0))
				literal := []string{"1247::oid", "23::oid", "0::integer"}
				fields := []string{"c", "o", "s"}
				casts := []string{"oid", "oid", "integer"}
				valid := []any{int64(1247), int64(23), int64(0)}
				if inverse {
					literal = []string{"'type'::text", "'{integer}'::text[]", "'{}'::text[]"}
					fields, casts, valid = []string{"k", "n", "a"}, []string{"text", "text[]", "text[]"}, []any{"type", "{integer}", "{}"}
				}
				call := func(args []string) string { return "pg_catalog." + d.name + "(" + strings.Join(args, ",") + ")" }
				constant, rowCall := call(literal), call(fields)
				value := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_addr_input"+tail, []string{"v"}, []string{d.typ}, [][]any{{want}}, args...)
				}
				expandedRow := append([]any(nil), d.row...)
				if inverse && b.PG {
					expandedRow = []any{"1247", "23", int64(0)}
				}
				from := func(expr string, null, ordinal bool, args ...any) fixture.Step {
					cols, types := append([]string(nil), d.columns...), append([]string(nil), d.types...)
					row := append([]any(nil), expandedRow...)
					if null {
						row = make([]any, len(cols))
					}
					q := "SELECT * FROM " + expr
					if ordinal {
						q += " WITH ORDINALITY"
						cols, types, row = append(cols, "ordinality"), append(types, "INT8"), append(row, int64(1))
					}
					return fixture.Select(q, cols, types, [][]any{row}, args...)
				}
				viewBody := "SELECT " + rowCall + " AS v FROM og_addr_input WHERE id=1"
				viewColumns, viewTypes, viewRow := []string{"v"}, []string{d.typ}, []any{d.value}
				if record {
					viewBody = "SELECT (" + rowCall + ").* FROM og_addr_input WHERE id=1"
					viewColumns, viewTypes, viewRow = d.columns, d.types, expandedRow
				}
				run(fixture.Exec("CREATE VIEW og_addr_view AS "+viewBody, 0))
				run(fixture.Exec("GRANT SELECT ON og_addr_view TO og_object_reader", 0))
				cases := []objectGrantProviderCase{
					{value(rowCall, " WHERE id=1", d.value), true, true},
					{value(rowCall, " WHERE id=2", nil), true, true},
					{value(constant, " WHERE id=1", d.value), true, true},
					{from(constant, false, false), true, true},
					{from(constant, false, true), true, true},
					{fixture.Select("SELECT * FROM og_addr_view", viewColumns, viewTypes, [][]any{viewRow}), true, true},
					{value("CASE WHEN id=$1::integer THEN "+rowCall+" ELSE NULL END", " WHERE id=1", d.value, int64(1)), true, true},
					{value("CASE WHEN FALSE THEN "+rowCall+" ELSE NULL END", " WHERE id=1", nil), false, true},
				}
				// These zero-row target/source distinctions are PG-first hypotheses,
				// not inferred from the absence of returned data.
				for _, tail := range []string{" WHERE id>99", " WHERE FALSE", " LIMIT 0"} {
					cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT "+rowCall+" AS v FROM og_addr_input"+tail, []string{"v"}, []string{d.typ}, nil), true, true})
				}
				for _, tail := range []string{" WHERE FALSE", " LIMIT 0"} {
					s := from(constant, false, false)
					s.Query, s.Rows = s.Query+tail, nil
					cases = append(cases, objectGrantProviderCase{s, tail == " LIMIT 0", true})
				}
				parameters := []string{"$1::" + casts[0], "$2::" + casts[1], "$3::" + casts[2]}
				cases = append(cases, objectGrantProviderCase{value(call(parameters), " WHERE id=1", d.value, valid...), true, true})
				for slot := range 3 {
					nulls := append([]string(nil), literal...)
					nulls[slot] = "NULL::" + casts[slot]
					bound := append([]any(nil), valid...)
					bound[slot] = nil
					cases = append(cases,
						objectGrantProviderCase{value(call(nulls), " WHERE id=1", nil), record, true},
						objectGrantProviderCase{from(call(nulls), true, true), record, true},
						objectGrantProviderCase{value(call(parameters), " WHERE id=1", nil, bound...), record, false},
						objectGrantProviderCase{from(call(parameters), true, true, bound...), record, false},
					)
					mixed := from(call(nulls), true, false)
					mixed.Query = "SELECT * FROM ROWS FROM(" + call(nulls) + ",pg_describe_object(NULL,23,0)) WITH ORDINALITY"
					mixed.Columns = append(mixed.Columns, "pg_describe_object", "ordinality")
					mixed.Types = append(mixed.Types, "TEXT", "INT8")
					mixed.Rows[0] = append(mixed.Rows[0], nil, int64(1))
					cases = append(cases, objectGrantProviderCase{mixed, record, true})
				}
				runtimeFrom := from(rowCall, true, true)
				runtimeFrom.Query = strings.Replace(runtimeFrom.Query, "SELECT * FROM ", "SELECT f.* FROM og_addr_input i,LATERAL ", 1) + " AS f WHERE i.id=2"
				if !record {
					runtimeFrom.Columns[0] = "f"
				}
				cases = append(cases, objectGrantProviderCase{runtimeFrom, true, true})
				if d.name != "pg_describe_object" {
					named := "objsubid=>s,objid=>o,classid=>c"
					if inverse {
						named = "object_args=>a,type=>k,object_names=>n"
					}
					cases = append(cases, objectGrantProviderCase{value("pg_catalog."+d.name+"("+named+")", " WHERE id=1", d.value), true, true})
				}
				// Identify a function whose own EXECUTE is revoked. Its fixed OID
				// is data, never a hidden SQL invocation or an expected-value oracle.
				oid := fmt.Sprint(d.oid)
				control := fixture.Select("SELECT pg_describe_object(1255,"+oid+",0) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"function " + d.name + "(" + d.signature + ")"}})
				if d.name == "pg_describe_object" {
					control = fixture.Select("SELECT * FROM pg_get_object_address('function',ARRAY['pg_catalog','pg_describe_object'],ARRAY['oid','oid','integer'])", []string{"classid", "objid", "objsubid"}, []string{"OID", "OID", "INT4"}, [][]any{{int64(1255), int64(3537), int64(0)}})
					if b.PG {
						control.Rows = [][]any{{"1255", "3537", int64(0)}}
					}
				}
				identifyingControl := fixture.Select("SELECT classid::bigint AS class,objid::bigint AS object,objsubid FROM pg_get_object_address('function',ARRAY['pg_catalog','"+d.name+"'],ARRAY['"+strings.ReplaceAll(d.signature, ",", "','")+"'])", []string{"class", "object", "objsubid"}, []string{"INT8", "INT8", "INT4"}, [][]any{{int64(1255), d.oid, int64(0)}})
				if inverse {
					identifyingControl = fixture.Select("SELECT * FROM pg_identify_object_as_address(1255,3954,0)", []string{"type", "object_names", "object_args"}, []string{"TEXT", "_TEXT", "_TEXT"}, [][]any{{"function", "{pg_catalog,pg_get_object_address}", "{pg_catalog.text,pg_catalog.text[],pg_catalog.text[]}"}})
				}
				// Full release metadata remains a real PostgreSQL contract, with
				// the established native missing-column boundary made explicit.
				result, inputs := int64(2249), "26 26 23"
				var argNames, allTypes, modes any
				switch d.name {
				case "pg_identify_object":
					argNames, allTypes, modes = "{classid,objid,objsubid,type,schema,name,identity}", "{26,26,23,25,25,25,25}", "{i,i,i,o,o,o,o}"
				case "pg_identify_object_as_address":
					argNames, allTypes, modes = "{classid,objid,objsubid,type,object_names,object_args}", "{26,26,23,25,1009,1009}", "{i,i,i,o,o,o}"
				case "pg_get_object_address":
					inputs = "25 1009 1009"
					argNames, allTypes, modes = "{type,object_names,object_args,classid,objid,objsubid}", "{25,1009,1009,26,26,23}", "{i,i,i,o,o,o}"
				default:
					result = 25
				}
				full := fixture.Select("SELECT prokind,proargtypes::text AS inputs,prorettype::bigint AS result,proisstrict,provolatile,proparallel,proretset,provariadic::bigint AS variadic,pronargdefaults,proargnames::text AS names,proallargtypes::text AS alltypes,proargmodes::text AS modes FROM pg_proc WHERE oid=$1",
					[]string{"prokind", "inputs", "result", "proisstrict", "provolatile", "proparallel", "proretset", "variadic", "pronargdefaults", "names", "alltypes", "modes"},
					[]string{"CHAR", "TEXT", "INT8", "BOOL", "CHAR", "CHAR", "BOOL", "INT8", "INT2", "TEXT", "TEXT", "TEXT"},
					[][]any{{"f", inputs, result, true, "s", "s", false, int64(0), int64(0), argNames, allTypes, modes}}, d.oid)
				if !b.PG {
					full = fixture.ImplicitError(full.Query, fixture.Diagnostic{Code: "42703", Message: `column "prokind" does not exist`, Position: "8"}, d.oid)
					full.SelectRows, full.ErrorStage = true, "execute"
					if prepared {
						full.ErrorStage = "prepare"
					}
				}
				run(full)
				objectGrantProviderLifecycle(t, p, b, c, run, target, "function", d.name, cases, []fixture.Step{control, identifyingControl})

				// Additional error/effect witnesses reuse actual providers and SQL
				// state. Each denied write leaves the original table unchanged.
				defer objectGrantRoutineCleanup(t, p, c, []string{target, "pg_catalog.nextval(regclass)"})
				run(fixture.Exec("CREATE TABLE og_addr_sink(v TEXT)", 0))
				run(fixture.Exec("GRANT INSERT,SELECT ON og_addr_sink TO og_object_reader", 0))
				run(fixture.Exec("CREATE SEQUENCE og_addr_effect START 40", 0))
				run(fixture.Exec("GRANT USAGE,SELECT ON SEQUENCE og_addr_effect TO og_object_reader", 0))
				run(fixture.Exec("CREATE SCHEMA og_addr_auth", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA og_addr_auth TO og_object_reader", 0))
				run(fixture.Exec("CREATE DOMAIN og_addr_auth.positive AS integer CONSTRAINT positive CHECK(VALUE>0)", 0))
				failure := func(q string, want fixture.Diagnostic, binding bool, args ...any) fixture.Step {
					s := fixture.ImplicitError(q, want, args...)
					s.SelectRows, s.ErrorStage = true, "execute"
					if binding && prepared {
						s.ErrorStage = "prepare"
					}
					return s
				}
				denial := fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + d.name}
				for _, allowed := range []bool{true, false, true, false} {
					if allowed {
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
					} else {
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC,og_object_reader", 0))
					}
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						bodyArgs := []string{"999999", "0", "0"}
						bodyError := fixture.Diagnostic{Code: "XX000", Message: "unsupported object class: 999999"}
						if inverse {
							bodyArgs = []string{"'stone'", "'{integer}'", "'{}'"}
							bodyError = fixture.Diagnostic{Code: "22023", Message: `unrecognized object type "stone"`}
						}
						want := bodyError
						if !allowed {
							want = denial
						}
						run(failure("SELECT "+call(bodyArgs)+" AS v", want, false))
						if inverse {
							for _, malformed := range []struct {
								args []string
								want fixture.Diagnostic
							}{
								{[]string{"'type'", "ARRAY[NULL]::text[]", "'{}'"}, fixture.Diagnostic{Code: "22023", Message: "name or argument lists may not contain nulls"}},
								{[]string{"'type'", "'{public.og_addr_missing}'", "'{}'"}, fixture.Diagnostic{Code: "42704", Message: `type "public.og_addr_missing" does not exist`}},
							} {
								want := malformed.want
								if !allowed {
									want = denial
								}
								run(failure("SELECT "+call(malformed.args), want, false))
							}
						}
						// Binding/import errors survive every outer NULL and ACL phase.
						literalBad := []string{"NULL", "23", "'bad'"}
						literalWant := fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`}
						if inverse {
							literalBad = []string{"NULL", "'bad'::text[]", "'{}'"}
							literalWant.Message, literalWant.Detail = `malformed array literal: "bad"`, `Array value must start with "{" or dimension information.`
						}
						literalQuery := "SELECT " + call(literalBad)
						literalWant.Position = fmt.Sprint(strings.Index(literalQuery, "'bad'") + 1)
						run(failure(literalQuery, literalWant, true))
						for slot := range 3 {
							if inverse && slot == 0 {
								continue
							}
							args := append([]any(nil), valid...)
							args[slot], args[(slot+1)%3] = "bad", nil
							typeName := casts[slot]
							want := fixture.Diagnostic{Code: "22P02", Message: fmt.Sprintf("invalid input syntax for type %s: %q", typeName, "bad"), Context: fmt.Sprintf("unnamed portal parameter $%d = '...'", slot+1)}
							if inverse {
								want.Message, want.Detail = `malformed array literal: "bad"`, `Array value must start with "{" or dimension information.`
							}
							run(failure("SELECT "+call(parameters), want, false, args...))
						}
						q := "INSERT INTO og_addr_sink SELECT 'prefix' FROM og_addr_input WHERE id=1 AND " + rowCall + " IS NOT NULL"
						if !allowed {
							s := failure(q, denial, false)
							s.SelectRows = false
							run(s)
						}
						run(fixture.Select("SELECT v FROM og_addr_sink", []string{"v"}, []string{"TEXT"}, nil))
					})
				}

				// Known-NULL scalar pruning must discard actual volatile and
				// revoked sibling demand, including FROM and ROWS FROM. RECORD
				// and runtime-NULL counterparts still initialize their providers.
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				for slot := range 3 {
					for _, nullMode := range []string{"literal", "bound", "runtime"} {
						nulls := append([]string(nil), literal...)
						nulls[slot] = "NULL::" + casts[slot]
						var args []any
						tail := " WHERE id=1"
						if nullMode == "bound" {
							nulls[slot], args = "$1::"+casts[slot], []any{nil}
						}
						if nullMode == "runtime" {
							nulls[slot], tail = fields[slot], " WHERE id=2"
						}
						effectSlot := (slot + 1) % 3
						effect := "nextval('og_addr_effect')::" + casts[effectSlot]
						if inverse {
							effect = "ARRAY[nextval('og_addr_effect')::text]"
							if effectSlot == 0 {
								effect = "nextval('og_addr_effect')::text"
							}
						}
						nulls[effectSlot] = effect
						for _, source := range []bool{false, true} {
							run(fixture.Select("SELECT setval('og_addr_effect',40,true) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}}))
							fixture.RoleGrantActor(run, "og_object_reader", func() {
								s := value(call(nulls), tail, nil, args...)
								if source {
									s = from(call(nulls), true, true, args...)
									if nullMode == "runtime" {
										s.Query = strings.Replace(s.Query, "SELECT * FROM ", "SELECT f.* FROM og_addr_input i,LATERAL ", 1) + " AS f WHERE i.id=2"
										if !record {
											s.Columns[0] = "f"
										}
									}
								}
								run(s)
							})
							want := int64(40)
							if record || nullMode == "runtime" {
								want++
							}
							run(fixture.Select("SELECT last_value FROM og_addr_effect", []string{"last_value"}, []string{"INT8"}, [][]any{{want}}))
						}
					}
				}
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.nextval(regclass) FROM PUBLIC", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					for _, bound := range []bool{false, true} {
						argsSQL := append([]string(nil), literal...)
						argsSQL[0] = "NULL::" + casts[0]
						var args []any
						if bound {
							argsSQL[0], args = "$1::"+casts[0], []any{nil}
						}
						argsSQL[1] = "nextval('og_addr_effect')::oid"
						if inverse {
							argsSQL[1] = "ARRAY[nextval('og_addr_effect')::text]"
						}
						expr := call(argsSQL)
						for _, source := range []bool{false, true} {
							s := value(expr, " WHERE id=1", nil, args...)
							if source {
								s = from(expr, true, true, args...)
							}
							if record {
								s = failure(s.Query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function nextval"}, false, args...)
							}
							run(s)
						}
					}
				})
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.nextval(regclass) TO PUBLIC", 0))
				if d.name == "pg_describe_object" {
					for _, allowed := range []bool{false, true} {
						if allowed {
							run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
						} else {
							run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC,og_object_reader", 0))
						}
						fixture.RoleGrantActor(run, "og_object_reader", func() {
							for _, null := range []string{"NULL::oid", "$1::oid"} {
								var args []any
								if null[0] == '$' {
									args = []any{nil}
								}
								for _, sibling := range []string{"1/0", "(-1)::og_addr_auth.positive"} {
									expr := "pg_describe_object(" + null + ",23," + sibling + ")"
									want := fixture.Diagnostic{Code: "22012", Message: "division by zero"}
									for _, q := range []string{"SELECT " + expr, "SELECT * FROM " + expr} {
										if sibling != "1/0" {
											// Both engines prune this domain coercion with
											// the strict NULL call; parameter input is separate.
											run(fixture.Select(q, []string{"pg_describe_object"}, []string{"TEXT"}, [][]any{{nil}}, args...))
										} else {
											run(failure(q, want, false, args...))
										}
									}
								}
							}
							run(fixture.Select("SELECT * FROM ROWS FROM(pg_describe_object(NULL,23,0),pg_describe_object(1247,NULL,0)) WITH ORDINALITY AS f(v,w,n)", []string{"v", "w", "n"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{nil, nil, int64(1)}}))
						})
					}
				}
				// Original SAME held handle: ordinary native precancellation occurs
				// before a known-NULL shortcut; the next call still works. The PG
				// cancellation transport contract is covered by the address baseline.
				if !b.PG {
					q := "SELECT " + call(parameters) + " AS v"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					for _, held := range []bool{false, true} {
						args := append([]any(nil), valid...)
						args[0] = nil
						var rows *sql.Rows
						if held {
							rows, err = stmt.QueryContext(ctx, args...)
						} else {
							rows, err = c.QueryContext(ctx, q, args...)
						}
						if rows != nil {
							rows.Close()
							t.Fatal("canceled call published rows")
						}
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("ordinary cancellation: %v", err)
						}
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{d.typ}, [][]any{{d.value}}, valid...))
				}
			})
		})
	}
}
