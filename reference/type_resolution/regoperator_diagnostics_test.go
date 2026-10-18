package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegOperatorDiagnostics(t *testing.T) {
	regoperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ alias, input, code, message, hint string }{
			{"regoper", "+", "42725", "more than one operator named +", ""}, {"regoper", "pg_catalog.+", "42725", "more than one operator named pg_catalog.+", ""}, {"regoper", "-", "42725", "more than one operator named -", ""},
			{"regoper", "||//", "42883", "operator does not exist: ||//", ""}, {"regoperator", "++(int4,int4)", "42883", "operator does not exist: ++(int4,int4)", ""},
			{"regoper", "regop_missing.||/", "42883", "operator does not exist: regop_missing.||/", ""}, {"regoperator", "regop_missing.+(int,int)", "42883", "operator does not exist: regop_missing.+(int,int)", ""},
			{"regoperator", "+", "22P02", "expected a left parenthesis", ""}, {"regoperator", "+(integer,integer", "22P02", "expected a right parenthesis", ""}, {"regoperator", "+(integer,)", "22P02", "expected a type name", ""},
			{"regoperator", "-(integer)", "42P02", "missing argument", "Use NONE to denote the missing argument of a unary operator."},
			{"regoperator", "+()", "54023", "too many arguments", "Provide two argument types for operator."}, {"regoperator", "+(integer,integer,integer)", "54023", "too many arguments", "Provide two argument types for operator."},
			{"regoperator", "-(integer,NONE)", "42883", "operator does not exist: -(integer,NONE)", ""}, {"regoperator", "-(NONE,NONE)", "42883", "operator does not exist: -(NONE,NONE)", ""},
			{"regoper", "a..+", "42602", "invalid name syntax", ""}, {"regoper", "a.b.c.+", "42601", "improper qualified name (too many dotted names): a.b.c.+", ""}, {"regoper", "regop_foreign.pg_catalog.+", "0A000", "cross-database references are not implemented: regop_foreign.pg_catalog.+", ""},
			{"regoper", "", "42602", "invalid name syntax", ""}, {"regoper", `"s`, "42602", "invalid name syntax", ""},
			{"regoper", "4294967296", "22003", "value \"4294967296\" is out of range for type oid", ""}, {"regoperator", "4294967296", "22003", "value \"4294967296\" is out of range for type oid", ""},
			{"regoper", "-1", "42883", "operator does not exist: -1", ""}, {"regoper", " 551 ", "42883", "operator does not exist:  551 ", ""},
			{"regoperator", "+(integer[ ,integer)", "22P02", "improper type name", ""},
		} {
			t.Run(tc.alias+"/"+tc.input, func(t *testing.T) {
				fixture.RegOperatorDiagnosticRoutes(t, p, b, c, tc.alias, tc.input, tc.code, tc.message, tc.hint)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.RegOperatorError("SELECT 'é' AS prefix, '||//'::regoper", "42883", "operator does not exist: ||//", "", "23", ""))
		// These full operand records were independently observed on pinned PostgreSQL 18.0.
		for _, tc := range []struct{ input, code, message, context string }{
			{`+("NONE",integer)`, "42704", `type "NONE" does not exist`, ""},
			{"+(regop_missing.secret,integer)", "3F000", `schema "regop_missing" does not exist`, ""},
			{"+(no_such_regop_type,integer)", "42704", `type "no_such_regop_type" does not exist`, ""},
			{"+(numeric(-1),integer)", "22023", "NUMERIC precision -1 must be between 1 and 1000", ""},
			{"+(123,integer)", "42601", `syntax error at or near "123"`, `invalid type name "123"`},
		} {
			s := fixture.RegOperatorError("SELECT "+fixture.RegOperatorLiteral(tc.input)+"::regoperator", tc.code, tc.message, "", "8", tc.context)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, alias := range []string{"regoper", "regoperator"} {
			for _, tc := range []struct{ expression, code, message, position string }{
				{"true", "42846", "cannot cast type boolean to " + alias, "12"},
				{"1.2::numeric", "42846", "cannot cast type numeric to " + alias, "20"},
				{"(-1)::bigint", "22003", "OID out of range", ""},
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.RegOperatorError("SELECT "+tc.expression+"::"+alias, tc.code, tc.message, "", tc.position, ""))
			}
		}
	})
}
