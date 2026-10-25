package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePartitionConstraintsControlCollision(t *testing.T) {
	partitionConstraintCase(t, "retained_temp_and_reminted_nested_tree", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TEMP TABLE pc_allocator_anchor(k INT)", 0))
		run(fixture.Exec("CREATE TABLE pc_auto(a INT,b INT,payload TEXT,PRIMARY KEY(a,b)) PARTITION BY RANGE(a)", 0))
		run(fixture.Exec("CREATE TABLE pc_mid PARTITION OF pc_auto DEFAULT PARTITION BY RANGE(b)", 0))
		run(fixture.Exec("CREATE TABLE pc_leaf PARTITION OF pc_mid(CONSTRAINT pc_local UNIQUE(payload)) DEFAULT", 0))
		run(fixture.Exec("ALTER INDEX pc_auto_pkey RENAME TO pc_swap", 0))
		run(fixture.Exec("ALTER INDEX pc_leaf_pkey RENAME TO pc_auto_pkey", 0))
		run(fixture.Exec("ALTER INDEX pc_swap RENAME TO pc_leaf_pkey", 0))
		run(fixture.Exec("INSERT INTO pc_auto VALUES(1,1,'x')", 1))
		run(fixture.Exec("CREATE TABLE pc_manual(a INT,b INT) PARTITION BY RANGE(a)", 0))
		run(fixture.Exec("CREATE TABLE pc_manual_mid PARTITION OF pc_manual DEFAULT PARTITION BY RANGE(b)", 0))
		run(fixture.Exec("CREATE TABLE pc_manual_a PARTITION OF pc_manual_mid FOR VALUES FROM(1) TO(3)", 0))
		run(fixture.Exec("CREATE TABLE pc_manual_b PARTITION OF pc_manual_mid DEFAULT", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_manual_root_idx ON ONLY pc_manual(a,b)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_manual_mid_idx ON ONLY pc_manual_mid(a,b)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_manual_a_idx ON pc_manual_a(a,b)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_manual_b_idx ON pc_manual_b(a,b)", 0))
		run(fixture.Exec("ALTER INDEX pc_manual_mid_idx ATTACH PARTITION pc_manual_a_idx", 0))
		run(fixture.Exec("ALTER INDEX pc_manual_root_idx ATTACH PARTITION pc_manual_mid_idx", 0))
		check := func(db fixture.Executor, record *fixture.Recorder) {
			apply := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, db, record, s) }
			apply(fixture.Select("SELECT a,b,payload FROM pc_auto", []string{"a", "b", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "x"}}))
			apply(partitionConstraintEdge("pc_auto_pkey", "pc_mid_pkey", "pc_leaf"))
			apply(partitionConstraintEdge("pc_mid_pkey", "pc_leaf_pkey", "pc_mid"))
			apply(partitionConstraintEdge("pc_manual_mid_idx", "pc_manual_root_idx", "pc_manual_mid"))
			apply(partitionConstraintIndexState("pc_manual_root_idx", false))
			apply(partitionConstraintIndexState("pc_manual_mid_idx", false))
			apply(fixture.Select("SELECT count(*)::int4 AS n FROM pg_depend WHERE objid='pc_manual_b_idx'::regclass::OID AND deptype='P'", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}}))
			apply(fixture.Select("SELECT c.conname,c.conparentid=p.oid AS immediate,c.conislocal,c.coninhcount FROM pg_constraint c JOIN pg_constraint p ON p.conname='pc_mid_pkey' WHERE c.conname='pc_auto_pkey'", []string{"conname", "immediate", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "BOOL", "INT2"}, [][]any{{"pc_auto_pkey", true, false, int64(1)}}))
			apply(partitionConstraintDuplicate("INSERT INTO pc_auto VALUES(1,1,'fresh')", "pc_leaf", "pc_auto_pkey", "Key (a, b)=(1, 1) already exists."))
			apply(partitionConstraintDuplicate("INSERT INTO pc_auto VALUES(2,2,'x')", "pc_leaf", "pc_local", "Key (payload)=(x) already exists."))
		}
		check(b.DB, b.Recorder)
		if b.PG {
			return
		}
		var sourceID int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='pc_auto'").Scan(&sourceID); err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("partition-remint")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		recordedDestination, destinationRecord := fixture.InsertSelectRecordedPool(t, connector)
		conn, err := recordedDestination.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, query := range []string{"CREATE TEMP TABLE pc_allocator_anchor(k INT)", "CREATE TEMP TABLE pc_retained(a INT,b INT,payload TEXT,PRIMARY KEY(a,b))", "INSERT INTO pc_retained VALUES(91,92,'sentinel')"} {
			if _, err := conn.ExecContext(p.Ctx, query); err != nil {
				t.Fatal(err)
			}
		}
		var collisionID int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='pc_retained'").Scan(&collisionID); err != nil {
			t.Fatal(err)
		}
		if collisionID != sourceID {
			t.Fatalf("fixture did not construct real allocator collision: %d != %d", collisionID, sourceID)
		}
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT a,b,payload FROM pc_auto")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(conn, destinationRecord)
			var restoredID int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='pc_auto'").Scan(&restoredID); err != nil {
				t.Fatal(err)
			}
			if restoredID == collisionID {
				t.Fatal("persistent identity aliases live TEMP table")
			}
			fixture.RunStep(t, p.Ctx, conn, destinationRecord, fixture.Select("SELECT a,b,payload FROM pc_retained", []string{"a", "b", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(91), int64(92), "sentinel"}}))
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, destination, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("collision roundtrip changed canonical stream")
			}
		}
		if !retained.Next() {
			t.Fatal("source retained cursor lost row")
		}
		var a, bv int64
		var payload string
		if err := retained.Scan(&a, &bv, &payload); err != nil || a != 1 || bv != 1 || payload != "x" {
			t.Fatal(a, bv, payload, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("source cursor changed")
		}
		for _, suffix := range []string{"INSERT INTO pc_auto VALUES(1,1,'late');", "CREATE TABLE pc_invalid(k INT,k INT);"} {
			if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump.String()+suffix)); err == nil {
				t.Fatal("invalid candidate replaced destination")
			}
			check(conn, destinationRecord)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		reader := &referenceRangeCancelEOF{Reader: strings.NewReader(dump.String()), cancel: cancel}
		if err := stuntgres.Restore(ctx, destination, reader); !errors.Is(err, context.Canceled) {
			t.Fatal("EOF cancellation was not preserved", err)
		}
		cancel()
		check(conn, destinationRecord)
		if p.Postgres != nil {
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
					if _, err := oracle.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual swapped/partial emitted replay", err)
			}
			check(oracle, p.PostgresRecord)
		}
	})
}
