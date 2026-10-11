package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// The postgres-only route specifies names and NULL policies independently using
// valid SQL. The memory route additionally exercises the native-only replacement
// API and, in reference mode, replays its actual unmodified emitted SQL on PG18.
func TestReferencePartitionConstraintsMultiKeyCollision(t *testing.T) {
	partitionConstraintCase(t, "selective_earlier_backing", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		// Persistent padding is emitted too. It leaves enough allocator space for
		// the destination TEMP namespace/anchor without touching either tree owner.
		run(fixture.Exec("CREATE TABLE pc_pad_a(k INT)", 0))
		run(fixture.Exec("CREATE TABLE pc_pad_b(k INT)", 0))
		run(fixture.Exec("CREATE TABLE pc_root(k INT,payload TEXT,CONSTRAINT pc_distinct UNIQUE(k,payload),CONSTRAINT pc_nnd UNIQUE NULLS NOT DISTINCT(k,payload)) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_leaf PARTITION OF pc_root DEFAULT", 0))
		run(fixture.Exec("ALTER INDEX pc_leaf_k_payload_key RENAME TO pc_leaf_distinct", 0))
		run(fixture.Exec("ALTER INDEX pc_leaf_k_payload_key1 RENAME TO pc_leaf_nnd", 0))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,NULL)", 1))

		applyTo := func(db fixture.Executor, record *fixture.Recorder) func(fixture.Step) {
			return func(s fixture.Step) {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, db, record, s)
			}
		}
		checkKeys := func(apply func(fixture.Step)) {
			query := "SELECT c.conname,c.conindid=r.oid AS backing,i.indnullsnotdistinct,c.conparentid=0::OID AS no_parent,c.conislocal,c.coninhcount FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid JOIN pg_class r ON r.oid=c.conindid AND r.relname=c.conname WHERE c.conrelid='pc_root'::regclass::OID ORDER BY c.conname"
			apply(fixture.Select(query,
				[]string{"conname", "backing", "indnullsnotdistinct", "no_parent", "conislocal", "coninhcount"},
				[]string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "INT2"},
				[][]any{{"pc_distinct", true, false, true, true, int64(0)}, {"pc_nnd", true, true, true, true, int64(0)}}))
		}
		checkChild := func(apply func(fixture.Step), table, distinct, nnd string) {
			// Join on the actual conparentid, not a name-based inferred edge. Also
			// require each constraint to own the identically named backing relation.
			apply(fixture.Select("SELECT c.conname,i.indnullsnotdistinct,p.conname AS parent_name,c.conparentid=p.oid AS immediate,c.conindid=r.oid AS backing,c.conislocal,c.coninhcount FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid JOIN pg_constraint p ON p.oid=c.conparentid JOIN pg_class r ON r.oid=c.conindid AND r.relname=c.conname WHERE c.conrelid='"+table+"'::regclass::OID ORDER BY c.conname",
				[]string{"conname", "indnullsnotdistinct", "parent_name", "immediate", "backing", "conislocal", "coninhcount"},
				[]string{"NAME", "BOOL", "NAME", "BOOL", "BOOL", "BOOL", "INT2"},
				[][]any{{distinct, false, "pc_distinct", true, true, false, int64(1)}, {nnd, true, "pc_nnd", true, true, false, int64(1)}}))
			apply(partitionConstraintEdge(distinct, "pc_distinct", table))
			apply(partitionConstraintEdge(nnd, "pc_nnd", table))
		}
		check := func(db fixture.Executor, record *fixture.Recorder, pg bool) {
			apply := applyTo(db, record)
			checkKeys(apply)
			checkChild(apply, "pc_leaf", "pc_leaf_distinct", "pc_leaf_nnd")
			s := fixture.Select("SELECT k,payload FROM pc_root", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}})
			s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{true, true})
			apply(s)
			empty := fixture.Select("SELECT k,payload FROM pc_root WHERE FALSE", s.Columns, s.Types, nil)
			empty.CheckMetadata = s.CheckMetadata
			apply(empty)
			// Only the NND key can reject this NULL-containing duplicate. The
			// first DISTINCT key must not steal its current physical diagnostic.
			apply(partitionConstraintDuplicate("INSERT INTO pc_root VALUES($1,$2)", "pc_leaf", "pc_leaf_nnd", "Key (k, payload)=(1, null) already exists.", 1, nil))
			apply(fixture.Select("SELECT k,payload FROM pc_leaf", s.Columns, s.Types, [][]any{{int64(1), nil}}))
		}
		future := func(db fixture.Executor, record *fixture.Recorder) {
			apply := applyTo(db, record)
			apply(fixture.Exec("CREATE TABLE pc_future PARTITION OF pc_root FOR VALUES FROM(10) TO(20)", 0))
			checkChild(apply, "pc_future", "pc_future_k_payload_key", "pc_future_k_payload_key1")
			apply(fixture.Exec("INSERT INTO pc_root VALUES($1,$2)", 1, 11, nil))
			apply(partitionConstraintDuplicate("INSERT INTO pc_root VALUES($1,$2)", "pc_future", "pc_future_k_payload_key1", "Key (k, payload)=(11, null) already exists.", 11, nil))
			apply(fixture.Select("SELECT k,payload FROM pc_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(11), nil}}))
		}
		check(b.DB, b.Recorder, b.PG)
		if b.PG {
			future(b.DB, b.Recorder)
			return // PostgreSQL has no native Restore or retained-TEMP importer API.
		}

		capture := func(db interface {
			QueryRowContext(context.Context, string, ...any) *sql.Row
		}) map[string]int64 {
			ids := make(map[string]int64)
			seen := make(map[int64]string)
			for _, name := range []string{"pc_root", "pc_leaf", "pc_distinct", "pc_nnd", "pc_leaf_distinct", "pc_leaf_nnd"} {
				var id int64
				if err := db.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='"+name+"'").Scan(&id); err != nil {
					t.Fatal(name, err)
				}
				ids[name] = id
			}
			for _, name := range []string{"pc_distinct", "pc_nnd", "pc_leaf_distinct", "pc_leaf_nnd"} {
				var id int64
				if err := db.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='"+name+"'").Scan(&id); err != nil {
					t.Fatal(name, err)
				}
				ids["constraint:"+name] = id
			}
			for name, id := range ids {
				if id <= 0 || seen[id] != "" {
					t.Fatalf("identities must be positive and distinct: %s=%d aliases %s", name, id, seen[id])
				}
				seen[id] = name
			}
			return ids
		}
		sourceIDs := capture(b.DB)
		if sourceIDs["pc_distinct"] >= sourceIDs["pc_nnd"] {
			t.Fatal("source did not allocate DISTINCT backing before NND backing", sourceIDs)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("partition-two-key-remint")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		recorded, record := fixture.InsertSelectRecordedPool(t, connector)
		conn, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		apply := applyTo(conn, record)
		apply(fixture.Exec("CREATE TEMP TABLE pc_retained_anchor(k INT)", 0))
		apply(fixture.Exec("INSERT INTO pc_retained_anchor VALUES(91)", 1))
		// Measure each real allocation. Keep only the TEMP sequence whose live
		// identity collides with the earlier backing index, not its owner/root.
		var collisionID int64
		for range 32 {
			apply(fixture.Exec("CREATE TEMP SEQUENCE pc_retained_collision START WITH 91", 0))
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='pc_retained_collision'").Scan(&collisionID); err != nil {
				t.Fatal(err)
			}
			if collisionID == sourceIDs["pc_distinct"] {
				break
			}
			if collisionID > sourceIDs["pc_distinct"] {
				t.Fatalf("allocator alignment overshot earlier backing: retained=%d source=%d", collisionID, sourceIDs["pc_distinct"])
			}
			apply(fixture.Exec("DROP SEQUENCE pc_retained_collision", 0))
		}
		if collisionID != sourceIDs["pc_distinct"] {
			t.Fatalf("no actual earlier-backing collision: retained=%d source=%d", collisionID, sourceIDs["pc_distinct"])
		}
		apply(fixture.Select("SELECT nextval('pc_retained_collision') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(91)}}))
		var emitted bytes.Buffer
		for cycle := 1; cycle <= 2; cycle++ {
			if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatalf("actual selective collision Restore cycle %d: %v", cycle, err)
			}
			restored := capture(conn)
			if restored["pc_distinct"] <= restored["pc_nnd"] || restored["pc_distinct"] == collisionID {
				t.Fatalf("earlier backing did not selectively remint above later backing: source=%v restored=%v retained=%d", sourceIDs, restored, collisionID)
			}
			for name, id := range sourceIDs {
				if name != "pc_distinct" && restored[name] != id {
					t.Fatalf("noncolliding identity changed in cycle %d: %s got %d want %d", cycle, name, restored[name], id)
				}
			}
			check(conn, record, false)
			apply(fixture.Select("SELECT k FROM pc_retained_anchor", []string{"k"}, []string{"INT4"}, [][]any{{int64(91)}}))
			apply(fixture.Select("SELECT last_value,is_called FROM pc_retained_collision", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(91), true}}))
			var retainedID int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='pc_retained_collision'").Scan(&retainedID); err != nil || retainedID != collisionID {
				t.Fatal("retained TEMP identity changed", retainedID, collisionID, err)
			}
			emitted.Reset()
			if err := stuntgres.Dump(p.Ctx, destination, &emitted); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), emitted.Bytes()) {
				t.Fatalf("selective collision cycle %d changed exact source/redump bytes", cycle)
			}
		}
		future(conn, record)
		if p.Postgres != nil {
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, query := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
					if _, err := oracle.ExecContext(ctx, query); err != nil {
						t.Error(err)
					}
				}
			}()
			// No rewritten replay, synthetic dump or native-to-PG parity bag.
			if _, err := oracle.ExecContext(p.Ctx, emitted.String()); err != nil {
				t.Fatal("actual selective-collision emitted PostgreSQL replay", err)
			}
			check(oracle, p.PostgresRecord, true)
			future(oracle, p.PostgresRecord)
		}
	})
}

