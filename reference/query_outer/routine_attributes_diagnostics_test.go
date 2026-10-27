package reference_test

import (
	"database/sql"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAttributesDiagnostics(t *testing.T) {
	const create = "CREATE FUNCTION ra_x_bad(integer) RETURNS integer LANGUAGE SQL AS 'INSERT INTO ra_x_log VALUES($1); SELECT 7'"
	// Semantic options are checked during utility execution, not protocol Parse.
	// These complete diagnostics are independently source-reasoned; root's pinned
	// PG-first discovery resolves any release-specific position/stage differences.
	for _, tc := range []struct{ name, option, code, message, at string }{
		{"cost_zero", "COST 0", "22023", "COST must be positive", ""},
		{"cost_signed_zero", "COST -0.0", "22023", "COST must be positive", ""},
		{"cost_negative", "COST -2", "22023", "COST must be positive", ""},
		{"rows_zero", "ROWS +0", "22023", "ROWS must be positive", ""},
		{"rows_negative", "ROWS -2", "22023", "ROWS must be positive", ""},
		{"scalar_rows", "ROWS 2", "22023", "ROWS is not applicable when function does not return a set", ""},
		{"parallel_value", "PARALLEL arbitrary", "42601", `parameter "parallel" must be SAFE, RESTRICTED, or UNSAFE`, ""},
		{"null_synonyms", "STRICT CALLED ON NULL INPUT", "42601", "conflicting or redundant options", "CALLED"},
		{"null_returns_synonyms", "RETURNS NULL ON NULL INPUT STRICT", "42601", "conflicting or redundant options", "STRICT"},
		{"duplicate_strict", "STRICT STRICT", "42601", "conflicting or redundant options", "last_strict"},
		{"volatility_synonyms", "IMMUTABLE STABLE", "42601", "conflicting or redundant options", "STABLE"},
		{"duplicate_volatile", "VOLATILE VOLATILE", "42601", "conflicting or redundant options", "last_volatile"},
		{"security_synonyms", "SECURITY DEFINER EXTERNAL SECURITY INVOKER", "42601", "conflicting or redundant options", "EXTERNAL"},
		{"duplicate_parallel", "PARALLEL SAFE PARALLEL UNSAFE", "42601", "conflicting or redundant options", "last_parallel"},
		{"duplicate_cost", "COST 1 COST 2", "42601", "conflicting or redundant options", "last_cost"},
		{"duplicate_rows", "ROWS 1 ROWS 2", "42601", "conflicting or redundant options", "last_rows"},
		{"unknown_setting", "SET definitely_missing_setting='x'", "42704", `unrecognized configuration parameter "definitely_missing_setting"`, ""},
		{"invalid_setting", "SET work_mem='nonsense'", "22023", `invalid value for parameter "work_mem": "nonsense"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE ra_x_log(v integer)", "SET application_name='outside'")
				q := "/* café 雪 */ " + create + " " + tc.option
				position := 0
				if tc.at != "" {
					at := tc.at
					switch at {
					case "last_strict":
						at = "STRICT"
					case "last_volatile":
						at = "VOLATILE"
					case "last_parallel":
						at = "PARALLEL"
					case "last_cost":
						at = "COST"
					case "last_rows":
						at = "ROWS"
					}
					position = proceduresOptionPosition(t, q, at)
					if len(tc.at) > 5 && tc.at[:5] == "last_" {
						// All repeated introducers here are ASCII; offset the character
						// position to the second occurrence without using byte positions.
						first := proceduresOptionPosition(t, tc.option, at)
						tail := tc.option[first-1+len(at):]
						position += len(at) + proceduresOptionPosition(t, tail, at) - 1
					}
				}
				want := proceduresDiagnostic(tc.code, tc.message, position)
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("semantic option failed Prepare", err)
				}
				defer stmt.Close()
				proceduresCount(t, p, b, c, "pg_proc WHERE proname='ra_x_bad'", 0)
				proceduresCount(t, p, b, c, "ra_x_log", 0)
				resultsError(t, p, c, q, false, "execute", want)
				proceduresCount(t, p, b, c, "pg_proc WHERE proname='ra_x_bad'", 0)
				proceduresCount(t, p, b, c, "ra_x_log", 0)
				resultsBothMetadata(t, p, b, c, "SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"outside"}}, []resultsNativeColumn{{Length: 1<<63 - 1, LengthKnown: true, NullableKnown: true}})
				resultsExec(t, p, b, c, create+" STRICT")
				proceduresBoth(t, p, b, c, "SELECT ra_x_bad(1) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				proceduresCount(t, p, b, c, "ra_x_log", 2)
			})
		})
	}
	for _, option := range []string{"STRICT", "RETURNS NULL ON NULL INPUT", "CALLED ON NULL INPUT", "IMMUTABLE", "STABLE", "VOLATILE", "PARALLEL SAFE", "COST -1", "ROWS 2"} {
		t.Run("procedure_"+option, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "/* café 雪 */ CREATE PROCEDURE ra_x_proc() LANGUAGE SQL " + option + " AS 'SELECT 1'"
				want := proceduresDiagnostic("42P13", "invalid attribute in procedure definition", utf8.RuneCountInString("/* café 雪 */ CREATE PROCEDURE ra_x_proc() LANGUAGE SQL ")+1)
				resultsError(t, p, c, q, false, "execute", want)
				proceduresCount(t, p, b, c, "pg_proc WHERE proname='ra_x_proc'", 0)
				resultsExec(t, p, b, c, "CREATE PROCEDURE ra_x_proc() LANGUAGE SQL SECURITY INVOKER SET application_name='inside' AS 'SELECT 1'")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("CALL ra_x_proc()", 0)))
			})
		})
	}
	for _, tc := range []struct{ name, suffix, at string }{
		{"bare_cost", "COST", ""}, {"bare_parallel", "PARALLEL", ""}, {"bare_security", "SECURITY", ""}, {"bare_set", "SET application_name", ""}, {"incomplete_null", "CALLED ON NULL", ""}, {"junk", "PARALLEL SAFE nonsense", "nonsense"},
	} {
		t.Run("syntax_"+tc.name, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE ra_x_log(v integer)")
				q := "/* café 雪 */ CREATE FUNCTION ra_x_prefix() RETURNS integer LANGUAGE SQL AS 'SELECT 8'; INSERT INTO ra_x_log VALUES(9); " + create + " " + tc.suffix
				message, position := "syntax error at end of input", utf8.RuneCountInString(q)+1
				if tc.at != "" {
					message = `syntax error at or near "` + tc.at + `"`
					position = proceduresOptionPosition(t, q, tc.at)
				}
				resultsError(t, p, c, q, false, "prepare", proceduresDiagnostic("42601", message, position))
				proceduresCount(t, p, b, c, "ra_x_log", 0)
				proceduresCount(t, p, b, c, "pg_proc WHERE proname IN ('ra_x_prefix','ra_x_bad')", 0)
				resultsExec(t, p, b, c, create)
				proceduresBoth(t, p, b, c, "SELECT ra_x_bad(1) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
			})
		})
	}
	t.Run("alter_identity_authority_and_atomicity", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ra_x_nonowner"})
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "CREATE ROLE ra_x_nonowner", "CREATE FUNCTION ra_x_live(integer) RETURNS integer LANGUAGE SQL STRICT STABLE PARALLEL SAFE COST 7 AS 'SELECT $1+1'", "CREATE FUNCTION ra_x_over(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", "CREATE FUNCTION ra_x_over(text) RETURNS text LANGUAGE SQL AS 'SELECT $1'", "CREATE PROCEDURE ra_x_kind(integer) LANGUAGE SQL AS 'SELECT $1'")
			resultsExec(t, p, b, c, "GRANT USAGE ON SCHEMA public TO ra_x_nonowner")
			for _, tc := range []struct{ q, code, message string }{
				{"ALTER FUNCTION ra_x_absent(integer) STRICT", "42883", "function ra_x_absent(integer) does not exist"},
				{"ALTER FUNCTION ra_x_over STRICT", "42725", "function name \"ra_x_over\" is not unique"},
				{"ALTER FUNCTION ra_x_kind(integer) STRICT", "42809", "ra_x_kind(integer) is not a function"},
			} {
				want := proceduresDiagnostic(tc.code, tc.message, 0)
				if tc.code == "42725" {
					want.Hint = "Specify the argument list to select the function unambiguously."
				}
				resultsError(t, p, c, tc.q, false, "execute", want)
			}
			resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_x_nonowner")
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			resultsError(t, p, c, "ALTER FUNCTION ra_x_live(integer) CALLED ON NULL INPUT COST -1", false, "execute", proceduresDiagnostic("42501", "must be owner of function ra_x_live", 0))
			resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
			attributesDeclarationCatalogBoth(t, p, b, c, "SELECT provolatile,proisstrict,proparallel,procost FROM pg_proc WHERE oid='ra_x_live(integer)'::regprocedure", []string{"provolatile", "proisstrict", "proparallel", "procost"}, []string{"CHAR", "BOOL", "CHAR", "FLOAT4"}, [][]any{{"s", true, "s", float64(7)}})
			proceduresBoth(t, p, b, c, "SELECT ra_x_live(NULL::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{nil}})
			proceduresBoth(t, p, b, c, "SELECT ra_x_live(4) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}})
			resultsExec(t, p, b, c, "ALTER FUNCTION ra_x_live(integer) VOLATILE CALLED ON NULL INPUT")
			proceduresBoth(t, p, b, c, "SELECT ra_x_live(4) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}})
		})
	})
}

func TestReferenceRoutineAttributesSecuritySyntax(t *testing.T) {
	// Independently expected from the released grammar: security modes are
	// unquoted keywords, and malformed modes fail Parse before utility admission.
	// Keep each matrix leaf in one scenario so oracle-only subtest selection can
	// exercise exactly the same body without filtering or duplicating backends.
	for _, kind := range []string{"FUNCTION", "PROCEDURE"} {
		for _, operation := range []string{"CREATE", "ALTER"} {
			for _, external := range []bool{false, true} {
				for _, mode := range []struct{ name, sql string }{
					{"bogus", "bogus"},
					{"quoted_definer", `"definer"`},
					{"quoted_invoker", `"invoker"`},
				} {
					clause := "SECURITY "
					if external {
						clause = "EXTERNAL " + clause
					}
					name := operation + "_" + kind + "_" + clause + mode.name
					t.Run(name, func(t *testing.T) {
						fixture.RoleReferenceCleanup(t, []string{"ra_syntax_nonowner"})
						proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							resultsExec(t, p, b, c,
								"CREATE ROLE ra_syntax_nonowner",
								"GRANT USAGE ON SCHEMA public TO ra_syntax_nonowner",
								"CREATE TABLE ra_syntax_log(v integer)",
								"CREATE FUNCTION ra_syntax_live() RETURNS integer LANGUAGE SQL SECURITY DEFINER SET application_name='inside' AS 'SELECT 42'",
								"CREATE PROCEDURE ra_syntax_proc() LANGUAGE SQL SECURITY DEFINER SET application_name='inside' AS 'INSERT INTO ra_syntax_log VALUES(42)'",
								"SET application_name='outside'")
							target := "ra_syntax_live()"
							body := " RETURNS integer LANGUAGE SQL AS 'SELECT 99'"
							if kind == "PROCEDURE" {
								target = "ra_syntax_proc()"
								body = " LANGUAGE SQL AS 'INSERT INTO ra_syntax_log VALUES(99)'"
							}
							q := operation + " " + kind + " " + target
							if operation == "CREATE" {
								target = "ra_syntax_new()"
								q = "CREATE " + kind + " " + target + body
							}
							q += " SET application_name='changed' " + clause + mode.sql
							assertSyntax := func(q string) {
								t.Helper()
								want := proceduresDiagnostic("42601", `syntax error at or near "`+mode.sql+`"`, proceduresOptionPosition(t, q, mode.sql))
								resultsError(t, p, c, q, false, "prepare", want)
							}
							assertSyntax("/* café 雪 */ " + q)
							// A malformed suffix rejects the entire input: neither the
							// preceding CREATE nor the preceding INSERT may execute.
							assertSyntax("/* café 雪 */ CREATE FUNCTION ra_syntax_prefix() RETURNS integer LANGUAGE SQL AS 'SELECT 8'; INSERT INTO ra_syntax_log VALUES(9); " + q)
							proceduresCount(t, p, b, c, "ra_syntax_log", 0)
							proceduresCount(t, p, b, c, "pg_proc WHERE proname IN ('ra_syntax_prefix','ra_syntax_new')", 0)
							if operation == "ALTER" {
								assertSyntax("/* café 雪 */ ALTER " + kind + " ra_syntax_absent() " + clause + mode.sql)
								proceduresCount(t, p, b, c, "pg_proc WHERE proname='ra_syntax_absent'", 0)
							}
							// Neither ownership nor schema CREATE permission may mask
							// the syntax error. The valid ALTER control proves that this
							// actor genuinely lacks ownership of the target routine.
							resultsExec(t, p, b, c, "SET SESSION AUTHORIZATION ra_syntax_nonowner")
							defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
							assertSyntax("/* café 雪 */ " + q)
							ownerTarget := "ra_syntax_live()"
							ownerName, ownerKind := "ra_syntax_live", "function"
							if kind == "PROCEDURE" {
								ownerTarget, ownerName, ownerKind = "ra_syntax_proc()", "ra_syntax_proc", "procedure"
							}
							resultsError(t, p, c, "ALTER "+kind+" "+ownerTarget+" "+clause+"INVOKER", false, "execute", proceduresDiagnostic("42501", "must be owner of "+ownerKind+" "+ownerName, 0))
							resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
							attributesDeclarationCatalogBoth(t, p, b, c,
								"SELECT proname::text AS name,prosecdef,proconfig FROM pg_proc WHERE proname IN ('ra_syntax_live','ra_syntax_proc') ORDER BY proname",
								[]string{"name", "prosecdef", "proconfig"}, []string{"TEXT", "BOOL", "_TEXT"},
								[][]any{{"ra_syntax_live", true, "{application_name=inside}"}, {"ra_syntax_proc", true, "{application_name=inside}"}})
							proceduresBoth(t, p, b, c, "SELECT ra_syntax_live() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CALL ra_syntax_proc()", 0))
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("CALL ra_syntax_proc()", 0)))
							resultsStoredBoth(t, p, b, c, "SELECT v FROM ra_syntax_log ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}, {int64(42)}})
							attributesSettingBoth(t, p, b, c, "SELECT current_setting('application_name') AS v", "outside")
						})
					})
				}
			}
		}
	}
	t.Run("positive_controls", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// DEFINER and INVOKER remain ordinary unquoted identifiers outside
			// the security-mode slot, including routine/argument/column names.
			resultsExec(t, p, b, c,
				"CREATE TABLE ra_syntax_identifiers(definer integer, invoker integer)",
				"CREATE FUNCTION definer(invoker integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
				"CREATE FUNCTION invoker(definer integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ra_syntax_identifiers VALUES(7,9)", 1))
			proceduresBoth(t, p, b, c, "SELECT definer(invoker) AS definer,invoker(definer) AS invoker FROM ra_syntax_identifiers", []string{"definer", "invoker"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(7)}})
			for _, kind := range []string{"FUNCTION", "PROCEDURE"} {
				target, body := "ra_syntax_valid_f()", " RETURNS integer LANGUAGE SQL AS 'SELECT 42'"
				if kind == "PROCEDURE" {
					target, body = "ra_syntax_valid_p()", " LANGUAGE SQL AS 'SELECT 42'"
				}
				for _, clause := range []string{"SECURITY ", "EXTERNAL SECURITY "} {
					for _, mode := range []struct {
						sql     string
						definer bool
					}{{"DEFINER", true}, {"INVOKER", false}} {
						for _, prepared := range []bool{false, true} {
							for _, q := range []string{
								"CREATE " + kind + " " + target + body + " " + clause + mode.sql,
								"ALTER " + kind + " " + target + " " + clause + "INVOKER",
								"ALTER " + kind + " " + target + " " + clause + mode.sql,
							} {
								step := fixture.Exec(q, 0)
								if prepared {
									step = fixture.Prepared(step)
								}
								fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
								want := mode.definer
								if q == "ALTER "+kind+" "+target+" "+clause+"INVOKER" {
									want = false
								}
								attributesDeclarationCatalogBoth(t, p, b, c, "SELECT prosecdef FROM pg_proc WHERE oid='"+target+"'::regprocedure", []string{"prosecdef"}, []string{"BOOL"}, [][]any{{want}})
							}
							if kind == "FUNCTION" {
								proceduresBoth(t, p, b, c, "SELECT ra_syntax_valid_f() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
							} else {
								fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("CALL "+target, 0)))
							}
							resultsExec(t, p, b, c, "DROP "+kind+" "+target)
						}
					}
				}
			}
		})
	})
}
