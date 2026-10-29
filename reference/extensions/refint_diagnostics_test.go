package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRefintArguments(t *testing.T) {
	refintCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		for _, tc := range []struct { name, function, clause, args, query, code, message string }{
			{"primary_zero", "check_primary_key", "AFTER INSERT FOR EACH ROW", "", "INSERT INTO arg_local VALUES(1,2)", "XX000", "check_primary_key: odd number of arguments should be specified"},
			{"primary_even", "check_primary_key", "AFTER INSERT FOR EACH ROW", "'a','remote'", "INSERT INTO arg_local VALUES(1,2)", "XX000", "check_primary_key: odd number of arguments should be specified"},
			{"primary_local", "check_primary_key", "AFTER INSERT FOR EACH ROW", "'missing','arg_remote','a'", "INSERT INTO arg_local VALUES(1,2)", "42703", `there is no attribute "missing" in relation "arg_local"`},
			{"primary_system", "check_primary_key", "AFTER INSERT FOR EACH ROW", "'ctid','arg_remote','a'", "INSERT INTO arg_local VALUES(1,2)", "42703", `there is no attribute "ctid" in relation "arg_local"`},
			{"primary_before_delete", "check_primary_key", "BEFORE DELETE FOR EACH ROW", "", "DELETE FROM arg_local", "XX000", "check_primary_key: must be fired by AFTER trigger"},
			{"primary_delete", "check_primary_key", "AFTER DELETE FOR EACH ROW", "", "DELETE FROM arg_local", "XX000", "check_primary_key: cannot process DELETE events"},
			{"primary_statement", "check_primary_key", "AFTER INSERT FOR EACH STATEMENT", "", "INSERT INTO arg_local VALUES(1,2)", "XX000", "check_primary_key: must be fired for row"},
			{"foreign_short", "check_foreign_key", "AFTER DELETE FOR EACH ROW", "'1','r','a','arg_remote'", "DELETE FROM arg_local", "XX000", "check_foreign_key: too short 4 (< 5) list of arguments"},
			{"foreign_zero", "check_foreign_key", "AFTER DELETE FOR EACH ROW", "'0','r','a','arg_remote','a'", "DELETE FROM arg_local", "XX000", "check_foreign_key: 0 (< 1) number of references specified"},
			{"foreign_negative", "check_foreign_key", "AFTER DELETE FOR EACH ROW", "'-1','r','a','arg_remote','a'", "DELETE FROM arg_local", "XX000", "check_foreign_key: -1 (< 1) number of references specified"},
			{"foreign_integer", "check_foreign_key", "AFTER DELETE FOR EACH ROW", "'x','r','a','arg_remote','a'", "DELETE FROM arg_local", "22P02", `invalid input syntax for type integer: "x"`},
			{"foreign_overflow", "check_foreign_key", "AFTER DELETE FOR EACH ROW", "'2147483648','r','a','arg_remote','a'", "DELETE FROM arg_local", "22003", `value "2147483648" is out of range for type integer`},
			{"foreign_action", "check_foreign_key", "AFTER DELETE FOR EACH ROW", "'1',' cascade','a','arg_remote','a'", "DELETE FROM arg_local", "XX000", "check_foreign_key: invalid action  cascade"},
			{"foreign_empty_action", "check_foreign_key", "AFTER DELETE FOR EACH ROW", "'1','','a','arg_remote','a'", "DELETE FROM arg_local", "XX000", "check_foreign_key: invalid action "},
			{"foreign_arity", "check_foreign_key", "AFTER DELETE FOR EACH ROW", "'2','r','a','arg_remote','a'", "DELETE FROM arg_local", "XX000", "check_foreign_key: invalid number of arguments 5 for 2 references"},
			{"foreign_local", "check_foreign_key", "AFTER DELETE FOR EACH ROW", "'1','r','missing','arg_remote','a'", "DELETE FROM arg_local", "42703", `there is no attribute "missing" in relation "arg_local"`},
			{"foreign_before_insert", "check_foreign_key", "BEFORE INSERT FOR EACH ROW", "", "INSERT INTO arg_local VALUES(1,2)", "XX000", "check_foreign_key: cannot process INSERT events"},
			{"foreign_before_delete", "check_foreign_key", "BEFORE DELETE FOR EACH ROW", "", "DELETE FROM arg_local", "XX000", "check_foreign_key: must be fired by AFTER trigger"},
			{"foreign_statement", "check_foreign_key", "AFTER INSERT FOR EACH STATEMENT", "", "INSERT INTO arg_local VALUES(1,2)", "XX000", "check_foreign_key: must be fired for row"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE TABLE arg_local(a int,b int)", 0))
				defer c.ExecContext(p.Ctx, "DROP TABLE arg_local CASCADE")
				run(fixture.Exec("INSERT INTO arg_local VALUES(7,8)", 1))
				// Runtime validation: malformed literals do not reject CREATE.
				parts := tc.clause
				// Timing/event precede ON, orientation follows it.
				orientation := " FOR EACH ROW"
				if parts == "AFTER INSERT FOR EACH STATEMENT" { parts, orientation = "AFTER INSERT", " FOR EACH STATEMENT" } else { parts = parts[:len(parts)-len(orientation)] }
				run(fixture.Exec("CREATE TRIGGER arg_guard "+parts+" ON arg_local"+orientation+" EXECUTE FUNCTION "+tc.function+"("+tc.args+")", 0))
				run(fixture.Exec("UPDATE arg_local SET a=a WHERE a=999", 0))
				run(fixture.ErrorDiagnostic(tc.query, refintError(tc.code, tc.message)))
				run(fixture.Select("SELECT a,b FROM arg_local ORDER BY a", []string{"a","b"}, []string{"INT4","INT4"}, [][]any{{int64(7),int64(8)}}))
			})
		}
		refintNotices(t, notices)
	})
}

