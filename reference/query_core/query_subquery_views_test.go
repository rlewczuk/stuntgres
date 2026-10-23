package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQuerySubqueryViews(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		subqueryScopeFixture(run)
		run(fixture.Exec("CREATE VIEW nested_view AS SELECT d.id,(SELECT i.v FROM inner_t i WHERE i.k=d.id AND i.v=10) AS scalar,EXISTS(SELECT 1 FROM inner_t i WHERE i.k=d.id) AS present FROM (SELECT id FROM outer_t) d", 0))
		check := func() {
			subqueryBoth(run, fixture.Select("SELECT * FROM nested_view ORDER BY id", []string{"id", "scalar", "present"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(10), true}, {int64(2), nil, true}, {int64(3), nil, false}, {int64(3), nil, false}}))
		}
		check()
		run(fixture.Exec("ALTER TABLE inner_t RENAME TO renamed_inner", 0))
		run(fixture.Exec("ALTER TABLE renamed_inner RENAME COLUMN v TO renamed_value", 0))
		check()
		run(fixture.Exec("ALTER TABLE renamed_inner ADD COLUMN unused INT", 0))
		run(fixture.Exec("ALTER TABLE renamed_inner DROP COLUMN unused", 0))
		check()
		want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column renamed_value of table renamed_inner because other objects depend on it")
		want.Detail = "view nested_view depends on column renamed_value of table renamed_inner"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("ALTER TABLE renamed_inner DROP COLUMN renamed_value", want))
		check()
		// OFFSET retains target demand (21000); eligible EXISTS without OFFSET
		// ignores the same graph but must still capture/deparse/remap its deps.
		run(fixture.Exec("CREATE TABLE ignored_source(v INT)", 0))
		run(fixture.Exec("INSERT INTO ignored_source VALUES(1),(2)", 2))
		run(fixture.Exec("CREATE VIEW ignored_view AS SELECT EXISTS(SELECT (SELECT v FROM ignored_source) FROM outer_t ORDER BY id LIMIT 1 OFFSET 1) AS present", 0))
		run(fixture.Exec("CREATE VIEW eligible_view AS SELECT EXISTS(SELECT (SELECT v FROM ignored_source) FROM outer_t ORDER BY id LIMIT 1) AS present", 0))
		eligibleCheck := func() {
			subqueryBoth(run, fixture.Select("SELECT * FROM eligible_view", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
		}
		ignoredCheck := func() {
			q := "SELECT * FROM ignored_view"
			subqueryQueryFailure(t, p, b, c, q, subqueryCardinality())
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			subqueryQueryFailure(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, subqueryCardinality())
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		ignoredCheck()
		eligibleCheck()
		run(fixture.Exec("ALTER TABLE ignored_source RENAME TO ignored_renamed", 0))
		run(fixture.Exec("ALTER TABLE ignored_renamed RENAME COLUMN v TO renamed_v", 0))
		ignoredCheck()
		eligibleCheck()
		// Keep the established single-dependent DROP diagnostic exact.
		run(fixture.Exec("DROP VIEW eligible_view", 0))
		dependency := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop table ignored_renamed because other objects depend on it")
		dependency.Detail = "view ignored_view depends on table ignored_renamed"
		dependency.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("DROP TABLE ignored_renamed", dependency))
		ignoredCheck()
		run(fixture.Exec("CREATE VIEW eligible_view AS SELECT EXISTS(SELECT (SELECT renamed_v FROM ignored_renamed) FROM outer_t ORDER BY id LIMIT 1) AS present", 0))
		eligibleCheck()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
			ignoredCheck()
			eligibleCheck()
		}
	})
}

func TestReferenceQuerySubqueryDerivedDuplicateViewLabels(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE VIEW duplicate_view(a,b) AS SELECT d.* FROM (SELECT 1 AS x,2 AS x)d", 0))
		// A user label resembling the private bridge namespace must remain
		// independent of whichever synthetic label the renderer chooses.
		run(fixture.Exec("CREATE VIEW collision_view(a,b,c) AS SELECT d.* FROM (SELECT 1 AS x,2 AS x,3 AS __view_added_0_1)d", 0))
		for _, definition := range []struct{ name, sql string }{
			{"duplicate_view", " SELECT x AS a,\n    x_1 AS b\n   FROM ( SELECT 1 AS x,\n            2 AS x) d(x, x_1);"},
			{"collision_view", " SELECT x AS a,\n    x_1 AS b,\n    __view_added_0_1 AS c\n   FROM ( SELECT 1 AS x,\n            2 AS x,\n            3 AS __view_added_0_1) d(x, x_1, __view_added_0_1);"},
		} {
			subqueryBoth(run, fixture.Select("SELECT pg_get_viewdef($1::regclass)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{definition.sql}}, definition.name))
			subqueryBoth(run, fixture.Select("SELECT definition FROM pg_catalog.pg_views WHERE schemaname='public' AND viewname=$1", []string{"definition"}, []string{"TEXT"}, [][]any{{definition.sql}}, definition.name))
		}
		fixture.ViewCopy(t, p, b, "duplicate_view", "duplicate_copy")
		fixture.ViewCopy(t, p, b, "collision_view", "collision_copy")
		check := func() {
			subqueryBoth(run, fixture.Select("SELECT * FROM duplicate_view", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
			subqueryBoth(run, fixture.Select("SELECT * FROM collision_view", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3)}}))
			subqueryBoth(run, fixture.Select("SELECT * FROM duplicate_copy", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
			subqueryBoth(run, fixture.Select("SELECT * FROM collision_copy", []string{"a", "b", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3)}}))
		}
		check()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
		}
	})
}
