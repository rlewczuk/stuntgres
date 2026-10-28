package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsExpressionDependencies(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		expressionPopulation(s)
		for _, q := range []string{"CREATE STATISTICS expr_single ON (a+a) FROM expr_t", "CREATE STATISTICS expr_joint(ndistinct) ON (a+1),b FROM expr_t", "CREATE STATISTICS expr_constant ON (1+2) FROM expr_t"} {
			s.run(fixture.Exec(q, 0))
		}
		// The bag includes namespace NORMAL and whole-table AUTO only for
		// expression-only objects. Repeated references do not duplicate edges.
		q := "SELECT s.stxname,d.refclassid::bigint,d.refobjsubid,d.deptype,d.refobjid=CASE WHEN d.refclassid=1259 THEN s.stxrelid ELSE s.stxnamespace END AS reference_link FROM pg_statistic_ext s JOIN pg_depend d ON d.classid='pg_statistic_ext'::regclass AND d.objid=s.oid WHERE s.stxrelid='expr_t'::regclass ORDER BY s.stxname,d.refclassid,d.refobjsubid"
		s.run(fixture.Select(q, []string{"stxname", "refclassid", "refobjsubid", "deptype", "reference_link"}, []string{"NAME", "INT8", "INT4", "CHAR", "BOOL"}, [][]any{
			{"expr_constant", int64(1259), int64(0), "a", true}, {"expr_constant", int64(2615), int64(0), "n", true},
			{"expr_joint", int64(1259), int64(1), "a", true}, {"expr_joint", int64(1259), int64(2), "a", true}, {"expr_joint", int64(2615), int64(0), "n", true},
			{"expr_single", int64(1259), int64(0), "a", true}, {"expr_single", int64(1259), int64(1), "a", true}, {"expr_single", int64(2615), int64(0), "n", true},
		}))
		oid := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='expr_joint'")
		s.run(fixture.Exec("ANALYZE expr_t", 0))
		s.run(fixture.Exec("ALTER TABLE expr_t RENAME COLUMN a TO renamed", 0))
		var deparsed string
		if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT pg_get_expr(stxexprs,stxrelid) FROM pg_statistic_ext WHERE stxname='expr_joint'").Scan(&deparsed); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(deparsed, "renamed") || strings.Contains(deparsed, "a +") {
			t.Fatal("rename did not deparse bound attribute", deparsed)
		}
		if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='expr_joint'"); got != oid {
			t.Fatal("rename replaced statistics", oid, got)
		}
		t.Log("expression dependencies phase: after input rename")
		s.run(expressionSkew("expr_joint", 1, "{2,3}"))
		s.run(fixture.Exec("ALTER TABLE expr_t DROP COLUMN spare", 0))
		s.run(expressionDefinition("expr_joint", "2", "{d,e}"))
		t.Log("expression dependencies phase: after dropping unselected spare")
		s.run(expressionSkew("expr_joint", 1, "{2,3}"))
		s.run(fixture.Exec("ANALYZE expr_t", 0))
		t.Log("expression dependencies phase: refreshed sparse layout")
		s.run(expressionSkew("expr_joint", 1, "{2,3}"))
		s.run(fixture.Exec("ALTER TABLE expr_t DROP COLUMN renamed", 0))
		s.run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxrelid='expr_t'::regclass ORDER BY stxname", []string{"stxname"}, []string{"NAME"}, [][]any{{"expr_constant"}}))
		s.run(fixture.Exec("DROP TABLE expr_t", 0))
		s.run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname='expr_constant'", []string{"stxname"}, []string{"NAME"}, nil))
	})
}

func TestReferenceStatisticsExpressionRoutineDependencies(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{"CREATE TABLE expr_t(a INT)", "CREATE FUNCTION expr_fn(a INT) RETURNS INT LANGUAGE SQL RETURN a+1", "CREATE STATISTICS expr_function ON (expr_fn(a)) FROM expr_t"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Select("SELECT d.deptype,d.refobjid='expr_fn(integer)'::regprocedure AS routine_link FROM pg_depend d JOIN pg_statistic_ext s ON d.classid='pg_statistic_ext'::regclass AND d.objid=s.oid WHERE s.stxname='expr_function' AND d.refclassid='pg_proc'::regclass", []string{"deptype", "routine_link"}, []string{"CHAR", "BOOL"}, [][]any{{"n", true}}))
		want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function expr_fn(integer) because other objects depend on it")
		want.Detail = "statistics object expr_function depends on function expr_fn(integer)"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		s.run(fixture.ErrorDiagnostic("DROP FUNCTION expr_fn(integer) RESTRICT", want))
		s.run(expressionDefinition("expr_function", "", "{e}"))
		s.run(fixture.Exec("DROP FUNCTION expr_fn(integer) CASCADE", 0))
		s.run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname='expr_function'", []string{"stxname"}, []string{"NAME"}, nil))
	})
}
