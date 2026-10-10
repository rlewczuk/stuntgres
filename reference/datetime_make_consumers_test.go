package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func datetimeMakeStored(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE dt_make_stored(id integer,y integer,m integer,d integer,iv interval DEFAULT make_interval(secs=>1.5,days=>2),stamp timestamp DEFAULT make_timestamp(2000,1,1,0,0,0),constructed date GENERATED ALWAYS AS (make_date(y,m,d)) STORED,CHECK (make_date(y,m,d)>=make_date(1,1,1)))", 0))
	run(referenceExec("CREATE INDEX dt_make_key ON dt_make_stored ((make_date(y,m,d)))", 0))
	run(referenceExec("INSERT INTO dt_make_stored(id,y,m,d) VALUES(1,2000,2,29),(2,2013,7,15)", 2))
	run(referenceExec("CREATE VIEW dt_make_view AS SELECT id,make_date(day=>d,month=>m,year=>y) AS date,make_interval(secs=>'1.5',days=>d) AS interval FROM dt_make_stored", 0))
	run(referenceSelect("SELECT id,constructed::text AS date,iv AS interval,stamp::text AS stamp FROM dt_make_stored ORDER BY id", []string{"id", "date", "interval", "stamp"}, []string{"INT4", "TEXT", "INTERVAL", "TEXT"}, [][]any{{int64(1), "2000-02-29", "2 days 00:00:01.5", "2000-01-01 00:00:00"}, {int64(2), "2013-07-15", "2 days 00:00:01.5", "2000-01-01 00:00:00"}}))
	run(referenceSelect("SELECT make_date(y,m,d)::text AS date,count(*) AS count FROM dt_make_stored GROUP BY make_date(y,m,d) ORDER BY make_date(y,m,d)", []string{"date", "count"}, []string{"TEXT", "INT8"}, [][]any{{"2000-02-29", int64(1)}, {"2013-07-15", int64(1)}}))
	run(referenceSelect("SELECT a.id FROM dt_make_stored a JOIN dt_make_stored b ON make_date(a.y,a.m,a.d)=make_date(b.y,b.m,b.d) ORDER BY a.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	run(referenceSelect("UPDATE dt_make_stored SET d=28 WHERE id=1 RETURNING make_date(y,m,d)::text AS date", []string{"date"}, []string{"TEXT"}, [][]any{{"2000-02-28"}}))
	run(referenceExec("ALTER TABLE dt_make_stored RENAME COLUMN d TO day", 0))
	want := referenceSelect("SELECT id,date::text AS date,interval FROM dt_make_view ORDER BY id", []string{"id", "date", "interval"}, []string{"INT4", "TEXT", "INTERVAL"}, [][]any{{int64(1), "2000-02-28", "28 days 00:00:01.5"}, {int64(2), "2013-07-15", "15 days 00:00:01.5"}})
	run(want)
	if b.pg {
		return
	}
	var before, after bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
		t.Fatal(err)
	}
	retained, err := p.native.QueryContext(p.ctx, want.query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	metadata, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(before.Bytes())); err != nil {
		t.Fatal(err)
	}
	for i, expected := range want.rows {
		if !retained.Next() {
			t.Fatalf("retained row %d: %v", i, retained.Err())
		}
		var id int64
		var date, interval string
		if err := retained.Scan(&id, &date, &interval); err != nil {
			t.Fatal(err)
		}
		if id != expected[0] || date != expected[1] || interval != expected[2] {
			t.Fatalf("retained row %d got %d %s %s want %#v", i, id, date, interval, expected)
		}
	}
	if retained.Next() || retained.Err() != nil {
		t.Fatalf("retained tail: %v", retained.Err())
	}
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 3 || metadata[0].DatabaseTypeName() != "INT4" || metadata[1].DatabaseTypeName() != "TEXT" || metadata[2].DatabaseTypeName() != "INTERVAL" {
		t.Fatalf("retained metadata %#v", metadata)
	}
	run(want)
	if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatalf("noncanonical redump\nbefore %s\nafter %s", before.String(), after.String())
	}
	bad := append(append([]byte{}, before.Bytes()...), []byte("\nINSERT INTO dt_make_stored(id,y,m,day) VALUES(9,2013,2,29);")...)
	err = stuntgres.Restore(p.ctx, p.native, bytes.NewReader(bad))
	if err == nil {
		t.Fatal("invalid staged restore succeeded")
	}
	referenceAssertDiagnostic(t, err, func() *referenceDiagnostic {
		d := datetimeMakeError("22008", "date field value out of range: 2013-02-29")
		return &d
	}())
	run(want)
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(before.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore: %v", err)
	}
	run(want)
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, before.String()); err != nil {
			t.Fatalf("actual emitted SQL replay: %v\n%s", err, before.String())
		}
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, want)
		if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceDatetimeMakeStoredConsumers(t *testing.T) {
	datetimeMakeScenario(t, datetimeMakeStored)
}
