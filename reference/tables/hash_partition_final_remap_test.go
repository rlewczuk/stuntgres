package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type referenceHashFinalTarget struct {
	control, pool *sql.DB
	conn          *sql.Conn
	record        *fixture.Recorder
}

func referenceHashNewFinalTarget(t *testing.T, p *fixture.Pair) referenceHashFinalTarget {
	t.Helper()
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("hash-final-remap")
	if err != nil {
		t.Fatal(err)
	}
	control := sql.OpenDB(connector)
	control.SetMaxIdleConns(0)
	t.Cleanup(func() { control.Close() })
	pool, record := fixture.InsertSelectRecordedPool(t, connector)
	pool.SetMaxIdleConns(0)
	conn, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return referenceHashFinalTarget{control: control, pool: pool, conn: conn, record: record}
}

func referenceHashFinalID(t *testing.T, p *fixture.Pair, db fixture.Executor, name string) uint32 {
	t.Helper()
	// QueryContext is common to the admitted DB/Conn executors.
	rows, err := db.QueryContext(p.Ctx, "SELECT '"+name+"'::REGCLASS::OID::BIGINT")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing relation identity", name, rows.Err())
	}
	var id int64
	if err := rows.Scan(&id); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("identity result shape", rows.Err())
	}
	return uint32(id)
}

