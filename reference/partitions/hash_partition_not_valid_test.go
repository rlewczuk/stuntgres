package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHashPartitionNotValidRestore(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE hp_nv(k INT,payload TEXT) PARTITION BY HASH(k)", 0))
		run(fixture.Exec("CREATE TABLE hp_nv_mid PARTITION OF hp_nv FOR VALUES WITH(MODULUS 1,REMAINDER 0) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE hp_nv_leaf(k INT,payload TEXT)", 0))
		run(fixture.Exec("INSERT INTO hp_nv_leaf VALUES(1,'old')", 1))
		run(fixture.Exec("ALTER TABLE hp_nv_leaf ADD CONSTRAINT hp_nv_old CHECK(payload <> 'old') NOT VALID", 0))
		run(fixture.Exec("ALTER TABLE hp_nv_mid ATTACH PARTITION hp_nv_leaf DEFAULT", 0))
		check := func(db fixture.Executor, record *fixture.Recorder) {
			apply := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, db, record, s) }
			for _, table := range []string{"hp_nv", "hp_nv_mid", "hp_nv_leaf"} {
				apply(fixture.Select("SELECT k,payload FROM "+table, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}}))
			}
			apply(fixture.Select("SELECT k FROM ONLY hp_nv", []string{"k"}, []string{"INT4"}, nil))
			apply(fixture.Select("SELECT k FROM ONLY hp_nv_mid", []string{"k"}, []string{"INT4"}, nil))
			apply(fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='hp_nv_leaf'::REGCLASS::OID AND conname='hp_nv_old'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{false}}))
			apply(fixture.Select("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='hp_nv_leaf'::REGCLASS::OID AND conname='hp_nv_old'", []string{"definition"}, []string{"TEXT"}, [][]any{{"CHECK ((payload <> 'old'::text)) NOT VALID"}}))
			for _, table := range []string{"hp_nv", "hp_nv_leaf"} {
				apply(referenceRangeError("INSERT INTO "+table+" VALUES(2,'old')", "23514", `new row for relation "hp_nv_leaf" violates check constraint "hp_nv_old"`, fixture.Diagnostic{Schema: "public", Table: "hp_nv_leaf", Constraint: "hp_nv_old", Detail: "Failing row contains (2, old)."}))
			}
			apply(fixture.Select("SELECT k,payload FROM hp_nv_leaf", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}}))
		}
		check(b.DB, b.Recorder)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(b.DB, b.Recorder)
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("unchanged NOT VALID roundtrip changed logical definitions/data")
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("hash-not-valid-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		pool, record := fixture.InsertSelectRecordedPool(t, connector)
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(pool, record)
		fixture.RunStep(t, p.Ctx, pool, record, fixture.Exec("INSERT INTO hp_nv VALUES(2,'new')", 1))
		fixture.RunStep(t, p.Ctx, pool, record, fixture.Select("SELECT k,payload FROM hp_nv ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "new"}}))
	})
}
