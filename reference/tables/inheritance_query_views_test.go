package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceQueryPreparedViews(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		fixture.SchemaReferenceCleanup(s.t, "query_moved")
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("CREATE VIEW default_view AS SELECT * FROM p", 0)
		s.exec("CREATE VIEW only_view AS SELECT * FROM ONLY p", 0)
		s.exec("CREATE VIEW only_alias AS SELECT r.a AS label FROM ONLY(p) r", 0)
		stmt, err := s.c.PrepareContext(s.p.Ctx, "SELECT a FROM p ORDER BY a")
		if err != nil {
			s.t.Fatal(err)
		}
		defer stmt.Close()
		check := func(values ...int64) {
			rows, err := stmt.QueryContext(s.p.Ctx)
			if err != nil {
				s.t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.ColumnTypes()
			if err != nil {
				s.t.Fatal(err)
			}
			fixture.TableDDLColumnTypes(s.t, s.b.PG, columns, []string{"a"}, []string{"INT4"}, []bool{true})
			fixture.TransactionRowValues(s.t, rows, values...)
		}
		check(1)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("INSERT INTO c VALUES(2)", 1)
		check(1, 2)
		s.exec("ALTER TABLE c RENAME TO renamed_child", 0)
		check(1, 2)
		// Stored views follow frozen identities across the named parent's move.
		s.exec("CREATE SCHEMA query_moved", 0)
		s.exec("ALTER TABLE p RENAME TO renamed_parent", 0)
		s.exec("ALTER TABLE renamed_parent SET SCHEMA query_moved", 0)
		for _, query := range []string{"SELECT * FROM default_view ORDER BY a", "SELECT a FROM query_moved.renamed_parent ORDER BY a"} {
			s.values(query, []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		}
		s.values("SELECT * FROM only_view", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
		s.values("SELECT * FROM only_alias", []string{"label"}, []string{"INT4"}, [][]any{{int64(1)}})
		s.exec("DROP TABLE renamed_child", 0)
		s.exec("CREATE TABLE renamed_child() INHERITS(query_moved.renamed_parent)", 0)
		s.exec("INSERT INTO renamed_child VALUES(3)", 1)
		s.values("SELECT * FROM default_view ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}})
		s.values("SELECT * FROM only_alias", []string{"label"}, []string{"INT4"}, [][]any{{int64(1)}})
		// Remove the scenario-specific schema, including dependent public views.
		s.exec("DROP SCHEMA query_moved CASCADE", 0)
	})
}
