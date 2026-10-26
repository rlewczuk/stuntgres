package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func internalCharControlRows(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, table string, want [][]any) {
	t.Helper()
	s := internalCharSelect("SELECT v FROM "+table+" ORDER BY id", want)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { internalCharMetadata(t, rows, b.PG, []bool{true}) }
	fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
}

func internalCharNativeRoundTrip(t *testing.T, p *fixture.Pair, b fixture.Backend, table string, want [][]any) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector("internal-char-destination")
	if err != nil {
		t.Fatal(err)
	}
	destination := sql.OpenDB(connector)
	defer destination.Close()
	if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	wrapped, recorder := fixture.RecordedNativePool(t, connector.(*stuntgres.Connector))
	copyBackend := b
	copyBackend.DB, copyBackend.Recorder = wrapped, recorder
	internalCharControlRows(t, p, copyBackend, wrapped, table, want)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, destination, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatalf("CHAR redump differs:\n%s\n%s", dump.String(), redump.String())
	}
	// Restore is staged: a later range failure must not publish the replacement
	// or lose the independently restored destination's descriptors and rows.
	script := `CREATE TABLE internal_char_replacement(v "char"); INSERT INTO internal_char_replacement VALUES('r'); INSERT INTO internal_char_replacement VALUES(128::int4::"char")`
	err = stuntgres.Restore(p.Ctx, destination, strings.NewReader(script))
	d := fixture.ControlsReferenceDiagnostic("22003", `"char" out of range`)
	fixture.AssertDiagnostic(t, err, &d)
	internalCharControlRows(t, p, copyBackend, wrapped, table, want)
	fixture.RunStep(t, p.Ctx, wrapped, recorder, fixture.Select(`SELECT count(*) AS n FROM pg_class WHERE relname='internal_char_replacement'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	if p.Postgres != nil {
		tx, err := p.Postgres.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(p.Ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
		pg := b
		pg.PG, pg.DB, pg.Recorder = true, p.Postgres, p.PostgresRecord
		internalCharControlRows(t, p, pg, tx, table, want)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceInternalCharControls(t *testing.T) {
	internalCharScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE internal_char_saved(id integer,v pg_catalog."char")`, 0))
		run(fixture.Exec(`INSERT INTO internal_char_saved VALUES(1,'r'),(2,''),(3,NULL),(4,'\377'),(5,''''),(6,'\')`, 6))
		want := [][]any{{"r"}, {""}, {nil}, {`\377`}, {"'"}, {`\`}}
		internalCharControlRows(t, p, b, b.DB, "internal_char_saved", want)
		if !b.PG {
			internalCharNativeRoundTrip(t, p, b, "internal_char_saved", want)
		}
	})
}
