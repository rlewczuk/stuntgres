package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectAddressStoredBoundaries(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		objectAddressRun(t, p, b, c, fixture.Exec("CREATE TABLE addr_stable(a OID)", 0))
		objectAddressRun(t, p, b, c, fixture.Exec("INSERT INTO addr_stable VALUES(23)", 1))
		for _, tc := range []struct{ name, expr, typ string }{
			{"identify", "(pg_identify_object(1259,a,0)).identity", "TEXT"},
			{"as_address", "(pg_identify_object_as_address(1259,a,0)).type", "TEXT"},
			{"inverse", "(pg_get_object_address('table',ARRAY['public','addr_stable'],ARRAY[]::text[])).objsubid", "INT"},
			{"describe", "pg_describe_object(1259,a,0)", "TEXT"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, q := range []string{
					"CREATE INDEX addr_stable_" + tc.name + " ON addr_stable ((" + tc.expr + "))",
					"CREATE TABLE addr_generated_" + tc.name + " (a OID,g " + tc.typ + " GENERATED ALWAYS AS (" + tc.expr + ") STORED)",
				} {
					message := "functions in index expression must be marked IMMUTABLE"
					if len(q) >= 12 && q[:12] == "CREATE TABLE" {
						message = "generation expression is not immutable"
					}
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P17", Message: message})
						if prepared {
							s = fixture.Prepared(s)
						}
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			})
		}
		objectAddressRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_attribute WHERE attrelid='addr_stable'::regclass::oid AND attnum>0 AND NOT attisdropped", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		objectAddressRun(t, p, b, c, fixture.Select("SELECT a::bigint AS a FROM addr_stable", []string{"a"}, []string{"INT8"}, [][]any{{int64(23)}}))
		objectAddressRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname IN ('addr_generated_identify','addr_generated_as_address','addr_generated_inverse','addr_generated_describe','addr_stable_identify','addr_stable_as_address','addr_stable_inverse','addr_stable_describe')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceObjectAddressStrictEvaluation(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE SEQUENCE addr_argument_effect", "CREATE TABLE addr_null_input(a OID)", "INSERT INTO addr_null_input VALUES(NULL)"} {
			count := int64(0)
			if q[0] == 'I' {
				count = 1
			}
			objectAddressRun(t, p, b, c, fixture.Exec(q, count))
		}
		// A nonconstant NULL input prevents planner constant simplification.
		// STRICT skips the function, not evaluation of reached sibling arguments.
		for _, fn := range []string{"pg_identify_object", "pg_identify_object_as_address", "pg_describe_object"} {
			typ := "RECORD"
			if fn == "pg_describe_object" {
				typ = "TEXT"
			}
			objectAddressRun(t, p, b, c, fixture.Select("SELECT "+fn+"(a,nextval('addr_argument_effect')::oid,0) AS v FROM addr_null_input", []string{"v"}, []string{typ}, [][]any{{nil}}))
		}
		// Each shared helper ran direct and freshly prepared, so six evaluations.
		objectAddressRun(t, p, b, c, fixture.Select("SELECT last_value FROM addr_argument_effect", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(6)}}))
		q := "SELECT pg_get_object_address('table',ARRAY[nextval('addr_argument_effect')::text],a::text::text[]) AS v FROM addr_null_input"
		objectAddressRun(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"RECORD"}, [][]any{{nil}}))
		objectAddressRun(t, p, b, c, fixture.Select("SELECT last_value FROM addr_argument_effect", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(8)}}))
	})
}
