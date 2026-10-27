package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalPoliciesRoutes(t *testing.T) {
	transactionalPolicyMatrix(t, []string{"autocommit", "sql", "go"}, func(s *transactionalPolicies, mode string) {
		queries := []string{
			"CREATE POLICY p ON t FOR SELECT TO tp_reader USING(id<=2)",
			"ALTER POLICY p ON t USING(id=2)",
			"ALTER POLICY p ON t RENAME TO renamed",
			"DROP POLICY renamed ON t",
		}
		// All handles really precede execution, including the not-yet-existing
		// policy. Preparing must not create, resolve or authorize the utility.
		var held []*sql.Stmt
		if s.held {
			for _, q := range queries {
				h, err := s.c.PrepareContext(s.p.Ctx, q)
				if err != nil {
					s.t.Fatal(q, err)
				}
				defer h.Close()
				held = append(held, h)
			}
		}
		s.ids()
		for _, commit := range []bool{false, true} {
			var finish func(bool)
			if mode != "autocommit" {
				finish = s.begin(mode)
			}
			var oid int64
			for i, q := range queries {
				if s.held {
					h := held[i]
					if tx, ok := s.x.(*sql.Tx); ok {
						h = tx.StmtContext(s.p.Ctx, h)
						defer h.Close()
					}
					fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.x, Statement: h, SQL: q}, s.b.Recorder, fixture.Exec(q, 0))
				} else {
					s.exec(q)
				}
				switch i {
				case 0:
					oid = s.oid("t", "p")
					s.facts("t", "p", "r", true, "tp_reader", "(id <= 2)", "")
					s.ids(1, 2)
				case 1:
					s.sameOID("t", "p", oid)
					s.facts("t", "p", "r", true, "tp_reader", "(id = 2)", "")
					s.ids(2)
				case 2:
					s.sameOID("t", "renamed", oid)
					s.facts("t", "renamed", "r", true, "tp_reader", "(id = 2)", "")
					s.ids(2)
				case 3:
					s.ids()
					s.run(pgPolicyAbsent("t", "renamed"))
				}
				if mode == "autocommit" {
					fixture.RunStep(s.t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("SET ROLE tp_reader", 0))
					want := [][]any{}
					if i == 0 {
						want = [][]any{{int64(1)}, {int64(2)}}
					} else if i < 3 {
						want = [][]any{{int64(2)}}
					}
					fixture.RunStep(s.t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, want))
					fixture.RunStep(s.t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Exec("RESET ROLE", 0))
				}
			}
			if finish != nil {
				finish(commit)
			}
			s.ids()
			// A separate create witnesses publication/rollback of a surviving
			// definition; C/A/N/D in one block alone could hide early publication.
			if mode != "autocommit" {
				finish = s.begin(mode)
				s.ddl(queries[0])
				private := s.oid("t", "p")
				s.facts("t", "p", "r", true, "tp_reader", "(id <= 2)", "")
				s.ids(1, 2)
				finish(commit)
				if commit {
					s.sameOID("t", "p", private)
					s.facts("t", "p", "r", true, "tp_reader", "(id <= 2)", "")
					s.ids(1, 2)
					s.ddl("DROP POLICY p ON t")
				} else {
					s.ids()
					s.run(pgPolicyAbsent("t", "p"))
				}
			}
		}
		s.run(fixture.Select("SELECT id,allowed FROM t ORDER BY id", []string{"id", "allowed"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}))
		s.run(fixture.Select("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}
