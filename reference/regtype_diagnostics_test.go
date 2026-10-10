package reference_test

import (
	"database/sql"
	"testing"
)

// Independent source expectations completed by the pinned PostgreSQL 18.0
// diagnostics in tests-1.md, including every empty optional field.
type regtypeInvalidInput struct {
	input, state, primary, position string
	parseContext                    bool
}

var regtypeInvalidInputs = []regtypeInvalidInput{
	{"missing_regtype_name", "42704", "type \"missing_regtype_name\" does not exist", "", false},
	{"regtype_a.missing_regtype_name", "42704", "type \"regtype_a.missing_regtype_name\" does not exist", "", false},
	{"absent_regtype_schema.d", "3F000", "schema \"absent_regtype_schema\" does not exist", "", false},
	{"", "42601", "invalid type name \"\"", "", false},
	{" \t ", "42601", "invalid type name \" \t \"", "", false},
	{"SETOF integer", "42601", "invalid type name \"SETOF integer\"", "", false},
	{"integer(", "42601", "syntax error at or near \"(\"", "8", true},
	{"int4(", "42601", "syntax error at end of input", "6", true},
	{"integer; SELECT 1", "42601", "syntax error at or near \";\"", "8", true},
	{"integer,text", "42601", "syntax error at or near \",\"", "8", true},
	{"integer..x", "42601", "syntax error at or near \"..\"", "8", true},
	{"\"name", "42601", "unterminated quoted identifier at or near \"\"name\"", "1", true},
	{"\"\"", "42601", "zero-length delimited identifier at or near \"\"\"\"", "1", true},
	{"integer(4)", "42601", "syntax error at or near \"(\"", "8", true},
	{"int4(4)", "42601", "type modifier is not allowed for type \"int4\"", "", false},
	{"pg_catalog.int4(4)", "42601", "type modifier is not allowed for type \"pg_catalog.int4\"", "", false},
	{"inet(3)", "42601", "type modifier is not allowed for type \"inet\"", "", false},
	{"pg_catalog.float8(25)", "42601", "type modifier is not allowed for type \"pg_catalog.float8\"", "", false},
	{"information_schema.character_data(5)", "42601", "type modifier is not allowed for type \"information_schema.character_data\"", "", false},
	{"numeric(1+2)", "42601", "type modifiers must be simple constants or identifiers", "", false},
	{"varchar(0)", "22023", "length for type varchar must be at least 1", "", false},
	{"float(0)", "22023", "precision for type float must be at least 1 bit", "7", true},
	{"float(54)", "22023", "precision for type float must be less than 54 bits", "7", true},
	{"numeric(0)", "22023", "NUMERIC precision 0 must be between 1 and 1000", "", false},
	{"bit(0)", "22023", "length for type bit must be at least 1", "", false},
	{"bit(83886081)", "22023", "length for type bit cannot exceed 83886080", "", false},
	{"interval(-1)", "42601", "syntax error at or near \"-\"", "10", true},
	{"pg_catalog.timestamp(1,2)", "22023", "invalid type modifier", "", false},
	{"4294967296", "22003", "value \"4294967296\" is out of range for type oid", "", false},
	{"08", "22P02", "invalid input syntax for type oid: \"08\"", "", false},
	{" 23 ", "42601", "syntax error at or near \"23\"", "2", true},
	{"+23", "42601", "syntax error at or near \"+\"", "1", true},
	{"-1", "42601", "syntax error at or near \"-\"", "1", true},
	{"0x17", "42601", "syntax error at or near \"0x17\"", "1", true},
	{"definitely_other_regtype_db.regtype_a.same", "0A000", "cross-database references are not implemented: definitely_other_regtype_db.regtype_a.same", "", false},
	{"definitely_other_regtype_db.absent_regtype_schema.int4", "0A000", "cross-database references are not implemented: definitely_other_regtype_db.absent_regtype_schema.int4", "", false},
	{"x.y.z.w", "42601", "improper qualified name (too many dotted names): x.y.z.w", "", false},
	{"public.regtype_pct_subject.v%TYPE", "42601", "syntax error at or near \"%\"", "29", true},
	{"public.absent_pct_subject.v%TYPE", "42601", "syntax error at or near \"%\"", "28", true},
	{"public.regtype_pct_subject.absent%TYPE[]", "42601", "syntax error at or near \"%\"", "34", true},
	{"SETOF public.regtype_pct_subject.v%TYPE", "42601", "syntax error at or near \"%\"", "35", true},
	{"t.v%type", "42601", "syntax error at or near \"%\"", "4", true},
	{"unknown[]", "42704", "type \"unknown[]\" does not exist", "", false},
}

