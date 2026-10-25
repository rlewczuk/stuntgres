package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func macOperationsSavedAssertions(t *testing.T, p *fixture.Pair, executor fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	pg := recorder.RawBackend == fixture.RawPostgres
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, recorder, s) }
	run(fixture.Select("SELECT id,g6 FROM mac_ops_generated ORDER BY id", []string{"id", "g6"}, []string{"INT4", "MACADDR"}, [][]any{{int64(1), "12:34:56:00:00:00"}, {int64(2), nil}}))
	run(macaddr8Select(pg, "SELECT id,m6,m8,d6,d8,e8 FROM mac_ops_saved ORDER BY id", []string{"id", "m6", "m8", "d6", "d8", "e8"}, []string{"INT4", "MACADDR", "MACADDR8", "MACADDR", "MACADDR8", "MACADDR8"}, [][]any{
		{int64(1), "12:34:56:78:90:ab", "12:34:56:78:90:ab:cd:ef", "12:34:56:00:00:00", "12:34:56:00:00:00:00:00", "02:34:56:ff:fe:ab:cd:ef"},
		{int64(2), "00:00:00:00:00:00", "00:00:00:00:00:00:00:00", "12:34:56:00:00:00", "12:34:56:00:00:00:00:00", "02:34:56:ff:fe:ab:cd:ef"},
		{int64(3), nil, nil, "12:34:56:00:00:00", "12:34:56:00:00:00:00:00", "02:34:56:ff:fe:ab:cd:ef"},
	}))
	s := macaddr8Select(pg, "SELECT id,n6,a6,o6,n8,a8,o8,t6,t8,e6,e8 FROM mac_ops_view ORDER BY id", []string{"id", "n6", "a6", "o6", "n8", "a8", "o8", "t6", "t8", "e6", "e8"}, []string{"INT4", "MACADDR", "MACADDR", "MACADDR", "MACADDR8", "MACADDR8", "MACADDR8", "MACADDR", "MACADDR8", "MACADDR8", "MACADDR8"}, [][]any{
		{int64(1), "ed:cb:a9:87:6f:54", "02:30:02:50:90:00", "1f:f4:fe:7d:ff:ab", "ed:cb:a9:87:6f:54:32:10", "02:30:02:50:90:00:01:cc", "1f:f4:fe:7d:ff:ab:ff:ef", "12:34:56:00:00:00", "12:34:56:00:00:00:00:00", "12:34:56:ff:fe:78:90:ab", "12:34:56:78:90:ab:cd:ef"},
		{int64(2), "ff:ff:ff:ff:ff:ff", "00:00:00:00:00:00", "0f:f0:aa:55:ff:00", "ff:ff:ff:ff:ff:ff:ff:ff", "00:00:00:00:00:00:00:00", "0f:f0:aa:55:ff:00:33:cc", "00:00:00:00:00:00", "00:00:00:00:00:00:00:00", "02:00:00:ff:fe:00:00:00", "02:00:00:00:00:00:00:00"},
		{int64(3), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil},
	})
	run(s)
	run(fixture.Prepared(s))
	// A fresh write proves that reconstructed defaults still execute, not just data replay.
	run(fixture.Exec("INSERT INTO mac_ops_saved(id) VALUES(4)", 1))
	run(macaddr8Select(pg, "SELECT d6,d8,e8 FROM mac_ops_saved WHERE id=4", []string{"d6", "d8", "e8"}, []string{"MACADDR", "MACADDR8", "MACADDR8"}, [][]any{{"12:34:56:00:00:00", "12:34:56:00:00:00:00:00", "02:34:56:ff:fe:ab:cd:ef"}}))
	run(fixture.Exec("DELETE FROM mac_ops_saved WHERE id=4", 1))
	_, transactional := executor.(*sql.Tx)
	if transactional {
		run(fixture.Exec("SAVEPOINT mac_ops_check", 0))
	}
	d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "mac_ops_saved" violates check constraint "mac_ops_prefix"`)
	d.Detail = "Failing row contains (5, null, null, 12:34:56:78:90:ab, 12:34:56:00:00:00:00:00, 02:34:56:ff:fe:ab:cd:ef)."
	d.Schema, d.Table, d.Constraint = "public", "mac_ops_saved", "mac_ops_prefix"
	run(fixture.ErrorDiagnostic("INSERT INTO mac_ops_saved(id,d6) VALUES(5,'12:34:56:78:90:ab')", d))
	if transactional {
		run(fixture.Exec("ROLLBACK TO SAVEPOINT mac_ops_check", 0))
		run(fixture.Exec("RELEASE SAVEPOINT mac_ops_check", 0))
	}
	run(fixture.Select("SELECT id FROM mac_ops_saved WHERE id=5", []string{"id"}, []string{"INT4"}, nil))
}

func macOperationsSavedControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	rows, err := b.DB.QueryContext(p.Ctx, "SELECT macaddr8_set7bit(m6) AS v FROM mac_ops_saved ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	macaddr8Metadata(t, rows, false, nil)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatal("MAC operations dump changed after Reset/Restore")
	}
	macOperationsSavedAssertions(t, p, b.DB, b.Recorder)
	fresh := fixture.NewFixture(t, false, true)
	if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	macOperationsSavedAssertions(t, p, fresh.Memory, fresh.MemoryRecord)
	var freshDump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, fresh.Native, &freshDump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != freshDump.String() {
		t.Fatal("MAC operations dump changed on fresh Driver")
	}
	for _, want := range []any{"12:34:56:ff:fe:78:90:ab", "02:00:00:ff:fe:00:00:00", nil} {
		if !rows.Next() {
			t.Fatal("missing retained MAC expression row", rows.Err())
		}
		var got any
		if err := rows.Scan(&got); err != nil || got != want {
			t.Fatalf("retained MAC expression got %v want %v: %v", got, want, err)
		}
	}
	macaddr8Metadata(t, rows, false, nil)
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra retained MAC expression row", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		tx, err := p.Postgres.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
		macOperationsSavedAssertions(t, p, tx, p.PostgresRecord)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceMACOperationsStored(t *testing.T) {
	macOperationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mac_ops_saved(id integer,m6 macaddr,m8 macaddr8,d6 macaddr DEFAULT trunc('12:34:56:78:90:ab'::macaddr),d8 macaddr8 DEFAULT trunc('12:34:56:78:90:ab:cd:ef'::macaddr8),e8 macaddr8 DEFAULT macaddr8_set7bit('00:34:56:ab:cd:ef'::macaddr),CONSTRAINT mac_ops_prefix CHECK(trunc(d6)=d6))", 0))
		run(fixture.Exec("INSERT INTO mac_ops_saved(id,m6,m8) VALUES(1,'12:34:56:78:90:ab','12:34:56:78:90:ab:cd:ef'),(2,'00:00:00:00:00:00','00:00:00:00:00:00:00:00'),(3,NULL,NULL)", 3))
		run(fixture.Exec("CREATE VIEW mac_ops_view AS SELECT id,~m6 AS n6,m6 & macaddr '0f:f0:aa:55:ff:00' AS a6,m6 | macaddr '0f:f0:aa:55:ff:00' AS o6,~m8 AS n8,m8 & macaddr8 '0f:f0:aa:55:ff:00:33:cc' AS a8,m8 | macaddr8 '0f:f0:aa:55:ff:00:33:cc' AS o8,trunc(m6) AS t6,trunc(m8) AS t8,macaddr8_set7bit(m6) AS e6,macaddr8_set7bit(m8) AS e8 FROM mac_ops_saved", 0))
		run(fixture.Exec("CREATE TABLE mac_ops_generated(id integer,m6 macaddr,g6 macaddr GENERATED ALWAYS AS (trunc(m6)) STORED)", 0))
		run(fixture.Exec("INSERT INTO mac_ops_generated(id,m6) VALUES(1,'12:34:56:78:90:ab'),(2,NULL)", 2))
		macOperationsSavedAssertions(t, p, b.DB, b.Recorder)
		if !b.PG {
			macOperationsSavedControls(t, p, b)
		}
	})
}
