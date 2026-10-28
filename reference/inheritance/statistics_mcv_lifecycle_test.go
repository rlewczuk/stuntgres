package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsMCVTransactions(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		s.run(fixture.Exec("CREATE STATISTICS mcv_z(mcv) ON a,b FROM mcv_t", 0))
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		s.run(fixture.Exec("ALTER STATISTICS mcv_z SET STATISTICS 0", 0))
		s.run(fixture.Exec("INSERT INTO mcv_t VALUES(2,2)", 1))
		defer s.conn.ExecContext(context.Background(), "ROLLBACK")
		backend := s.pair.Backends()[0]
		if s.pg {
			backend = s.pair.Backends()[1]
		}
		peer, err := backend.DB.Conn(s.pair.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		mcvAssert(t, s, "mcv_s", mcvFive...)
		mcvReadOn(t, s, peer, "mcv_s", mcvFour...)
		mcvAssert(t, s, "mcv_z", mcvFour...)
		mcvReadOn(t, s, peer, "mcv_z", mcvFour...)
		s.run(fixture.Exec("SAVEPOINT mcv_save", 0))
		s.run(fixture.Exec("DELETE FROM mcv_t WHERE a=2", 2))
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		mcvAssert(t, s, "mcv_s", mcvExpected{"1,1", 2.0 / 3, 4.0 / 9}, mcvExpected{"NULL,NULL", 1.0 / 3, 1.0 / 9})
		s.run(fixture.Exec("ROLLBACK TO mcv_save", 0))
		mcvAssert(t, s, "mcv_s", mcvFive...)
		s.run(fixture.Exec("ROLLBACK", 0))
		mcvReadOn(t, s, peer, "mcv_s", mcvFour...)
		for _, completion := range []string{"ROLLBACK", "COMMIT"} {
			gid := "mcv_" + completion
			fixture.PreparedReferenceCleanup(t, backend, gid)
			s.run(fixture.Exec("BEGIN", 0))
			s.run(fixture.Exec("ANALYZE mcv_t", 0))
			s.run(fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
			mcvReadOn(t, s, peer, "mcv_s", mcvFour...)
			fixture.RunStep(t, s.pair.Ctx, peer, backend.Recorder, fixture.Exec(completion+" PREPARED '"+gid+"'", 0))
		}
		mcvReadOn(t, s, peer, "mcv_s", mcvFive...)
		mcvOrdered(s, true)
		mcvAssert(t, s, "mcv_z", mcvFour...)
		mcvReadOn(t, s, peer, "mcv_z", mcvFour...)
	})
}
func TestReferenceStatisticsMCVLifecycle(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{"CREATE TABLE mcv_p(a INT,hole INT,b INT)", "ALTER TABLE mcv_p DROP COLUMN hole", "CREATE TABLE mcv_c() INHERITS(mcv_p)", "CREATE STATISTICS mcv_ps(mcv) ON a,b FROM mcv_p", "CREATE STATISTICS mcv_pe(mcv) ON (a+0),(b+0) FROM mcv_p"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO mcv_p VALUES(1,1),(1,1)", 2))
		s.run(fixture.Exec("INSERT INTO mcv_c VALUES(2,2),(2,2),(2,2)", 3))
		s.run(fixture.Exec("ANALYZE mcv_p", 0))
		for _, name := range []string{"mcv_ps", "mcv_pe"} {
			_, local := readMCV(t, s, name, false)
			assertIntMCV(t, local, mcvExpected{"1,1", 1, 1})
			_, inherited := readMCV(t, s, name, true)
			assertIntMCV(t, inherited, mcvExpected{"2,2", .6, .36}, mcvExpected{"1,1", .4, .16})
		}
		id := statisticExtDataID(t, s.conn, "mcv_ps", "mcv_p")
		s.run(fixture.Exec("ALTER TABLE mcv_p RENAME COLUMN b TO renamed_b", 0))
		s.run(fixture.Exec("ALTER TABLE mcv_p RENAME TO mcv_renamed", 0))
		if statisticExtDataID(t, s.conn, "mcv_ps", "mcv_renamed") != id {
			t.Fatal("rename changed statistics identity")
		}
		_, old := readMCV(t, s, "mcv_ps", true)
		assertIntMCV(t, old, mcvExpected{"2,2", .6, .36}, mcvExpected{"1,1", .4, .16})
		for _, q := range []string{"CREATE TABLE mcv_part(a INT,b INT) PARTITION BY RANGE(a)", "CREATE TABLE mcv_leaf PARTITION OF mcv_part FOR VALUES FROM(0) TO(10)", "CREATE STATISTICS mcv_part_s(mcv) ON a,b FROM mcv_part"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO mcv_part VALUES(1,1),(1,1)", 2))
		s.run(fixture.Exec("ANALYZE mcv_part", 0))
		_, inherited := readMCV(t, s, "mcv_part_s", true)
		assertIntMCV(t, inherited, mcvExpected{"1,1", 1, 1})
		s.run(fixture.Select("SELECT d.stxdinherit FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='mcv_part_s'", []string{"stxdinherit"}, []string{"BOOL"}, [][]any{{true}}))
		s.run(fixture.Exec("DROP STATISTICS mcv_ps", 0))
		s.run(fixture.Exec("ALTER TABLE mcv_renamed DROP COLUMN renamed_b CASCADE", 0))
		s.run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname='mcv_pe'", []string{"stxname"}, []string{"NAME"}, nil))
	})
}
func TestReferenceStatisticsMCVCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					observer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer observer.Close()
					run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, observer, b.Recorder, step) }
					s := ndistinctScenario{run: run, pg: b.PG, conn: observer, pair: p}
					mcvSetup(t, s)
					run(fixture.Exec("INSERT INTO mcv_t VALUES(2,2)", 1))
					holder, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Close()
					defer holder.ExecContext(context.Background(), "ROLLBACK")
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("BEGIN", 0))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE mcv_t IN SHARE MODE", 0))
					worker, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer worker.Close()
					query := "ANALYZE mcv_t"
					var held *sql.Stmt
					if prepared {
						held, err = worker.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					waiterPID, holderPID := 0, 0
					var oid int64
					if b.PG {
						waiterPID = fixture.LockPID(t, p.Ctx, worker)
						holderPID = fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, "mcv_t")
					}
					fixture.BinaryConversionActiveCancelAfter(t, p, b, worker, query, "(*relationRequest).wait", func(ctx context.Context) {
						if b.PG {
							fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "ShareUpdateExclusiveLock")
						}
					}, func(ctx context.Context) fixture.RelationOutcome {
						var out fixture.RelationOutcome
						if prepared {
							out.Result, out.Err = held.ExecContext(ctx)
						} else {
							out.Result, out.Err = worker.ExecContext(ctx, query)
						}
						return out
					})
					mcvAssert(t, s, "mcv_s", mcvFour...)
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("ROLLBACK", 0))
					run(fixture.Exec("ANALYZE mcv_t", 0))
					mcvAssert(t, s, "mcv_s", mcvFive...)
				})
			}
		})
	}
}