func TestReferenceRegtypeDiagnostics(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		regtypeSetup(t, p, conn, recorder)
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE public.regtype_pct_subject(v integer)", 0))
		const frameQuery = "SELECT $1::regtype AS r"
		const runtimeQuery = "SELECT $1::text::regtype AS r"
		frame, err := conn.PrepareContext(p.ctx, frameQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer frame.Close()
		runtime, err := conn.PrepareContext(p.ctx, runtimeQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		for _, fact := range regtypeInvalidInputs {
			t.Run(fact.input, func(t *testing.T) {
				before := len(*notices)
				literalQuery := "SELECT /* é🙂 */ " + regtypeLiteral(fact.input) + "::regtype AS r"
				for _, route := range []string{"literal-query", "literal-prepare", "direct-frame", "held-frame", "runtime-text", "held-runtime-text"} {
					t.Run(route, func(t *testing.T) {
						query := frameQuery
						var rows *sql.Rows
						var err error
						switch route {
						case "literal-query":
							query = literalQuery
							rows, err = conn.QueryContext(p.ctx, query)
						case "literal-prepare":
							query = literalQuery
							statement, e := conn.PrepareContext(p.ctx, query)
							err = e
							if statement != nil {
								statement.Close()
								t.Error("failed literal Prepare exposed statement")
							}
						case "direct-frame":
							rows, err = conn.QueryContext(p.ctx, query, fact.input)
						case "held-frame":
							rows, err = frame.QueryContext(p.ctx, fact.input)
						case "runtime-text":
							query = runtimeQuery
							rows, err = conn.QueryContext(p.ctx, query, fact.input)
						case "held-runtime-text":
							query = runtimeQuery
							rows, err = runtime.QueryContext(p.ctx, fact.input)
						}
						if rows != nil {
							rows.Close()
							t.Error("failed query exposed cursor")
						}
						want := regtypeInputDiagnostic(fact, route)
						referenceAssertDiagnostic(t, err, &want)
					})
				}
				if len(*notices) != before {
					t.Errorf("invalid regtype input emitted notices: %#v", (*notices)[before:])
				}
			})
		}
		// Same held frame still accepts valid and NULL after every failed input.
		for _, value := range []any{"integer", nil, []byte("pg_catalog.int4")} {
			s := referenceSelect(frameQuery, []string{"r"}, []string{"REGTYPE"}, [][]any{{"integer"}}, value)
			if value == nil {
				s.rows = [][]any{{nil}}
			}
			regtypeRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: frame, query: frameQuery}, recorder, s)
		}
	})
}

func TestReferenceRegtypeInvalidSQLAndDemand(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		regtypeSetup(t, p, conn, recorder)
		for _, q := range []string{"CREATE TABLE regtype_a.values_subject(id integer,r regtype)", "INSERT INTO regtype_a.values_subject VALUES(1,'integer')"} {
			count := int64(0)
			if q[0] == 'I' {
				count = 1
			}
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, count))
		}
		for _, fact := range []struct{ query, state, primary string }{
			{"SELECT TRUE::regtype AS r", "42846", "cannot cast type boolean to regtype"},
			{"SELECT '-1'::bigint::regtype AS r", "22003", "OID out of range"},
			{"INSERT INTO regtype_a.values_subject VALUES(9,'integer'::text)", "42804", ""},
			{"SELECT $1::regtype AS r FROM regtype_a.values_subject WHERE id=99", "42704", `type "missing_regtype_name" does not exist`},
			{"SELECT NULL::text || $1::regtype::text AS r", "42704", `type "missing_regtype_name" does not exist`},
			{"INSERT INTO regtype_a.values_subject VALUES(4,'integer'),(5,'missing_regtype_name')", "42704", `type "missing_regtype_name" does not exist`},
			{"UPDATE regtype_a.values_subject SET r='missing_regtype_name'", "42704", `type "missing_regtype_name" does not exist`},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fact.query, func(t *testing.T) {
					var args []any
					if fact.query == "SELECT $1::regtype AS r FROM regtype_a.values_subject WHERE id=99" || fact.query == "SELECT NULL::text || $1::regtype::text AS r" {
						args = []any{"missing_regtype_name"}
					}
					var result sql.Result
					var err error
					if prepared {
						stmt, e := conn.PrepareContext(p.ctx, fact.query)
						if e != nil {
							if stmt != nil {
								stmt.Close()
								t.Error("failed Prepare returned handle")
							}
							regtypeAssertError(t, e, fact.state, fact.primary, fact.query, "invalid-SQL-prepared", "prepare")
							return
						}
						defer stmt.Close()
						result, err = stmt.ExecContext(p.ctx, args...)
					} else {
						result, err = conn.ExecContext(p.ctx, fact.query, args...)
					}
					if result != nil {
						t.Error("failed statement returned result")
					}
					route := "invalid-SQL-direct/demand"
					if prepared {
						route = "invalid-SQL-prepared/demand"
					}
					regtypeAssertError(t, err, fact.state, fact.primary, fact.query, route, "execute")
				})
			}
		}
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT id,r FROM regtype_a.values_subject ORDER BY id", []string{"id", "r"}, []string{"INT4", "REGTYPE"}, [][]any{{int64(1), "integer"}}))
	})
}

func TestReferenceRegtypeAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"regtype_reader"})
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		for _, q := range []string{"CREATE ROLE regtype_reader", "CREATE SCHEMA regtype_private", "CREATE DOMAIN regtype_private.secret AS integer", "CREATE SCHEMA regtype_reader AUTHORIZATION regtype_reader", "CREATE TABLE regtype_reader.regtype_denied(r regtype)", "SET search_path TO regtype_private,regtype_reader"} {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, 0))
		}
		var oid int64
		if err := conn.QueryRowContext(p.ctx, "SELECT 'regtype_private.secret'::regtype::bigint").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET ROLE regtype_reader", 0))
		var current string
		if err := conn.QueryRowContext(p.ctx, "SELECT current_database()").Scan(&current); err != nil {
			t.Fatal(err)
		}
		for _, input := range []string{"regtype_private.secret", regtypeIdentifier(current) + ".regtype_private.secret", "secret"} {
			state := "42501"
			primary := "permission denied for schema regtype_private"
			if input == "secret" {
				state = "42704"
				primary = `type "secret" does not exist`
			}
			for _, literal := range []bool{false, true} {
				query := "SELECT $1::regtype AS r"
				args := []any{input}
				if literal {
					query = "SELECT " + regtypeLiteral(input) + "::regtype AS r"
					args = nil
				}
				rows, err := conn.QueryContext(p.ctx, query, args...)
				if rows != nil {
					rows.Close()
					t.Error("unauthorized query returned cursor")
				}
				regtypeAssertError(t, err, state, primary, query, "authorization-direct", "execute")
				stmt, err := conn.PrepareContext(p.ctx, query)
				if err != nil {
					regtypeAssertError(t, err, state, primary, query, "authorization-prepared", "prepare")
					if stmt != nil {
						stmt.Close()
						t.Error("failed prepare returned handle")
					}
					continue
				}
				rows, err = stmt.QueryContext(p.ctx, args...)
				if rows != nil {
					rows.Close()
					t.Error("unauthorized query returned cursor")
				}
				stmt.Close()
				regtypeAssertError(t, err, state, primary, query, "authorization-prepared", "execute")
			}
		}
		s := regtypeSelect("SELECT $1::oid::regtype AS a", "regtype_private.secret")
		s.args = []any{oid}
		regtypeBoth(t, p, conn, recorder, s)
		const deniedQuery = "INSERT INTO regtype_reader.regtype_denied VALUES($1)"
		held, err := conn.PrepareContext(p.ctx, deniedQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, input := range []string{"missing_regtype_name", "integer"} {
			for _, prepared := range []bool{false, true} {
				var result sql.Result
				var err error
				if prepared {
					result, err = held.ExecContext(p.ctx, input)
				} else {
					result, err = conn.ExecContext(p.ctx, deniedQuery, input)
				}
				if result != nil {
					t.Error("denied write exposed result")
				}
				state := "42704"
				primary := `type "missing_regtype_name" does not exist`
				if input == "integer" {
					state = "42501"
					primary = "permission denied for table regtype_denied"
				}
				route := "input-before-authorization-direct"
				if prepared {
					route = "input-before-authorization-held"
				}
				regtypeAssertError(t, err, state, primary, deniedQuery, route, "execute")
			}
		}
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("RESET ROLE", 0))
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT count(*) AS n FROM regtype_reader.regtype_denied", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
