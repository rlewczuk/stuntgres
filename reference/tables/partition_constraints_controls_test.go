package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePartitionConstraintsControls(t *testing.T) {
	partitionConstraintCase(t, "custom_names_and_partial_tree", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_auto(a INT,b INT,payload TEXT,CONSTRAINT pc_auto_key PRIMARY KEY(a,b)) PARTITION BY RANGE(a)", 0))
		run(fixture.Exec("CREATE TABLE pc_mid PARTITION OF pc_auto DEFAULT PARTITION BY RANGE(b)", 0))
		run(fixture.Exec("CREATE TABLE pc_leaf PARTITION OF pc_mid(CONSTRAINT pc_local UNIQUE(payload)) DEFAULT", 0))
		run(fixture.Exec("ALTER INDEX pc_mid_pkey RENAME TO pc_mid_custom", 0))
		run(fixture.Exec("ALTER INDEX pc_leaf_pkey RENAME TO pc_leaf_custom", 0))
		run(fixture.Exec("INSERT INTO pc_auto VALUES(1,1,'x')", 1))
		partitionConstraintManualTree(run)
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", 0))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'a'),(3,'b')", 2))
		run(fixture.Exec("CREATE TABLE pc_nnroot(k INT CONSTRAINT pc_named_nn NOT NULL,CONSTRAINT pc_named_ck CHECK(k>0)) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_nnleaf PARTITION OF pc_nnroot(k CONSTRAINT pc_named_nn NOT NULL,CONSTRAINT pc_named_ck CHECK(k>0)) DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pc_nnroot VALUES(1)", 1))
		run(fixture.Exec("CREATE TABLE pc_reuse_root(k INT,payload INT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_reuse_mid PARTITION OF pc_reuse_root DEFAULT PARTITION BY RANGE(payload)", 0))
		run(fixture.Exec("CREATE TABLE pc_reuse_leaf PARTITION OF pc_reuse_mid DEFAULT", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_reuse_mid_idx ON ONLY pc_reuse_mid(k,payload)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_reuse_root_idx ON pc_reuse_root(k,payload)", 0))
		run(fixture.Exec("INSERT INTO pc_reuse_root VALUES(1,1),(1,1)", 2))
		check := func(db fixture.Executor, record *fixture.Recorder) {
			apply := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, db, record, s) }
			apply(fixture.Select("SELECT a,b,payload FROM pc_auto", []string{"a", "b", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "x"}}))
			apply(fixture.Select("SELECT k,payload FROM pc_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), "b"}}))
			apply(partitionConstraintIndexState("pc_root_idx", false))
			apply(partitionConstraintEdge("pc_a_idx", "pc_root_idx", "pc_a"))
			apply(partitionConstraintEdge("pc_leaf_custom", "pc_mid_custom", "pc_leaf"))
			apply(partitionConstraintEdge("pc_mid_custom", "pc_auto_key", "pc_mid"))
			apply(fixture.Select("SELECT conname,conislocal FROM pg_constraint WHERE conname='pc_local'", []string{"conname", "conislocal"}, []string{"NAME", "BOOL"}, [][]any{{"pc_local", true}}))
			apply(partitionConstraintDuplicate("INSERT INTO pc_auto VALUES(1,1,'new')", "pc_leaf", "pc_leaf_custom", "Key (a, b)=(1, 1) already exists."))
			apply(partitionConstraintDuplicate("INSERT INTO pc_auto VALUES(2,2,'x')", "pc_leaf", "pc_local", "Key (payload)=(x) already exists."))
			apply(fixture.Select("SELECT conname,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pc_nnleaf'::regclass::OID ORDER BY conname", []string{"conname", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"pc_named_ck", false, int64(1)}, {"pc_named_nn", true, int64(1)}}))
			apply(fixture.Select("SELECT k FROM pc_nnroot", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			apply(partitionConstraintIndexState("pc_reuse_root_idx", false))
			apply(partitionConstraintIndexState("pc_reuse_mid_idx", false))
			apply(partitionConstraintEdge("pc_reuse_mid_idx", "pc_reuse_root_idx", "pc_reuse_mid"))
			apply(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_reuse_leaf_k_payload_idx'", []string{"relname"}, []string{"NAME"}, nil))
			apply(fixture.Select("SELECT k,payload FROM pc_reuse_root ORDER BY k,payload", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}}))
		}
		check(b.DB, b.Recorder)
		if b.PG {
			return
		} // SQL state above is the shared oracle; PG has no Restore API.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT a,b,payload FROM pc_auto")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		metadata, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		heldInsert, err := b.DB.PrepareContext(p.Ctx, "INSERT INTO pc_auto VALUES($1,$2,$3)")
		if err != nil {
			t.Fatal(err)
		}
		defer heldInsert.Close()
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(b.DB, b.Recorder)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("same-driver redump is not deterministic")
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("partition-constraints-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		recordedDestination, destinationRecord := fixture.InsertSelectRecordedPool(t, connector)
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(recordedDestination, destinationRecord)
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(recordedDestination, destinationRecord)
		if p.Postgres != nil {
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, query := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
					if _, err := oracle.ExecContext(ctx, query); err != nil {
						t.Error(err)
					}
				}
			}()
			if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual emitted partition constraint replay", err)
			}
			check(oracle, p.PostgresRecord)
			fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, fixture.Exec("INSERT INTO pc_auto VALUES(2,2,'healthy')", 1))
		}
		for _, suffix := range []string{"INSERT INTO pc_auto VALUES(1,1,'duplicate');", "CREATE TABLE pc_restore_bad(k INT,k INT);"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+suffix)); err == nil {
				t.Fatal("bad restore accepted")
			}
			check(b.DB, b.Recorder)
		}
		fixture.TableDDLColumnTypes(t, false, metadata, []string{"a", "b", "payload"}, []string{"INT4", "INT4", "TEXT"}, []bool{false, false, true})
		run(fixture.Exec("ALTER TABLE pc_auto DROP CONSTRAINT pc_auto_key", 0))
		held := fixture.HeldStatement{Executor: b.DB, Statement: heldInsert, SQL: "INSERT INTO pc_auto VALUES($1,$2,$3)"}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 1, 1, 1, "new"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, partitionConstraintDuplicate(held.SQL, "pc_leaf", "pc_local", "Key (payload)=(x) already exists.", 2, 2, "x"))
		var oldRows [][]any
		for retained.Next() {
			var a, b, payload any
			if err := retained.Scan(&a, &b, &payload); err != nil {
				t.Fatal(err)
			}
			oldRows = append(oldRows, []any{a, b, payload})
		}
		if err := retained.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(oldRows, [][]any{{int64(1), int64(1), "x"}}) {
			t.Fatal("retained cursor changed across Restore and backing drop", oldRows)
		}
	})
}