func TestReferenceRefintQuotingDiagnostics(t *testing.T) {
	refintCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec(`CREATE SCHEMA "Refint Q"`, 0))
		run(fixture.Exec(`CREATE TABLE "Refint Q"."Parent Q"("parent key" int)`, 0))
		run(fixture.Exec(`INSERT INTO "Refint Q"."Parent Q" VALUES(1)`, 1))
		run(fixture.Exec(`CREATE TABLE quote_child("child key" int)`, 0))
		for _, tc := range []struct { name, args, code, message, position, query string }{
			{"quoted", `'child key','"Refint Q"."Parent Q"','"parent key"'`, "", "", "", ""},
			{"local_quoted", `'"child key"','"Refint Q"."Parent Q"','"parent key"'`, "42703", `there is no attribute ""child key"" in relation "quote_child"`, "", ""},
			{"arity_one", `'quote_child'`, "42601", "syntax error at end of input", "33", "select 1 from quote_child where "},
			{"missing_relation", `'child key','refint_absent','id'`, "42P01", `relation "refint_absent" does not exist`, "15", "select 1 from refint_absent where id = $1 "},
			{"missing_column", `'child key','"Refint Q"."Parent Q"','absent'`, "42703", `column "absent" does not exist`, "43", `select 1 from "Refint Q"."Parent Q" where absent = $1 `},
			{"malformed_relation", `'child key','"Refint Q".','id'`, "42601", `syntax error at or near "="`, "36", `select 1 from "Refint Q". where id = $1 `},
			{"unquoted_relation", `'child key','Refint Q.Parent Q','id'`, "42601", `syntax error at or near "."`, "23", "select 1 from Refint Q.Parent Q where id = $1 "},
		} {
			t.Run(tc.name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TRIGGER quote_guard AFTER INSERT ON quote_child FOR EACH ROW EXECUTE FUNCTION check_primary_key("+tc.args+")", 0))
				defer c.ExecContext(p.Ctx, "DROP TRIGGER quote_guard ON quote_child")
				if tc.code != "" {
					d := refintError(tc.code, tc.message)
					d.InternalPosition, d.InternalQuery = tc.position, tc.query
					fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("INSERT INTO quote_child VALUES(1)", d))
					return
				}
				for _, prepared := range []bool{false,true} { s := fixture.Exec("INSERT INTO quote_child VALUES($1)", 1, 1); s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, r, s) }
			})
		}
		refintNotices(t, notices)
	})
}
