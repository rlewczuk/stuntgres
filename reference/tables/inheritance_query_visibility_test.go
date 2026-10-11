package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceQuerySnapshots(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		inheritanceQueryMapped(s)
		peer, err := s.b.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		for _, isolation := range []sql.IsolationLevel{sql.LevelReadCommitted, sql.LevelRepeatableRead} {
			tx, err := s.c.BeginTx(s.p.Ctx, &sql.TxOptions{Isolation: isolation})
			if err != nil {
				s.t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				run := func(step fixture.Step) { step.Prepared = s.prepared; fixture.RunStep(s.t, s.p.Ctx, tx, s.record, step) }
				run(fixture.Select("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				fixture.RunStep(s.t, s.p.Ctx, peer, s.b.Recorder, fixture.Exec("INSERT INTO c VALUES(false,3,'peer',33)", 1))
				want := [][]any{{int64(1)}, {int64(2)}}
				if isolation == sql.LevelReadCommitted {
					want = append(want, []any{int64(3)})
				}
				run(fixture.Select("SELECT a FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, want))
				run(fixture.Exec("INSERT INTO c VALUES(false,4,'own',44)", 1))
				run(fixture.Select("SELECT a FROM p WHERE a=4", []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}}))
				run(fixture.Select("WITH s AS (SELECT a FROM p) SELECT p.a FROM p JOIN s ON p.a=s.a WHERE p.a=4", []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}}))
				if err = tx.Rollback(); err != nil {
					s.t.Fatal(err)
				}
			}()
			s.values("SELECT a FROM p WHERE a=4", []string{"a"}, []string{"INT4"}, nil)
			s.values("SELECT a FROM p WHERE a=3", []string{"a"}, []string{"INT4"}, [][]any{{int64(3)}})
			fixture.RunStep(s.t, s.p.Ctx, peer, s.b.Recorder, fixture.Exec("DELETE FROM c WHERE a=3", 1))
		}
	})
}

func TestReferenceInheritanceQueryTemporaryVisibility(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("CREATE TEMP TABLE own_child() INHERITS(p)", 0)
		s.exec("INSERT INTO own_child VALUES(2)", 1)
		s.exec("CREATE TEMP TABLE own_grandchild() INHERITS(own_child)", 0)
		s.exec("INSERT INTO own_grandchild VALUES(3)", 1)
		s.values("SELECT * FROM p ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
		peer, err := s.b.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		step := fixture.Select("SELECT * FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
		step.Prepared = s.prepared
		fixture.RunStep(s.t, s.p.Ctx, peer, s.b.Recorder, step)
		s.values("SELECT * FROM ONLY p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
		s.exec("DROP TABLE own_child CASCADE", 0)
		s.values("SELECT * FROM p", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}})
	})
}