func TestReferenceHashPartitionFinalRemap(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE hp_final_spacer(k INT)", 0))
		run(fixture.Exec("CREATE TABLE hp_final_ref(k INT)", 0))
		sourceID := referenceHashFinalID(t, p, b.DB, "hp_final_ref")
		// Plain OID literals retain their numeric bits. This admitted immutable
		// expression deterministically distinguishes replay identity from remint:
		// key0 hashes to residue0; key2 hashes to residue2 under builtin support2.
		expression := fmt.Sprintf("CASE WHEN k::OID = '%d'::OID THEN 0 ELSE 2 END", sourceID)
		run(fixture.Exec("CREATE TABLE hp_final(k REGCLASS,id INT) PARTITION BY HASH(("+expression+"))", 0))
		referenceHashChildren(run, "hp_final", 8)
		for r := range 8 {
			run(fixture.Exec(fmt.Sprintf("CREATE UNIQUE INDEX hp_final_idx_%d ON hp_final_%d(id)", r, r), 0))
		}
		run(fixture.Exec("INSERT INTO hp_final VALUES('hp_final_ref',7)", 1))
		run(fixture.Select("SELECT "+expression+" AS partition_key FROM hp_final", []string{"partition_key"}, []string{"INT4"}, [][]any{{int64(0)}}))
		for r := range 8 {
			var want [][]any
			if r == 0 {
				want = [][]any{{int64(7)}}
			}
			run(fixture.Select(fmt.Sprintf("SELECT id FROM hp_final_%d WHERE id=7", r), []string{"id"}, []string{"INT4"}, want))
			run(partitionConstraintIndexState(fmt.Sprintf("hp_final_idx_%d", r), true))
		}
		run(partitionConstraintDuplicate("INSERT INTO hp_final VALUES('hp_final_ref',7)", "hp_final_0", "hp_final_idx_0", "Key (id)=(7) already exists."))
		run(fixture.Select("SELECT k::TEXT,id FROM hp_final", []string{"k", "id"}, []string{"TEXT", "INT4"}, [][]any{{"hp_final_ref", int64(7)}}))
		run(fixture.Select("SELECT id FROM ONLY hp_final", []string{"id"}, []string{"INT4"}, nil))
		var dump bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Exec("INSERT INTO hp_final VALUES('hp_final_ref',8)", 1))
		run(partitionConstraintDuplicate("INSERT INTO hp_final VALUES('hp_final_ref',8)", "hp_final_0", "hp_final_idx_0", "Key (id)=(8) already exists."))
		run(fixture.Exec("INSERT INTO hp_final VALUES('0'::REGCLASS,9)", 1))
		run(fixture.Select("SELECT "+expression+" AS partition_key,id FROM hp_final_2 WHERE id=9", []string{"partition_key", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(9)}}))
		run(partitionConstraintDuplicate("INSERT INTO hp_final VALUES('0'::REGCLASS,9)", "hp_final_2", "hp_final_idx_2", "Key (id)=(9) already exists."))
		run(fixture.Select("SELECT id FROM hp_final_0 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
		if b.PG {
			return
		}

		var replayID, droppedID, finalID uint32
		var alignmentIDs [2]uint32
		var temporaryFacts [][3]int64
		facts := func() string {
			return fmt.Sprintf("sourceOID=%d replayOID=%d droppedOID=%d finalOID=%d alignmentSequenceOIDs=%v TEMP[relation,rowtype,namespace]=%v key outputs=0->2 builtin residues=0->2", sourceID, replayID, droppedID, finalID, alignmentIDs, temporaryFacts)
		}
		t.Cleanup(func() {
			if t.Failed() {
				t.Log(facts())
			}
		})
		// A no-TEMP control measures replay's actual OID, rather than treating
		// a source-versus-final identity difference as proof of final repair.
		baseline := referenceHashNewFinalTarget(t, p)
		if err := stuntgres.Restore(p.Ctx, baseline.control, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err, facts())
		}
		replayID = referenceHashFinalID(t, p, baseline.conn, "hp_final_ref")
		if replayID != sourceID {
			t.Fatal("source/replay identity precondition failed", facts())
		}
		fixture.RunStep(t, p.Ctx, baseline.conn, baseline.record, fixture.Select("SELECT "+expression+" AS partition_key FROM hp_final", []string{"partition_key"}, []string{"INT4"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, baseline.conn, baseline.record, fixture.Select("SELECT id FROM hp_final_0 WHERE id=7", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))

		dropped := referenceHashNewFinalTarget(t, p)
		retained := referenceHashNewFinalTarget(t, p)
		// A sequence owns exactly one relation identity and no rowtype. Align
		// both real target allocation histories before creating their TEMP tables.
		for i, target := range []referenceHashFinalTarget{dropped, retained} {
			fixture.RunStep(t, p.Ctx, target.conn, target.record, fixture.Exec("CREATE SEQUENCE hp_final_oid_pad", 0))
			alignmentIDs[i] = referenceHashFinalID(t, p, target.conn, "hp_final_oid_pad")
		}
		if alignmentIDs[0] != alignmentIDs[1] {
			t.Fatal("matched sequence preallocation identities differ", facts())
		}
		var previousHigh int64
		// Advance identical real TEMP histories only until their monotonically
		// allocated relation/rowtype IDs reach replayID. There is no residue hunt.
		for i := 0; ; i++ {
			name := fmt.Sprintf("hp_final_temp_%d", i)
			var observed [2][3]int64
			for n, target := range []referenceHashFinalTarget{dropped, retained} {
				fixture.RunStep(t, p.Ctx, target.conn, target.record, fixture.Exec("CREATE TEMP TABLE "+name+"(k INT)", 0))
				fixture.RunStep(t, p.Ctx, target.conn, target.record, fixture.Exec("INSERT INTO "+name+" VALUES(91)", 1))
				rows, err := target.conn.QueryContext(p.Ctx, "SELECT oid::BIGINT,reltype::BIGINT,relnamespace::BIGINT FROM pg_class WHERE relname='"+name+"'")
				if err != nil {
					t.Fatal(err, facts())
				}
				if !rows.Next() {
					rows.Close()
					t.Fatal("TEMP identity absent", name, facts())
				}
				if err := rows.Scan(&observed[n][0], &observed[n][1], &observed[n][2]); err != nil {
					rows.Close()
					t.Fatal(err, facts())
				}
				if rows.Next() || rows.Err() != nil {
					rows.Close()
					t.Fatal("TEMP identity shape", rows.Err(), facts())
				}
				rows.Close()
			}
			temporaryFacts = append(temporaryFacts, observed[0])
			if observed[0] != observed[1] {
				t.Fatal("matched allocation histories differ", observed, facts())
			}
			if uint32(observed[0][2]) == replayID {
				t.Fatal("referent collision is only TEMP namespace", facts())
			}
			if uint32(observed[0][0]) == replayID || uint32(observed[0][1]) == replayID {
				break
			}
			high := max(observed[0][0], observed[0][1])
			if high <= previousHigh || high > int64(replayID) {
				t.Fatal("TEMP allocation crossed replay identity without exact collision", facts())
			}
			previousHigh = high
		}
		for i := range len(temporaryFacts) {
			fixture.RunStep(t, p.Ctx, dropped.conn, dropped.record, fixture.Exec(fmt.Sprintf("DROP TABLE hp_final_temp_%d", i), 0))
		}
		// Retire the dropped borrower without assuming namespace teardown.
		// The measured TEMP namespace differs from replayID; only the exact
		// relation/rowtype collision matters, and droppedID is checked below.
		if err := dropped.conn.Close(); err != nil {
			t.Fatal(err, facts())
		}
		for i, observed := range temporaryFacts {
			query := fmt.Sprintf("SELECT oid::BIGINT,reltype::BIGINT FROM pg_class WHERE relname='hp_final_temp_%d'", i)
			fixture.RunStep(t, p.Ctx, dropped.pool, dropped.record, fixture.Select(query, []string{"oid", "reltype"}, []string{"INT8", "INT8"}, nil))
			fixture.RunStep(t, p.Ctx, retained.conn, retained.record, fixture.Select(query, []string{"oid", "reltype"}, []string{"INT8", "INT8"}, [][]any{{observed[0], observed[1]}}))
		}
		if err := stuntgres.Restore(p.Ctx, dropped.control, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err, facts())
		}
		droppedID = referenceHashFinalID(t, p, dropped.pool, "hp_final_ref")
		if droppedID != replayID {
			t.Fatal("dropped TEMP control remapped replay identity", facts())
		}
		fixture.RunStep(t, p.Ctx, dropped.pool, dropped.record, fixture.Select("SELECT relname FROM pg_class WHERE relname='hp_final_oid_pad'", []string{"relname"}, []string{"NAME"}, nil))
		fixture.RunStep(t, p.Ctx, dropped.pool, dropped.record, fixture.Select("SELECT "+expression+" AS partition_key FROM hp_final", []string{"partition_key"}, []string{"INT4"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, dropped.pool, dropped.record, fixture.Select("SELECT id FROM hp_final_0 WHERE id=7", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if err := stuntgres.Restore(p.Ctx, retained.control, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err, facts())
		}
		finalID = referenceHashFinalID(t, p, retained.conn, "hp_final_ref")
		if finalID == replayID {
			t.Fatal("retained exact collision did not remap reference identity", facts())
		}
		apply := func(s fixture.Step) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, retained.conn, retained.record, s)
		}
		apply(fixture.Select("SELECT relname FROM pg_class WHERE relname='hp_final_oid_pad'", []string{"relname"}, []string{"NAME"}, nil))
		apply(fixture.Select("SELECT "+expression+" AS partition_key FROM hp_final", []string{"partition_key"}, []string{"INT4"}, [][]any{{int64(2)}}))
		for r := range 8 {
			var want [][]any
			if r == 2 {
				want = [][]any{{int64(7)}}
			}
			apply(fixture.Select(fmt.Sprintf("SELECT id FROM hp_final_%d WHERE id=7", r), []string{"id"}, []string{"INT4"}, want))
			apply(partitionConstraintIndexState(fmt.Sprintf("hp_final_idx_%d", r), true))
		}
		apply(fixture.Select("SELECT k::TEXT,id FROM hp_final", []string{"k", "id"}, []string{"TEXT", "INT4"}, [][]any{{"hp_final_ref", int64(7)}}))
		apply(fixture.Select("SELECT id FROM ONLY hp_final", []string{"id"}, []string{"INT4"}, nil))
		apply(fixture.Select("SELECT id FROM hp_final_0", []string{"id"}, []string{"INT4"}, nil))
		apply(partitionConstraintDuplicate("INSERT INTO hp_final VALUES('hp_final_ref',7)", "hp_final_2", "hp_final_idx_2", "Key (id)=(7) already exists."))
		apply(fixture.Exec("INSERT INTO hp_final VALUES('hp_final_ref',8)", 1))
		apply(fixture.Select("SELECT id FROM hp_final_2 WHERE id=8", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
		// The old numeric identity selects CASE true/residue0. Reusing id7 in
		// the emptied source proves its unique root no longer contains the row.
		apply(fixture.Exec(fmt.Sprintf("INSERT INTO hp_final_0 VALUES('%d'::REGCLASS,7)", replayID), 1))
		apply(fixture.Select("SELECT id FROM hp_final_0 WHERE id=7", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		apply(fixture.Select("SELECT id FROM hp_final ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(8)}}))
		for i := range len(temporaryFacts) {
			apply(fixture.Select(fmt.Sprintf("SELECT k FROM hp_final_temp_%d", i), []string{"k"}, []string{"INT4"}, [][]any{{int64(91)}}))
		}
	})
}
