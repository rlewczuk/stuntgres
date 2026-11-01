package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgTrgmRetainedMetadata(t *testing.T, rows *sql.Rows, check fixture.Step) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, check.Columns) {
		t.Fatal("retained names", names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(check.Types) {
		t.Fatal("retained descriptors", meta, err)
	}
	for i, m := range meta {
		if m.DatabaseTypeName() != check.Types[i] {
			t.Fatalf("retained %d type=%s want=%s", i, m.DatabaseTypeName(), check.Types[i])
		}
	}
}

func pgTrgmControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step, namedOwner bool) {
	t.Helper()
	run := pgTrgmRun(t, p, b, c)
	// Exercise detached provider declarations and stored expressions through
	// installation-cohort replay, not just the stock released SQL spellings.
	run(fixture.Exec("CREATE SCHEMA trgm_moved", 0))
	run(fixture.Exec("ALTER FUNCTION trgm_addon.show_trgm(text) RENAME TO original_trigrams", 0))
	run(fixture.Exec("ALTER FUNCTION trgm_addon.original_trigrams(text) SET SCHEMA trgm_moved", 0))
	run(fixture.Exec("ALTER EXTENSION pg_trgm DROP FUNCTION trgm_moved.original_trigrams(text)", 0))
	run(check)
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	q := "SELECT trgm_addon.similarity($1,$2),$1::text OPERATOR(trgm_addon.%) $2::text AS matches"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	frame := pgTrgmSelect(b, q, []string{"similarity", "matches"}, []string{"FLOAT4", "BOOL"}, [][]any{{float64(0.25), false}}, "a", "aa")
	fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
	// Capture real recorded Rows before controls. Later SQL must not replace
	// their old metadata, row generation, immutable arrays or close evidence.
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
	retained, err := c.QueryContext(ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	b.Recorder.Mu.Lock()
	operation := b.Recorder.Operations[len(b.Recorder.Operations)-1]
	b.Recorder.Mu.Unlock()
	pgTrgmRetainedMetadata(t, retained, check)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='pg_trgm'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	run(extensionMissing("SELECT similarity('cat','cat')", "similarity", "unknown, unknown"))
	pgTrgmLoaded(run, true)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	pgTrgmRetainedMetadata(t, retained, check)
	fixture.ScalarRows(t, false, retained, check.Columns, check.Types, check.Rows, false)
	b.Recorder.Mu.Lock()
	closed, closeErr := operation.Closed, operation.CloseErr
	captured := make([][]any, len(operation.Rows))
	for i, row := range operation.Rows {
		captured[i] = make([]any, len(row))
		for j, value := range row {
			captured[i][j] = fixture.Normalize(value)
		}
	}
	b.Recorder.Mu.Unlock()
	if !closed || closeErr != nil || !fixture.RecordedRowsMatch(captured, check) {
		t.Fatal("retained recorded rows/closure", captured, closed, closeErr)
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
	run(check)
	run(fixture.Prepared(check))
	if fixture.ExtensionDump(t, p.Ctx, p.Native) != dump {
		t.Fatal("deterministic provider replay changed")
	}
	// Physical settings are neither serialized by Dump nor reset by catalog controls.
	run(fixture.Exec("SET pg_trgm.similarity_threshold='0.2'", 0))
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	pgTrgmLoaded(run, true)
	pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.2")
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.2")
	changed := frame
	changed.Rows = [][]any{{float64(0.25), true}}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, changed)
	run(fixture.Exec("RESET pg_trgm.similarity_threshold", 0))
	run(check)
	extensionControlsFailures(t, p, c, b.Recorder, "pg_trgm", dump, check)
	failure := errors.New("pg_trgm genuine reader failure")
	if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
		t.Fatal("reader failure", err)
	}
	cancelled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(cancelled, p.Native, strings.NewReader(dump)); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled restore", err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nSELECT pg_trgm_later_invalid();")); err == nil {
		t.Fatal("later-invalid replay accepted")
	}
	run(check)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
	if fixture.ExtensionDump(t, p.Ctx, p.Native) != dump {
		t.Fatal("failed restore mutated target")
	}
	pgTrgmIndependentDestination(t, p, b, dump, check, namedOwner)
	// Re-evaluate restored generated/default/CHECK/view recipes, not snapshots.
	run(fixture.Exec("UPDATE trgm_source SET v='cat' WHERE id=2", 1))
	run(fixture.Exec("INSERT INTO trgm_source(id,v) VALUES(4,'cat')", 1))
	run(fixture.Select("SELECT id,score,ordinary,word_score,strict_score,trigrams FROM trgm_source WHERE id>=2 ORDER BY id", []string{"id", "score", "ordinary", "word_score", "strict_score", "trigrams"}, []string{"INT4", "FLOAT4", "FLOAT4", "FLOAT4", "FLOAT4", "_TEXT"}, [][]any{{int64(2), float64(0.25), float64(1), float64(1), float64(1), pgTrgmArray("  c", " ca", "at ", "cat")}, {int64(3), float64(0.25), nil, nil, nil, nil}, {int64(4), float64(0.25), float64(1), float64(1), float64(1), pgTrgmArray("  c", " ca", "at ", "cat")}}))
}

