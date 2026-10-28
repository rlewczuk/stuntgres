package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsNDistinctTransactions(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{"CREATE TABLE nd_t(a INT,b INT,c INT)", "CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t", "CREATE STATISTICS nd_z(ndistinct) ON a,c FROM nd_t"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(1,1,1),(2,2,2)", 2))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(3,3,3)", 1))
		backend := s.pair.Backends()[0]
		if s.pg {
			backend = s.pair.Backends()[1]
		}
		peer, err := backend.DB.Conn(s.pair.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer s.conn.ExecContext(context.Background(), "ROLLBACK")
		observe := func(n int64) {
			want := `{"1, 2": 2}`
			if n == 3 {
				want = `{"1, 2": 3}`
			}
			fixture.RunStep(t, s.pair.Ctx, peer, backend.Recorder, ndistinctRows(s.pg, []any{"nd_s", false, want}, []any{"nd_z", false, `{"1, 3": 2}`}))
		}
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("ANALYZE nd_t(a,b)", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 3}`}, []any{"nd_z", false, `{"1, 3": 2}`}))
		observe(2)
		s.run(fixture.Exec("SAVEPOINT nd_sp", 0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(4,4,4)", 1))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 4}`}, []any{"nd_z", false, `{"1, 3": 4}`}))
		s.run(fixture.Exec("ROLLBACK TO nd_sp", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 3}`}, []any{"nd_z", false, `{"1, 3": 2}`}))
		s.run(fixture.Exec("ROLLBACK", 0))
		observe(2)
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("ANALYZE nd_t(a,b)", 0))
		s.run(fixture.Exec("COMMIT", 0))
		observe(3)
		for _, completion := range []string{"ROLLBACK", "COMMIT"} {
			gid := "ndistinct_" + completion
			fixture.PreparedReferenceCleanup(t, backend, gid)
			s.run(fixture.Exec("BEGIN", 0))
			s.run(fixture.Exec("ANALYZE nd_t(a,c)", 0))
			s.run(fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
			fixture.RunStep(t, s.pair.Ctx, peer, backend.Recorder, fixture.Exec(completion+" PREPARED '"+gid+"'", 0))
			if completion == "ROLLBACK" {
				observe(3)
			}
		}
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 3}`}, []any{"nd_z", false, `{"1, 3": 3}`}))
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("CREATE TABLE nd_private(a INT,b INT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS nd_private_s(ndistinct) ON a,b FROM nd_private", 0))
		s.run(fixture.Exec("INSERT INTO nd_private VALUES(9,9)", 1))
		s.run(fixture.Exec("ANALYZE nd_private", 0))
		s.run(fixture.Exec("ROLLBACK", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 3}`}, []any{"nd_z", false, `{"1, 3": 3}`}))
	})
}
