package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func walNamesConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE wal_names_saved(id integer,lsn pg_lsn,label text DEFAULT pg_walfile_name('0/1000000'::pg_lsn),CONSTRAINT wal_names_label CHECK(label IS NULL OR (pg_split_walfile_name(label)).timeline_id=1))", 0))
	run(fixture.Exec("INSERT INTO wal_names_saved(id,lsn) VALUES(1,'0/FFFFFF'),(2,'0/1000000')", 2))
	run(fixture.Exec("INSERT INTO wal_names_saved(id,lsn) VALUES(3,NULL)", 1))
	run(fixture.Exec("CREATE VIEW wal_names_view AS SELECT id,pg_walfile_name(lsn) AS file_name,(pg_walfile_name_offset(lsn)).file_offset AS file_offset,(pg_split_walfile_name(pg_walfile_name(lsn))).segment_number AS segment_number,(pg_split_walfile_name(pg_walfile_name(lsn))).timeline_id AS timeline_id FROM wal_names_saved", 0))
	run(fixture.Exec("CREATE TABLE wal_names_materialized(n numeric,t bigint,label text)", 0))
	run(fixture.Exec("INSERT INTO wal_names_materialized SELECT (pg_split_walfile_name('FFFFFFFFFFFFFFFFFFFFFFFF')).segment_number,(pg_split_walfile_name('FFFFFFFFFFFFFFFFFFFFFFFF')).timeline_id,pg_walfile_name('FFFFFFFF/FFFFFFFF')", 1))
	walNamesConsumerRows(t, p, b.DB, b.Recorder, false)
}
func walNamesConsumerRows(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, extra bool) {
	t.Helper()
	want := [][]any{{int64(1), "000000010000000000000000", int64(16777215), "0", int64(1)}, {int64(2), "000000010000000000000001", int64(0), "1", int64(1)}, {int64(3), nil, nil, nil, nil}}
	if extra {
		want = append(want, []any{int64(4), "000000010000000100000000", int64(0), "256", int64(1)})
	}
	for _, s := range []fixture.Step{
		fixture.Select("SELECT * FROM wal_names_view ORDER BY id", []string{"id", "file_name", "file_offset", "segment_number", "timeline_id"}, []string{"INT4", "TEXT", "INT4", "NUMERIC", "INT8"}, want),
		fixture.Select("SELECT n,t,label FROM wal_names_materialized", []string{"n", "t", "label"}, []string{"NUMERIC", "INT8", "TEXT"}, [][]any{{"1103806594815", int64(4294967295), "00000001FFFFFFFF000000FF"}}),
		fixture.Select("SELECT label FROM wal_names_saved WHERE id=2", []string{"label"}, []string{"TEXT"}, [][]any{{"000000010000000000000001"}}),
	} {
		fixture.RunStep(t, p.Ctx, x, r, s)
		fixture.RunStep(t, p.Ctx, x, r, fixture.Prepared(s))
	}
}
func walNamesNativeConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
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
	walNamesConsumerRows(t, p, b.DB, b.Recorder, false)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatal("non-deterministic source WAL consumers", dump.String(), redump.String())
	}
	connector, err := stuntgres.NewDriver().OpenConnector("wal-names/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	control := sql.OpenDB(connector)
	defer control.Close()
	destination, recorder := fixture.RecordedNativePool(t, connector)
	if err := stuntgres.Restore(p.Ctx, control, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	walNamesConsumerRows(t, p, destination, recorder, false)
	redump.Reset()
	if err := stuntgres.Dump(p.Ctx, control, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatal("destination WAL replay changed recipes", dump.String(), redump.String())
	}
	// Destroy source state before demanding destination expressions and defaults.
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Exec("INSERT INTO wal_names_saved(id,lsn) VALUES(4,'1/0')", 1))
	walNamesConsumerRows(t, p, destination, recorder, true)
	fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Select("SELECT label FROM wal_names_saved WHERE id=4", []string{"label"}, []string{"TEXT"}, [][]any{{"000000010000000000000001"}}))
	// A genuine malformed feature expression in staged INSERT must leave the
	// destination's existing catalog and all already committed consumers intact.
	script := "CREATE TABLE wal_names_rejected(n numeric); INSERT INTO wal_names_rejected VALUES((pg_split_walfile_name('bad')).segment_number);"
	err = stuntgres.Restore(p.Ctx, control, strings.NewReader(script))
	d := walNamesInvalid("bad")
	fixture.AssertDiagnostic(t, err, &d)
	walNamesConsumerRows(t, p, destination, recorder, true)
	fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='wal_names_rejected'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
}
func TestReferenceWALNamesControls(t *testing.T) {
	walNamesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		walNamesConsumers(t, p, b)
		if !b.PG {
			walNamesNativeConsumers(t, p, b)
		}
	})
}

func TestReferenceWALNamesFeatureSmoke(t *testing.T) {
	walNamesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Select("SHOW wal_segment_size", []string{"wal_segment_size"}, []string{"TEXT"}, [][]any{{"16MB"}}))
		if b.PG {
			run(fixture.Select("SHOW server_version_num", []string{"server_version_num"}, []string{"TEXT"}, [][]any{{"180006"}}))
		}
		run(walNamesSelect(b.PG, "SELECT pg_walfile_name('0/1000000'::pg_lsn) AS file_name,(pg_walfile_name_offset('0/1000000'::pg_lsn)).file_offset AS file_offset", []string{"file_name", "file_offset"}, []string{"TEXT", "INT4"}, [][]any{{"000000010000000000000001", int64(0)}}))
		q := "SELECT segment_number,timeline_id FROM pg_split_walfile_name($1)"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, walNamesSelect(b.PG, q, []string{"segment_number", "timeline_id"}, []string{"NUMERIC", "INT8"}, [][]any{{"431", int64(4294967295)}}, "ffffffFF00000001000000af"))
		s := fixture.ErrorDiagnostic(q, walNamesInvalid("bad"), "bad")
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, walNamesSelect(b.PG, q, []string{"segment_number", "timeline_id"}, []string{"NUMERIC", "INT8"}, [][]any{{"1", int64(1)}}, "000000010000000000000001"))
		stmt.Close()
		run(walNamesSelect(b.PG, "SELECT * FROM pg_walfile_name_offset(NULL::pg_lsn)", []string{"file_name", "file_offset"}, []string{"TEXT", "INT4"}, [][]any{{nil, nil}}))
		walNamesConsumers(t, p, b)
		if !b.PG {
			walNamesNativeConsumers(t, p, b)
		}
	})
}
