package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDriverBatchRefreshComposition(t *testing.T) {
	t.Run("existing_repeated_population", func(t *testing.T) {
		batchReviewControlsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			batchRefreshSeed(t, p, b, f.A)
			batchExec(t, p, b, f.A, "CREATE INDEX batch_refresh_source_idx ON batch_refresh_base(i)", 0)
			var indexOID int64
			if err := f.A.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='batch_refresh_source_idx'").Scan(&indexOID); err != nil || indexOID == 0 {
				t.Fatal("source index identity", indexOID, err)
			}
			old := batchRefreshOld()
			four := append(batchRefreshOld(), []any{int64(3), "c"})
			five := append(append(batchRefreshOld(), []any{int64(3), "c"}), []any{int64(4), "d"})
			q := "SELECT i,s FROM batch_refresh_mv; INSERT INTO batch_refresh_base VALUES(3,'c'); REFRESH MATERIALIZED VIEW batch_refresh_mv WITH DATA; SELECT i,s FROM batch_refresh_mv; REFRESH MATERIALIZED VIEW batch_refresh_mv WITH NO DATA; SELECT relispopulated FROM pg_class WHERE relname='batch_refresh_mv'; INSERT INTO batch_refresh_base VALUES(4,'d'); REFRESH MATERIALIZED VIEW batch_refresh_mv; SELECT i,s FROM batch_refresh_mv"
			batchSets(t, batchQuery(t, p, f.A, q), batchRefreshSet(old...), batchRefreshSet(four...), batchSet{[]string{"relispopulated"}, []string{"BOOL"}, [][]any{{false}}, false}, batchRefreshSet(five...))
			batchRefreshRows(t, p, b, f.B, "batch_refresh_mv", five)
			batchRefreshRows(t, p, b, f.B, "batch_refresh_base", five)
			batchRefreshPopulation(t, p, b, f.B, "batch_refresh_mv", true)
			var after int64
			if err := f.B.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='batch_refresh_source_idx'").Scan(&after); err != nil || after != indexOID {
				t.Fatal("refresh replaced source index", after, indexOID, err)
			}
			fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.Select("SELECT i,s FROM batch_refresh_base WHERE i=4", []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "d"}}))
			batchExec(t, p, b, f.A, "REFRESH MATERIALIZED VIEW batch_refresh_mv WITH NO DATA; SELECT 1", 1)
			batchRefreshPopulation(t, p, b, f.B, "batch_refresh_mv", false)
			fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.MaterializedViewUnpopulated("batch_refresh_mv"))
			batchExec(t, p, b, f.A, "REFRESH MATERIALIZED VIEW batch_refresh_mv WITH DATA; REFRESH MATERIALIZED VIEW batch_refresh_mv", 0)
			batchRefreshRows(t, p, b, f.B, "batch_refresh_mv", five)
		})
	})
	t.Run("private_create_and_source_index", func(t *testing.T) {
		batchReviewControlsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			batchExec(t, p, b, f.A, "CREATE TABLE batch_refresh_base(i INT,s TEXT)", 0)
			q := "INSERT INTO batch_refresh_base VALUES(1,'a'),(1,'a'),(2,NULL); CREATE INDEX batch_refresh_source_idx ON batch_refresh_base(i); CREATE MATERIALIZED VIEW batch_refresh_mv AS SELECT i,s FROM batch_refresh_base WITH NO DATA; REFRESH MATERIALIZED VIEW batch_refresh_mv; SELECT i,s FROM batch_refresh_mv; INSERT INTO batch_refresh_base VALUES(3,'c'); REFRESH MATERIALIZED VIEW batch_refresh_mv WITH DATA; SELECT i,s FROM batch_refresh_mv; REFRESH MATERIALIZED VIEW batch_refresh_mv WITH NO DATA; REFRESH MATERIALIZED VIEW batch_refresh_mv; SELECT i,s FROM batch_refresh_mv"
			four := append(batchRefreshOld(), []any{int64(3), "c"})
			batchSets(t, batchQuery(t, p, f.A, q), batchRefreshSet(batchRefreshOld()...), batchRefreshSet(four...), batchRefreshSet(four...))
			batchRefreshRows(t, p, b, f.B, "batch_refresh_mv", four)
			batchRefreshRows(t, p, b, f.B, "batch_refresh_base", four)
			batchRefreshPopulation(t, p, b, f.B, "batch_refresh_mv", true)
			fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.Select("SELECT indexname FROM pg_indexes WHERE tablename='batch_refresh_base'", []string{"indexname"}, []string{"NAME"}, [][]any{{"batch_refresh_source_idx"}}))
			fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.Select("SELECT i,s FROM batch_refresh_base WHERE i=3", []string{"i", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}}))
		})
	})
}

func TestReferenceDriverBatchRefreshAtomicity(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "private"}[private], func(t *testing.T) {
			batchReviewControlsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
				batchRefreshSeed(t, p, b, f.A)
				batchExec(t, p, b, f.A, "CREATE INDEX batch_refresh_source_idx ON batch_refresh_base(i)", 0)
				name := "batch_refresh_mv"
				prefix := "SELECT i,s FROM batch_refresh_mv; "
				if private {
					name = "batch_refresh_private"
					prefix += "CREATE MATERIALIZED VIEW " + name + " AS SELECT i,s FROM batch_refresh_base WITH NO DATA; CREATE INDEX batch_refresh_rollback_idx ON batch_refresh_base(s); "
				}
				four := append(batchRefreshOld(), []any{int64(3), "c"})
				q := prefix + "INSERT INTO batch_refresh_base VALUES(3,'c'); REFRESH MATERIALIZED VIEW " + name + "; SELECT i,s FROM " + name + "; REFRESH MATERIALIZED VIEW " + name + " WITH NO DATA; SELECT relispopulated FROM pg_class WHERE relname='" + name + "'; REFRESH MATERIALIZED VIEW " + name + " WITH DATA; SELECT i,s FROM " + name + "; SELECT missing FROM batch_refresh_base"
				r := batchQuery(t, p, f.A, q)
				sets := []batchSet{batchRefreshSet(batchRefreshOld()...), batchRefreshSet(four...), {[]string{"relispopulated"}, []string{"BOOL"}, [][]any{{false}}, false}, batchRefreshSet(four...)}
				for i, set := range sets {
					if i != 0 && !r.NextResultSet() {
						t.Fatal("lost completed private refresh output", i, r.Err())
					}
					batchReadSet(t, r, set)
				}
				if r.NextResultSet() {
					t.Fatal("failed suffix returned another result")
				}
				w := batchDiag("42703", `column "missing" does not exist`)
				w.Position = batchPosition(q, "missing")
				fixture.AssertDiagnostic(t, r.Err(), &w)
				r.Close()
				batchRefreshRows(t, p, b, f.B, "batch_refresh_base", batchRefreshOld())
				batchRefreshRows(t, p, b, f.B, "batch_refresh_mv", batchRefreshOld())
				batchRefreshPopulation(t, p, b, f.B, "batch_refresh_mv", true)
				fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.Select("SELECT i,s FROM batch_refresh_base WHERE i=3", []string{"i", "s"}, []string{"INT4", "TEXT"}, nil))
				if private {
					fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='batch_refresh_private'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.Select("SELECT indexname FROM pg_indexes WHERE tablename='batch_refresh_base'", []string{"indexname"}, []string{"NAME"}, [][]any{{"batch_refresh_source_idx"}}))
			})
		})
	}
}
