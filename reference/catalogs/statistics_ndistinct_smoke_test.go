package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsNDistinctFeatureSmoke(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE nd_t(a INT,b INT)", 0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(1,1),(1,1),(2,2),(2,2)", 4))
		s.run(fixture.Exec("CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t", 0))
		s.run(fixture.Exec("CREATE STATISTICS nd_absent(ndistinct) ON a,b FROM nd_t", 0))
		s.run(fixture.Exec("ALTER STATISTICS nd_absent SET STATISTICS 0", 0))
		s.run(ndistinctRows(s.pg))
		s.run(fixture.Select("SELECT (SELECT stxdndistinct FROM pg_statistic_ext_data) AS v", []string{"v"}, []string{statisticsDatabaseTypeName(s.pg, "pg_ndistinct")}, [][]any{{nil}}))
		held, err := s.conn.PrepareContext(s.pair.Ctx, ndistinctQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 2}`}))
		s.run(fixture.Select("SELECT a,b FROM nd_t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}}))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(3,3)", 1))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 2}`}))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 3}`}))
		s.run(fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS 0", 0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(4,4)", 1))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 3}`}))
		s.run(fixture.Select("SELECT stxstattarget FROM pg_statistic_ext WHERE stxname='nd_s'", []string{"stxstattarget"}, []string{"INT2"}, [][]any{{int64(0)}}))
		if !s.pg {
			var saved bytes.Buffer
			if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(s.pair.Ctx, s.pair.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			s.run(ndistinctRows(false, []any{"nd_s", false, `{"1, 2": 3}`}))
			var again bytes.Buffer
			if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &again); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(saved.Bytes(), again.Bytes()) {
				t.Fatal("native semantic state changed on roundtrip", saved.String(), again.String())
			}
		}
		s.run(fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS DEFAULT", 0))
		s.run(fixture.Select("SELECT stxstattarget FROM pg_statistic_ext WHERE stxname='nd_s'", []string{"stxstattarget"}, []string{"INT2"}, [][]any{{nil}}))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 4}`}))
		backend := s.pair.Backends()[0]
		if s.pg {
			backend = s.pair.Backends()[1]
		}
		fixture.RunStep(t, s.pair.Ctx, fixture.HeldStatement{s.conn, held, ndistinctQuery}, backend.Recorder, ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 4}`}))
		for _, q := range []string{"CREATE TABLE nd_null(a INT,b INT)", "CREATE STATISTICS nd_null_s(ndistinct) ON a,b FROM nd_null", "CREATE TABLE nd_parent(a INT,b INT)", "CREATE TABLE nd_child() INHERITS(nd_parent)", "CREATE STATISTICS nd_parent_s(ndistinct) ON a,b FROM nd_parent", "CREATE STATISTICS nd_child_s(ndistinct) ON a,b FROM nd_child"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO nd_null VALUES(NULL,NULL),(NULL,NULL),(NULL,1),(NULL,1),(1,NULL),(1,1)", 6))
		s.run(fixture.Exec("ANALYZE nd_null", 0))
		s.run(fixture.Exec("INSERT INTO nd_parent VALUES(1,1),(1,1)", 2))
		s.run(fixture.Exec("INSERT INTO nd_child VALUES(2,2),(2,2),(2,2)", 3))
		s.run(fixture.Exec("ANALYZE ONLY nd_parent", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_null_s", false, `{"1, 2": 4}`}, []any{"nd_parent_s", false, `{"1, 2": 1}`}, []any{"nd_parent_s", true, `{"1, 2": 2}`}, []any{"nd_s", false, `{"1, 2": 4}`}))
	})
}
