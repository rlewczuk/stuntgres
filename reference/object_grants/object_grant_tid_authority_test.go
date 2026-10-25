package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Released REL_18_0 pg_operator.dat/pg_proc.dat declare six INTERNAL functions,
// all with full IN (27,27), result BOOL16, strict/immutable/parallel-safe.
// These ACL, pruning and equality-derived expectations are independent hypotheses
// until the root observes this WHOLE definition on pinned PostgreSQL18.0 first.
// No physical ctid locator, input/output helper or SQL-body edge is implied.
func TestReferenceObjectGrantTIDAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, op             string
		oid, operator        int64
		less, equal, greater bool
	}{
		{"tideq", "=", 1292, 387, false, true, false},
		{"tidne", "<>", 1265, 402, true, false, true},
		{"tidlt", "<", 2791, 2799, true, false, false},
		{"tidle", "<=", 2793, 2801, true, true, false},
		{"tidge", ">=", 2792, 2802, false, true, true},
		{"tidgt", ">", 2790, 2800, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, []string{"og_tid_authority"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				target := "pg_catalog." + tc.name + "(tid,tid)"
				run(fixture.Select("SELECT oid::regprocedure::TEXT AS identity,proname,pronamespace::BIGINT AS namespace,pronargs,prorettype::BIGINT AS result,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid=$1",
					[]string{"identity", "proname", "namespace", "pronargs", "result", "proisstrict", "provolatile", "proparallel"},
					[]string{"TEXT", "NAME", "INT8", "INT2", "INT8", "BOOL", "CHAR", "CHAR"},
					[][]any{{tc.name + "(tid,tid)", tc.name, int64(11), int64(2), int64(16), true, "i", "s"}}, tc.oid))
				// Full PostgreSQL metadata complements shared identity/ACL checks.
				full := "SELECT p.proargtypes::TEXT AS inputs,p.prokind,p.prolang::BIGINT AS language,p.proretset,p.pronargdefaults,p.provariadic::BIGINT AS variadic,p.proargnames IS NULL AS unnamed FROM pg_proc p WHERE p.oid=$1"
				metadata := fixture.Select(full,
					[]string{"inputs", "prokind", "language", "proretset", "pronargdefaults", "variadic", "unnamed"},
					[]string{"TEXT", "CHAR", "INT8", "BOOL", "INT2", "INT8", "BOOL"},
					[][]any{{"27 27", "f", int64(12), false, int64(0), int64(0), true}}, tc.oid)
				if b.PG {
					run(metadata)
				}
				if b.PG {
					run(fixture.Select("SELECT oprname,oprleft::BIGINT AS l,oprright::BIGINT AS r,oprresult::BIGINT AS result,oprcode::oid::BIGINT AS routine FROM pg_operator WHERE oid=$1",
						[]string{"oprname", "l", "r", "result", "routine"}, []string{"NAME", "INT8", "INT8", "INT8", "INT8"},
						[][]any{{tc.op, int64(27), int64(27), int64(16), tc.oid}}, tc.operator))
				}
				run(fixture.Exec("CREATE SCHEMA og_tid_authority AUTHORIZATION og_object_other", 0))
				fixture.RoleGrantActor(run, "og_object_other", func() {
					run(fixture.Exec("CREATE TABLE og_tid_authority.source(id INTEGER,a TID,b TID)", 0))
					run(fixture.Exec("INSERT INTO og_tid_authority.source VALUES(1,'(1,2)','(1,10)'),(2,'(1,2)','(1,2)'),(3,'(2147483648,0)','(2147483647,65535)'),(4,NULL,'(1,2)'),(5,'(1,2)',NULL),(6,NULL,NULL)", 6))
					run(fixture.Exec("CREATE TABLE og_tid_authority.keys(k TID)", 0))
					run(fixture.Exec("INSERT INTO og_tid_authority.keys VALUES('(1,2)'),('(1,2)'),('(1,10)'),(NULL),(NULL)", 5))
					run(fixture.Exec("CREATE VIEW og_tid_authority.saved AS SELECT a "+tc.op+" b AS v FROM og_tid_authority.source WHERE id=1", 0))
					run(fixture.Exec("GRANT SELECT ON og_tid_authority.source,og_tid_authority.keys,og_tid_authority.saved TO og_object_reader", 0))
				})
				run(fixture.Exec("GRANT USAGE ON SCHEMA og_tid_authority TO og_object_reader", 0))
				value := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_tid_authority.source"+tail, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
				}
				literal := func(expr string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
				}
				call := "a " + tc.op + " b"
				cases := []objectGrantProviderCase{
					{value(call, " WHERE id=1", tc.less), true, true},
					{value("a OPERATOR(pg_catalog."+tc.op+") b", " WHERE id=1", tc.less), true, true},
					{value(call, " WHERE id=2", tc.equal), true, true},
					{value(call, " WHERE id=3", tc.greater), true, true},
					{value(call, " WHERE id=4", nil), true, true},
					{value(call, " WHERE id=5", nil), true, true},
					{value(call, " WHERE id=6", nil), true, true},
					{fixture.Select("SELECT v FROM og_tid_authority.saved", []string{"v"}, []string{"BOOL"}, [][]any{{tc.less}}), true, true},
					// Row-dependent CASE avoids a constant-only warmed-plan claim.
					{value("(CASE WHEN id=1 THEN $1::TID ELSE a END) "+tc.op+" b", " WHERE id=1", tc.less, "(1,2)"), true, true},
					{value("a "+tc.op+" (CASE WHEN id=1 THEN $1::TID ELSE b END)", " WHERE id=1", tc.less, "(1,10)"), true, true},
					{literal("'(1,2)'::TID "+tc.op+" '(1,10)'::TID", tc.less), true, false},
					{literal("NULL::TID "+tc.op+" '(1,10)'::TID", nil), false, false},
					{literal("'(1,2)'::TID "+tc.op+" NULL::TID", nil), false, false},
					{value("$1::TID "+tc.op+" b", " WHERE id=1", nil, nil), false, false},
					{value("a "+tc.op+" $1::TID", " WHERE id=1", nil, nil), false, false},
					{value("CASE WHEN FALSE THEN "+call+" ELSE TRUE END", " WHERE id=1", true), false, true},
				}
				if tc.op == "<>" {
					cases = append(cases, objectGrantProviderCase{value("a != b", " WHERE id=1", true), true, true})
					cases = append(cases, objectGrantProviderCase{value("a NOT IN (b,NULL::TID)", " WHERE id=1", nil), true, true})
				}
				for _, tail := range []string{" WHERE FALSE", " WHERE id>99", " LIMIT 0"} {
					cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT "+call+" AS v FROM og_tid_authority.source"+tail, []string{"v"}, []string{"BOOL"}, nil), true, true})
				}
				if tc.name == "tideq" {
					// Equality-derived nodes consume tideq, not tidne or scalar
					// ordering identities. Runtime NULL rows do not erase admission.
					cases = append(cases,
						objectGrantProviderCase{value("a IS DISTINCT FROM b", " WHERE id=1", true), true, true},
						objectGrantProviderCase{value("a IS NOT DISTINCT FROM b", " WHERE id=2", true), true, true},
						objectGrantProviderCase{value("a IS DISTINCT FROM b", " WHERE id=4", true), true, true},
						objectGrantProviderCase{value("a IS NOT DISTINCT FROM b", " WHERE id=6", true), true, true},
						objectGrantProviderCase{value("a IN (b,NULL::TID)", " WHERE id=1", nil), true, true},
						objectGrantProviderCase{value("a IN (b,NULL::TID)", " WHERE id=2", true), true, true},
						objectGrantProviderCase{value("a IN (b,NULL::TID)", " WHERE id=4", nil), true, true},
						objectGrantProviderCase{fixture.Select("SELECT NULLIF(a,b) AS v FROM og_tid_authority.source WHERE id=1", []string{"v"}, []string{"TID"}, [][]any{{"(1,2)"}}), true, true},
						objectGrantProviderCase{fixture.Select("SELECT NULLIF(a,b) AS v FROM og_tid_authority.source WHERE id=2", []string{"v"}, []string{"TID"}, [][]any{{nil}}), true, true},
						objectGrantProviderCase{fixture.Select("SELECT NULLIF(a,b) AS v FROM og_tid_authority.source WHERE id=4", []string{"v"}, []string{"TID"}, [][]any{{nil}}), true, true},
						objectGrantProviderCase{value("CASE a WHEN b THEN TRUE ELSE FALSE END", " WHERE id=1", false), true, true},
						objectGrantProviderCase{value("CASE a WHEN b THEN TRUE ELSE FALSE END", " WHERE id=2", true), true, true},
						objectGrantProviderCase{value("CASE a WHEN b THEN TRUE ELSE FALSE END", " WHERE id=6", false), true, true},
						objectGrantProviderCase{fixture.Select("SELECT DISTINCT k FROM og_tid_authority.keys ORDER BY k", []string{"k"}, []string{"TID"}, [][]any{{"(1,2)"}, {"(1,10)"}, {nil}}), true, true},
						objectGrantProviderCase{fixture.Select("SELECT k,count(*) AS n FROM og_tid_authority.keys GROUP BY k ORDER BY k", []string{"k", "n"}, []string{"TID", "INT8"}, [][]any{{"(1,2)", int64(2)}, {"(1,10)", int64(1)}, {nil, int64(2)}}), true, true},
					)
				}
				controlOp, controlWant := "=", false
				if tc.name == "tideq" {
					controlOp, controlWant = "<>", true
				}
				controls := []fixture.Step{
					value("a "+controlOp+" b", " WHERE id=1", controlWant),
					// Btree sort support is not a scalar EXECUTE grant on any
					// of the six operators, including tideq's grouping authority.
					fixture.Select("SELECT k FROM og_tid_authority.keys ORDER BY k", []string{"k"}, []string{"TID"}, [][]any{{"(1,2)"}, {"(1,2)"}, {"(1,10)"}, {nil}, {nil}}),
					fixture.Select("SELECT k FROM og_tid_authority.keys ORDER BY k DESC NULLS FIRST", []string{"k"}, []string{"TID"}, [][]any{{nil}, {nil}, {"(1,10)"}, {"(1,2)"}, {"(1,2)"}}),
				}
				if tc.name == "tideq" {
					controls = append(controls, value("a NOT IN (b,NULL::TID)", " WHERE id=1", nil))
				}
				// Shared lifecycle asserts full diagnostic fields/execute stage
				// and the SAME held statements through PUBLIC→deny→grant→deny.
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, target, "function", tc.name, cases, controls)
			})
		})
	}

	t.Run("type_usage_new_dependency_not_existing_datum", func(t *testing.T) {
		objectGrantScenario(t, []string{"og_tid_usage"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			defer func() {
				for _, query := range []string{"RESET SESSION AUTHORIZATION", "GRANT USAGE ON TYPE pg_catalog.tid TO PUBLIC", "REVOKE USAGE ON TYPE pg_catalog.tid FROM og_object_reader"} {
					if _, err := c.ExecContext(p.Ctx, query); err != nil {
						t.Error("TID type ACL cleanup", query, err)
					}
				}
			}()
			run(fixture.Exec("CREATE SCHEMA og_tid_usage", 0))
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA og_tid_usage TO og_object_reader", 0))
			run(fixture.Exec("CREATE TABLE og_tid_usage.existing(v TID)", 0))
			run(fixture.Exec("INSERT INTO og_tid_usage.existing VALUES('(1,2)'),(NULL)", 2))
			run(fixture.Exec("GRANT SELECT ON og_tid_usage.existing TO og_object_reader", 0))
			fullDiagnostic := func(query string) fixture.Step {
				s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for type tid"})
				s.ErrorStage = "execute"
				return s
			}
			read := func() {
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					run(fixture.Select("SELECT v FROM og_tid_usage.existing ORDER BY v", []string{"v"}, []string{"TID"}, [][]any{{"(1,2)"}, {nil}}))
					run(fixture.Select("SELECT $1::TID AS v", []string{"v"}, []string{"TID"}, [][]any{{"(1,2)"}}, "(1,2)"))
					run(fixture.Select("SELECT '(1,2)'::TID::TEXT AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"(1,2)"}}))
				})
			}
			run(fixture.Exec("REVOKE USAGE ON TYPE pg_catalog.tid FROM PUBLIC", 0))
			read()
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fullDiagnostic("CREATE TABLE og_tid_usage.denied(v TID)"))
			})
			run(fixture.Exec("GRANT USAGE ON TYPE pg_catalog.tid TO og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Exec("CREATE TABLE og_tid_usage.allowed(v TID)", 0))
				run(fixture.Exec("INSERT INTO og_tid_usage.allowed VALUES('(2,3)')", 1))
			})
			run(fixture.Exec("REVOKE USAGE ON TYPE pg_catalog.tid FROM og_object_reader", 0))
			read()
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fullDiagnostic("CREATE TABLE og_tid_usage.denied_again(v TID)"))
				run(fixture.Select("SELECT v FROM og_tid_usage.allowed", []string{"v"}, []string{"TID"}, [][]any{{"(2,3)"}}))
			})
		})
	})
}
