package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
)

type resetDatabaseIndex struct {
	Scans, Read, Fetch int64
	Last               sql.NullTime
}

func (r resetDatabaseIndex) equal(other resetDatabaseIndex) bool {
	return r.Scans == other.Scans && r.Read == other.Read && r.Fetch == other.Fetch && resetDatabaseSameTime(r.Last, other.Last)
}

func resetDatabaseIndexRead(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) resetDatabaseIndex {
	t.Helper()
	var r resetDatabaseIndex
	if err := c.QueryRowContext(p.Ctx, "SELECT idx_scan,idx_tup_read,idx_tup_fetch,last_idx_scan FROM pg_stat_all_indexes WHERE indexrelname=$1", name).Scan(&r.Scans, &r.Read, &r.Fetch, &r.Last); err != nil {
		t.Fatal(err)
	}
	return r
}
func resetDatabaseIndexAwait(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) resetDatabaseIndex {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	for {
		r := resetDatabaseIndexRead(t, p, c, name)
		if r.Scans > 0 && r.Read > 0 && r.Last.Valid {
			return r
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("genuine index publication", r, err)
		}
		runtime.Gosched()
	}
}
func TestReferenceStatsResetDatabaseRelations(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(statDatabasePreparedLabel(prepared), func(t *testing.T) {
			activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				statsCreate(t, p, b, c, "reset_relation_a", "reset_relation_b")
				resetDatabaseExec(t, p, c, "ALTER TABLE reset_relation_a ADD COLUMN k integer", "CREATE INDEX reset_relation_id ON reset_relation_a USING hash(id)", "CREATE INDEX reset_relation_k ON reset_relation_a USING hash(k)", "SET stats_fetch_consistency=none")
				w, retire := statsReportingConn(t, p, b)
				statsExec(t, p, b, w, "INSERT INTO reset_relation_a VALUES(1,11),(2,12)", 2)
				statsExec(t, p, b, w, "INSERT INTO reset_relation_b VALUES(1),(2),(3)", 3)
				for _, q := range []string{"SET enable_seqscan=off", "SET enable_bitmapscan=off", "SET enable_indexonlyscan=off"} {
					activityExec(t, p, w, q)
				}
				fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT k FROM reset_relation_a WHERE id=1", []string{"k"}, []string{"INT4"}, [][]any{{int64(11)}}))
				fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT id FROM reset_relation_a WHERE k=11", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				statsExec(t, p, b, w, "ANALYZE reset_relation_a", 0)
				statsExec(t, p, b, w, "VACUUM reset_relation_a", 0)
				retire()
				statsAwait(t, p, b, "reset_relation_a", "n_tup_ins", 2)
				statsAwait(t, p, b, "reset_relation_b", "n_tup_ins", 3)
				one, two := resetDatabaseIndexAwait(t, p, c, "reset_relation_id"), resetDatabaseIndexAwait(t, p, c, "reset_relation_k")
				beforeB, err := statsReportingRead(p.Ctx, c, "reset_relation_b")
				if err != nil {
					t.Fatal(err)
				}
				step := fixture.Select("SELECT pg_stat_reset_single_table_counters('reset_relation_a'::regclass::oid)", []string{"pg_stat_reset_single_table_counters"}, []string{"VOID"}, [][]any{{""}})
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				statsReportingWant(t, p, b, c, "reset_relation_a", statsReportingVector{})
				statsReportingWant(t, p, b, c, "reset_relation_b", beforeB)
				gotOne, gotTwo := resetDatabaseIndexRead(t, p, c, "reset_relation_id"), resetDatabaseIndexRead(t, p, c, "reset_relation_k")
				if !gotOne.equal(one) || !gotTwo.equal(two) {
					t.Fatal("heap reset fanned out to indexes", gotOne, one, gotTwo, two)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT seq_scan,seq_tup_read,vacuum_count,analyze_count,last_seq_scan,last_vacuum,last_analyze FROM pg_stat_all_tables WHERE relname='reset_relation_a'", []string{"seq_scan", "seq_tup_read", "vacuum_count", "analyze_count", "last_seq_scan", "last_vacuum", "last_analyze"}, []string{"INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ", "TIMESTAMPTZ", "TIMESTAMPTZ"}, [][]any{{int64(0), int64(0), int64(0), int64(0), nil, nil, nil}}))
				resetDatabaseValue(t, p, b, c, "SELECT pg_stat_reset_single_table_counters('reset_relation_id'::regclass::oid)", "pg_stat_reset_single_table_counters", "")
				gotOne, gotTwo = resetDatabaseIndexRead(t, p, c, "reset_relation_id"), resetDatabaseIndexRead(t, p, c, "reset_relation_k")
				if !gotOne.equal(resetDatabaseIndex{}) || !gotTwo.equal(two) {
					t.Fatal("index reset fanned out", gotOne, gotTwo, two)
				}
				statsReportingWant(t, p, b, c, "reset_relation_a", statsReportingVector{})
				statsReportingRows(t, p, b, c, "reset_relation_a", []int64{1, 2})
				statsReportingRows(t, p, b, c, "reset_relation_b", []int64{1, 2, 3})
				activityExec(t, p, c, "ANALYZE reset_relation_a")
				statsAwait(t, p, b, "reset_relation_a", "analyze_count", 1)
			})
		})
	}
}