func pgTrgmIndependentDestination(t *testing.T, p *fixture.Pair, b fixture.Backend, dump string, check fixture.Step, namedOwner bool) {
	t.Helper()
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("pgtrgm_destination")
	if err != nil {
		t.Fatal(err)
	}
	raw := sql.OpenDB(connector)
	defer raw.Close()
	db, recorder := fixture.RecordedNativePool(t, connector)
	targetBackend := b
	targetBackend.Recorder = recorder
	run := pgTrgmRun(t, p, targetBackend, db)
	// Advance actual allocators through ordinary declarations. Named logical
	// principals are recreated in this independent root, not copied numeric IDs.
	run(fixture.Exec("CREATE ROLE trgm_allocator", 0))
	run(fixture.Exec("CREATE USER trgm_owner NOSUPERUSER", 0))
	run(fixture.Exec("CREATE TABLE trgm_destination_sentinel(id int)", 0))
	run(fixture.Exec("INSERT INTO trgm_destination_sentinel VALUES(91)", 1))
	if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump)); err != nil {
		t.Fatal("independent destination", err)
	}
	run(check)
	run(fixture.Prepared(check))
	run(fixture.Select("SELECT a,w,s FROM trgm_literals", []string{"a", "w", "s"}, []string{"_TEXT", "FLOAT4", "FLOAT4"}, [][]any{{pgTrgmArray("  c", " ca", "at ", "cat"), float64(0.5), float64(0.25)}}))
	run(pgTrgmScalar(targetBackend, "SELECT trgm_moved.original_trigrams('cat')", "original_trigrams", "_TEXT", pgTrgmArray("  c", " ca", "at ", "cat")))
	pgTrgmSettingValue(run, pgTrgmSettings[0].name, "0.3")
	if fixture.ExtensionDump(t, p.Ctx, raw) != dump {
		t.Fatal("independent destination changed symbolic replay")
	}
	// A live-signature collision must reject atomically, retaining executable
	// destination rows and all old resources.
	before := fixture.ExtensionDump(t, p.Ctx, raw)
	if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump+"\nCREATE FUNCTION trgm_addon.similarity(text,text) RETURNS text LANGUAGE SQL AS 'SELECT $1';")); err == nil {
		t.Fatal("incompatible signature accepted")
	}
	run(check)
	if fixture.ExtensionDump(t, p.Ctx, raw) != before {
		t.Fatal("collision replaced destination")
	}
	missingConnector, err := stuntgres.NewDriver().OpenConnector("pgtrgm_missing_owner")
	if err != nil {
		t.Fatal(err)
	}
	missingRaw := sql.OpenDB(missingConnector)
	defer missingRaw.Close()
	missing, missingRecorder := fixture.RecordedNativePool(t, missingConnector)
	missingBackend := b
	missingBackend.Recorder = missingRecorder
	missingRun := pgTrgmRun(t, p, missingBackend, missing)
	missingRun(fixture.Exec("CREATE TABLE trgm_sentinel(id int)", 0))
	missingRun(fixture.Exec("INSERT INTO trgm_sentinel VALUES(91)", 1))
	missingBefore := fixture.ExtensionDump(t, p.Ctx, missingRaw)
	if namedOwner {
		if err := stuntgres.Restore(p.Ctx, missingRaw, strings.NewReader(dump)); err == nil {
			t.Fatal("missing named owner accepted")
		}
		missingRun(fixture.Select("SELECT id FROM trgm_sentinel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(91)}}))
		if fixture.ExtensionDump(t, p.Ctx, missingRaw) != missingBefore {
			t.Fatal("missing-owner rejection mutated target")
		}
		missingRun(fixture.Exec("CREATE USER trgm_owner NOSUPERUSER", 0))
		beforeReplacement := fixture.ExtensionDump(t, p.Ctx, missingRaw)
		reader := &extensionMutationReader{reader: strings.NewReader(dump), mutate: func() error {
			for _, q := range []string{"DROP USER trgm_owner", "CREATE USER trgm_owner NOSUPERUSER"} {
				fixture.RunStep(t, p.Ctx, missing, missingRecorder, fixture.Exec(q, 0))
			}
			return nil
		}}
		if err := stuntgres.Restore(p.Ctx, missingRaw, reader); err == nil {
			t.Fatal("drop/recreated owner authority accepted")
		}
		missingRun(fixture.Select("SELECT id FROM trgm_sentinel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(91)}}))
		if fixture.ExtensionDump(t, p.Ctx, missingRaw) != beforeReplacement {
			t.Fatal("owner replacement published staged catalog")
		}
	}
}
