package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func batchUtilityComposition(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = a.ExecContext(ctx, "ROLLBACK")
			if _, err := peer.ExecContext(ctx, "DROP SCHEMA IF EXISTS bs CASCADE"); err != nil {
				t.Error("composition schema cleanup", err)
			}
		})
		q := "CREATE SCHEMA bs; CREATE TABLE bs.u(i INT); INSERT INTO bs.u VALUES(1),(2); ALTER TABLE bs.u ADD COLUMN j INT DEFAULT 4; CREATE INDEX bi ON bs.u(i); CREATE VIEW bs.v AS SELECT i FROM bs.u; CREATE TABLE bs.c AS SELECT i FROM bs.v; SELECT i FROM bs.c ORDER BY i"
		w := batchDiag("42703", `column "missing" does not exist`)
		failure := q + "; SELECT missing"
		w.Position = batchPosition(failure, "missing")
		r := batchQuery(t, p, a, failure)
		fixture.BatchReadSet(t, r, fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, false})
		fixture.AssertDiagnostic(t, r.Err(), &w)
		r.Close()
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_tables WHERE schemaname='bs'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.BatchSets(t, batchQuery(t, p, a, q), fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, false})
		batchValues(t, p, b, peer, "SELECT i FROM bs.c ORDER BY i", 1, 2)
		batchExec(t, p, b, a, "CREATE TABLE dup(i INT)", 0)
		failure = "INSERT INTO dup VALUES(1),(1); CREATE UNIQUE INDEX dup_i ON dup(i)"
		want := batchDiag("23505", `could not create unique index "dup_i"`)
		want.Detail = "Key (i)=(1) is duplicated."
		want.Schema = "public"
		want.Table = "dup"
		want.Constraint = "dup_i"
		batchError(t, p, a, failure, want)
		batchValues(t, p, b, a, "SELECT i FROM dup")
		batchExec(t, p, b, a, "CREATE TABLE recreate(i INT); INSERT INTO recreate VALUES(7)", 1)
		failure = "DROP TABLE recreate; CREATE TABLE recreate(i INT); INSERT INTO recreate VALUES(8); SELECT missing"
		w.Position = batchPosition(failure, "missing")
		batchError(t, p, a, failure, w)
		batchValues(t, p, b, a, "SELECT i FROM recreate", 7)
		batchExec(t, p, b, a, "DROP TABLE recreate; CREATE TABLE recreate(i INT); INSERT INTO recreate VALUES(8)", 1)
		batchValues(t, p, b, peer, "SELECT i FROM recreate", 8)
	})
	// Keep unrelated native exclusions; table/schema utilities are transactional.
	for _, utility := range []string{"CREATE SCHEMA excluded", "CREATE INDEX excluded ON batch_t(i)", "COMMENT ON TABLE batch_t IS 'excluded'", "GRANT SELECT ON batch_t TO PUBLIC"} {
		t.Run("explicit/"+utility, func(t *testing.T) {
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				batchSeed(t, p, b, a)
				batchExec(t, p, b, a, "BEGIN", 0)
				q := "INSERT INTO batch_t VALUES(1); " + utility
				if b.PG || utility == "CREATE SCHEMA excluded" || utility == "CREATE INDEX excluded ON batch_t(i)" || utility == "COMMENT ON TABLE batch_t IS 'excluded'" {
					batchExec(t, p, b, a, q, 0)
					batchValues(t, p, b, a, "SELECT i FROM batch_t ORDER BY i", 1, 7)
					if utility == "CREATE INDEX excluded ON batch_t(i)" {
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND indexname='excluded'", []string{"indexname"}, []string{"NAME"}, [][]any{{"excluded"}}))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND indexname='excluded'", []string{"indexname"}, []string{"NAME"}, nil))
					}
					if utility == "COMMENT ON TABLE batch_t IS 'excluded'" {
						fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT obj_description('batch_t'::regclass,'pg_class') AS text", []string{"text"}, []string{"TEXT"}, [][]any{{"excluded"}}))
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT obj_description('batch_t'::regclass,'pg_class') AS text", []string{"text"}, []string{"TEXT"}, [][]any{{nil}}))
					}
				} else {
					batchError(t, p, a, q, batchDiag("0A000", "statement is unsupported in an explicit transaction"))
				}
				batchExec(t, p, b, a, "ROLLBACK", 0)
				batchValues(t, p, b, peer, "SELECT i FROM batch_t", 7)
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT indexname FROM pg_indexes WHERE schemaname='public' AND indexname='excluded'", []string{"indexname"}, []string{"NAME"}, nil))
				if utility == "COMMENT ON TABLE batch_t IS 'excluded'" {
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT obj_description('batch_t'::regclass,'pg_class') AS text", []string{"text"}, []string{"TEXT"}, [][]any{{nil}}))
				}
				batchExec(t, p, b, a, "BEGIN; ALTER TABLE batch_t ADD COLUMN j INT; CREATE SEQUENCE supported; ROLLBACK", 0)
				batchValues(t, p, b, a, "SELECT i FROM batch_t", 7)
			})
		})
	}
}