// This oracle-first supplement pins the creation-order question for a mid-level
// local NND key and an inherited DISTINCT key sharing the same attribute stem.
// Before root's pinned observation, the explicit suffix expectations below are
// author predictions: inherited indexes precede the local CREATE INDEX work.
func TestReferencePartitionConstraintsMultiKeyNestedOrder(t *testing.T) {
	partitionConstraintCase(t, "local_nnd_and_inherited_distinct", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_nested_r(k INT,b INT,payload TEXT,CONSTRAINT pc_nested_d UNIQUE(k,b,payload)) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_nested_m PARTITION OF pc_nested_r(CONSTRAINT pc_nested_local_n UNIQUE NULLS NOT DISTINCT(k,b,payload)) FOR VALUES FROM(0) TO(10) PARTITION BY RANGE(b)", 0))
		run(fixture.Exec("CREATE TABLE pc_nested_l PARTITION OF pc_nested_m FOR VALUES FROM(0) TO(10)", 0))
		run(fixture.Exec("INSERT INTO pc_nested_r VALUES(1,1,NULL)", 1))
		check := func(db fixture.Executor, record *fixture.Recorder) {
			apply := func(s fixture.Step) {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, db, record, s)
			}
			apply(fixture.Select("SELECT c.conname,i.indnullsnotdistinct,c.conparentid=0::OID AS no_parent,c.conislocal,c.coninhcount FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid WHERE c.conrelid='pc_nested_r'::regclass::OID ORDER BY c.conname",
				[]string{"conname", "indnullsnotdistinct", "no_parent", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "INT2"},
				[][]any{{"pc_nested_d", false, true, true, int64(0)}}))
			apply(fixture.Select("SELECT c.conname,i.indnullsnotdistinct,c.conparentid=0::OID AS no_parent,c.conislocal,c.coninhcount FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid WHERE c.conrelid='pc_nested_m'::regclass::OID ORDER BY c.conname",
				[]string{"conname", "indnullsnotdistinct", "no_parent", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "INT2"},
				[][]any{{"pc_nested_local_n", true, true, true, int64(0)}, {"pc_nested_m_k_b_payload_key", false, false, false, int64(1)}}))
			apply(fixture.Select("SELECT c.conname,i.indnullsnotdistinct,p.conname AS parent_name,p.conislocal AS parent_local,c.conparentid=p.oid AS immediate,c.conindid=r.oid AS backing FROM pg_constraint c JOIN pg_index i ON i.indexrelid=c.conindid JOIN pg_constraint p ON p.oid=c.conparentid JOIN pg_class r ON r.oid=c.conindid AND r.relname=c.conname WHERE c.conrelid='pc_nested_l'::regclass::OID ORDER BY c.conname",
				[]string{"conname", "indnullsnotdistinct", "parent_name", "parent_local", "immediate", "backing"}, []string{"NAME", "BOOL", "NAME", "BOOL", "BOOL", "BOOL"},
				[][]any{{"pc_nested_l_k_b_payload_key", false, "pc_nested_m_k_b_payload_key", false, true, true}, {"pc_nested_l_k_b_payload_key1", true, "pc_nested_local_n", true, true, true}}))
			apply(fixture.Select("SELECT c.conparentid=p.oid AS immediate FROM pg_constraint c JOIN pg_constraint p ON p.conname='pc_nested_d' WHERE c.conname='pc_nested_m_k_b_payload_key'",
				[]string{"immediate"}, []string{"BOOL"}, [][]any{{true}}))
			apply(partitionConstraintEdge("pc_nested_l_k_b_payload_key", "pc_nested_m_k_b_payload_key", "pc_nested_l"))
			apply(partitionConstraintEdge("pc_nested_l_k_b_payload_key1", "pc_nested_local_n", "pc_nested_l"))
			apply(partitionConstraintDuplicate("INSERT INTO pc_nested_r VALUES($1,$2,$3)", "pc_nested_l", "pc_nested_l_k_b_payload_key1", "Key (k, b, payload)=(1, 1, null) already exists.", 1, 1, nil))
			apply(fixture.Select("SELECT k,b,payload FROM pc_nested_r", []string{"k", "b", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), nil}}))
		}
		check(b.DB, b.Recorder)
		if b.PG {
			return // Shared SQL pins the order; native control APIs are not PG APIs.
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal("actual nested local/inherited Restore", err)
		}
		check(b.DB, b.Recorder)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("nested local/inherited Restore changed exact source/redump bytes")
		}
		if p.Postgres != nil {
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, query := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
					if _, err := oracle.ExecContext(ctx, query); err != nil {
						t.Error(err)
					}
				}
			}()
			if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual nested local/inherited emitted PostgreSQL replay", err)
			}
			check(oracle, p.PostgresRecord)
		}
	})
}
