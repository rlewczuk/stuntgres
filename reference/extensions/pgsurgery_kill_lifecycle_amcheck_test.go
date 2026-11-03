package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const ksLifeCheckSQL = "SELECT ks_part.bt_index_check('ks_part.kill_idx'::regclass,true) AS checked"

func ksLifeCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	freezeBoth(t, p, b, c, fixture.Select(ksLifeCheckSQL, []string{"checked"}, []string{"VOID"}, [][]any{{""}}))
}

func ksLifeLogicalCorruption(index string) fixture.Diagnostic {
	// Existing index.AmcheckCapture.corrupt constructor: XX002, no optional
	// fields. This logical invariant is intentionally not the PG HOT diagnostic.
	return fixture.ControlsReferenceDiagnostic("XX002", `index entry references killed heap tuple for index "`+index+`"`)
}

func ksLifeTIDParts(t *testing.T, tid string) (uint32, uint16) {
	t.Helper()
	var block uint32
	var offset uint16
	if n, err := fmt.Sscanf(tid, "(%d,%d)", &block, &offset); n != 2 || err != nil || offset == 0 {
		t.Fatal("real TID", tid, err)
	}
	return block, offset
}

func ksLifeDeadNotice(t *testing.T, notices []fixture.Diagnostic, tid string) {
	t.Helper()
	block, offset := ksLifeTIDParts(t, tid)
	want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`skipping tid (%d, %d) for relation "heap" because it is marked dead`, block, offset)}
	if len(notices) != 1 || notices[0] != want {
		t.Fatalf("dead-slot notice %#v, want %#v", notices, want)
	}
}

func TestReferencePgsurgeryKillAmcheck(t *testing.T) {
	ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		freezeRun(t, p, b, c, fixture.Exec("CREATE EXTENSION amcheck SCHEMA ks_part", 0))
		defer c.ExecContext(p.Ctx, "DROP EXTENSION amcheck CASCADE")
		freezeRun(t, p, b, c, fixture.Exec("CREATE INDEX kill_idx ON ks_part.heap(k)", 0))
		horizon := ksLifeHorizon(t, p, b)
		defer horizon.ExecContext(p.Ctx, "ROLLBACK")
		a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'root') RETURNING k,v,ctid,xmin,xmax")
		freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(2,'survivor')", 1))
		ksLifeCheck(t, p, b, c)
		if !b.PG {
			ksLifeApply(t, p, b, c, a.tid)
			ksLifeRows(t, p, b, c, []any{int64(2), "survivor"})
			step := fixture.ImplicitError(ksLifeCheckSQL, ksLifeLogicalCorruption("kill_idx"))
			step.SelectRows = true
			freezeBoth(t, p, b, c, step)
			return
		}
		// Real PG-only physical witness; pageinspect proves HEAP_ONLY_TUPLE
		// and HOT root linkage, not merely same-page coordinates. No physical
		// HOT engine or fake corruption is added to the native scenario.
		freezeRun(t, p, b, c, fixture.Exec("CREATE EXTENSION pageinspect SCHEMA ks_part", 0))
		defer c.ExecContext(p.Ctx, "DROP EXTENSION pageinspect CASCADE")
		// A killed HOT root can independently sever index reachability. This
		// payload witness deliberately scans the heap; amcheck below examines
		// the genuine retained index through its own physical scan.
		freezeRun(t, p, b, c, fixture.Exec("SET enable_indexscan=off", 0))
		defer c.ExecContext(p.Ctx, "RESET enable_indexscan")
		freezeRun(t, p, b, c, fixture.Exec("SET enable_bitmapscan=off", 0))
		defer c.ExecContext(p.Ctx, "RESET enable_bitmapscan")
		next := freezeOne(t, p, c, "UPDATE ks_part.heap SET v='hot_successor' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
		rootBlock, rootOffset := ksLifeTIDParts(t, a.tid)
		nextBlock, nextOffset := ksLifeTIDParts(t, next.tid)
		if a.tid == next.tid || rootBlock != nextBlock {
			t.Fatal("small-heap HOT fixture lacks distinct same-page versions", a, next)
		}
		freezeRun(t, p, b, c, fixture.Select("SELECT (t_infomask2 & 32768)<>0 AS heap_only FROM ks_part.heap_page_items(ks_part.get_raw_page('ks_part.heap',$1::bigint)) WHERE lp=$2::integer", []string{"heap_only"}, []string{"BOOL"}, [][]any{{true}}, int64(nextBlock), int64(nextOffset)))
		freezeRun(t, p, b, c, fixture.Select("SELECT (t_infomask2 & 16384)<>0 AS hot_updated,t_ctid FROM ks_part.heap_page_items(ks_part.get_raw_page('ks_part.heap',$1::bigint)) WHERE lp=$2::integer", []string{"hot_updated", "t_ctid"}, []string{"BOOL", "TID"}, [][]any{{true, next.tid}}, int64(rootBlock), int64(rootOffset)))
		ksLifeCheck(t, p, b, c)
		ksLifeApply(t, p, b, c, a.tid)
		ksLifeRows(t, p, b, c, []any{int64(1), "hot_successor"}, []any{int64(2), "survivor"})
		// REL_18_6 heapam_handler.c's unresolved live heap-only parent. All
		// optional portable diagnostic fields are source-predicted empty;
		// root must discover the pinned runtime's complete record independently.
		want := fixture.ControlsReferenceDiagnostic("XX001", fmt.Sprintf(`failed to find parent tuple for heap-only tuple at (%d,%d) in table "heap"`, nextBlock, nextOffset))
		step := fixture.ImplicitError(ksLifeCheckSQL, want)
		step.SelectRows = true
		freezeBoth(t, p, b, c, step)
	})
	t.Run("postgres_extra_non_hot_entry_is_not_the_hot_witness", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			if !b.PG {
				return
			} // Native logical corruption is asserted above.
			freezeRun(t, p, b, c, fixture.Exec("CREATE EXTENSION amcheck SCHEMA ks_part", 0))
			defer c.ExecContext(p.Ctx, "DROP EXTENSION amcheck CASCADE")
			freezeRun(t, p, b, c, fixture.Exec("CREATE INDEX kill_idx ON ks_part.heap(k)", 0))
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'extra') RETURNING k,v,ctid,xmin,xmax")
			ksLifeCheck(t, p, b, c)
			ksLifeApply(t, p, b, c, a.tid)
			ksLifeRows(t, p, b, c)
			// Source predicts success: heapallindexed checks live heap members,
			// not every obsolete extra index member. Root oracle resolves it.
			ksLifeCheck(t, p, b, c)
		})
	})
}