func TestReferencePartitionConstraintsFeatureSmoke(t *testing.T) {
	partitionConstraintCase(t, "routed_key_and_manual_index", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintTree(run, "PRIMARY KEY(k,payload)")
		run(fixture.Exec("INSERT INTO pc_root VALUES($1,$2)", 1, 1, "x"))
		run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES($1,$2)", "pc_a", "pc_a_pkey", "Key (k, payload)=(1, x) already exists.", 1, "x"))
		s := fixture.Select("SELECT k,payload FROM pc_root", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}})
		s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{false, false})
		run(s)
		run(fixture.Exec("CREATE TABLE pc_manual(k INT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_manual_leaf PARTITION OF pc_manual DEFAULT", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_manual_idx ON ONLY pc_manual(k)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_manual_leaf_idx ON pc_manual_leaf(k)", 0))
		run(partitionConstraintIndexState("pc_manual_idx", false))
		run(fixture.Exec("ALTER INDEX pc_manual_idx ATTACH PARTITION pc_manual_leaf_idx", 0))
		run(partitionConstraintIndexState("pc_manual_idx", true))
		run(referenceRangeError("DROP INDEX pc_manual_leaf_idx", "2BP01", "cannot drop index pc_manual_leaf_idx because index pc_manual_idx requires it", fixture.Diagnostic{Hint: "You can drop index pc_manual_idx instead."}))
	})
}
