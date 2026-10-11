package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

const jsonbControlInput = ` {"z":1.230e-5,"a":1,"a":2,"apostrophe":"it's","path":"\\x"} `
const jsonbControlOutput = `{"a": 2, "z": 0.00001230, "path": "\\x", "apostrophe": "it's"}`
const jsonbControlDefault = `{"k":1,"k":2,"u":"\uD83D\uDE04"}`
const jsonbControlDefaultOutput = `{"k": 2, "u": "😄"}`

func TestReferenceJSONBControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		jsonbReferenceControlSeed(t, p, backend)
	}
	jsonbReferenceNativeControls(t, p, false)
}
func jsonbReferenceControlSeed(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
	run(fixture.Exec("CREATE TABLE jb_saved(id INT,v JSONB DEFAULT jsonb "+jsonReferenceQuote(jsonbControlDefault)+",n JSONB DEFAULT CAST(NULL AS JSONB))", 0))
	run(fixture.Exec("CREATE TABLE jb_empty(v JSONB)", 0))
	run(fixture.Exec("INSERT INTO jb_saved VALUES(1,$1,NULL)", 1, jsonbControlInput))
	run(fixture.Prepared(fixture.Exec("INSERT INTO jb_saved(id,n) VALUES(2,'null')", 1)))
	jsonbReferenceControlRows(t, p, backend)
}
func jsonbReferenceControlRows(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT id,v,n FROM jb_saved ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSONB", "JSONB"}, [][]any{{int64(1), jsonbControlOutput, nil}, {int64(2), jsonbControlDefaultOutput, "null"}}))
	s := fixture.Select("SELECT v FROM jb_empty", []string{"v"}, []string{"JSONB"}, nil)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { jsonbReferenceMetadata(t, rows, backend.PG, true) }
	fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
}
func jsonbReferenceNativeControls(t *testing.T, p *fixture.Pair, smoke bool) {
	t.Helper()
	native := p.Backends()[0]
	assertRestoredIndexes := func(backend fixture.Backend) {
		if !smoke {
			return
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Select("SELECT id FROM jb_unique WHERE v=$1::jsonb", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}, `{"x":[1.00]}`)))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT id FROM jb_unique ORDER BY v,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
	}
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		held, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT v,n FROM jb_saved ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer held.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		jsonbReferenceControlRows(t, p, native)
		assertRestoredIndexes(native)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			return err
		}
		if redump.String() != dump.String() {
			t.Fatal("nondeterministic JSONB dump")
		}
		if p.Postgres != nil {
			// The smoke dump includes its comparison/index/atomicity relations too.
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				return err
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				return err
			}
			jsonbReferenceControlRows(t, p, p.Backends()[1])
			assertRestoredIndexes(p.Backends()[1])
		}
		for _, tc := range []struct {
			input string
			want  fixture.Diagnostic
		}{
			{"bad", jsonReferenceDiagnostic(jsonReferenceInvalid{"bad", "bad", `Token "bad" is invalid.`, "JSON data, line 1: bad"})},
			{"1e131072", fixture.ControlsReferenceDiagnostic("22003", "value overflows numeric format")},
			{`"\u0000"`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P05", Message: "unsupported Unicode escape sequence", Detail: `\u0000 cannot be converted to text.`, Context: `JSON data, line 1: "\u0000...`}},
		} {
			prefix := "CREATE TABLE staged(v JSONB); INSERT INTO staged VALUES('null'),("
			script := prefix + jsonReferenceQuote(tc.input) + ")"
			tc.want.Position = fixture.Position(len(prefix) + 1)
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
			fixture.AssertDiagnostic(t, err, &tc.want)
			jsonbReferenceControlRows(t, p, native)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				return err
			}
			if after.String() != dump.String() {
				t.Fatal("failed restore changed catalog or rows")
			}
		}
		for _, backend := range p.Backends() {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO jb_saved(id) VALUES(3)", 1)))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v,n FROM jb_saved WHERE id=3", []string{"v", "n"}, []string{"JSONB", "JSONB"}, [][]any{{jsonbControlDefaultOutput, nil}}))
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE jb_saved(v TEXT); INSERT INTO jb_saved VALUES('new')")); err != nil {
			return err
		}
		types := held.(driver.RowsColumnTypeDatabaseTypeName)
		if types.ColumnTypeDatabaseTypeName(0) != "JSONB" || types.ColumnTypeDatabaseTypeName(1) != "JSONB" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
			t.Fatal("retained JSONB metadata changed")
		}
		nullable := held.(driver.RowsColumnTypeNullable)
		if n, known := nullable.ColumnTypeNullable(0); !known || !n {
			t.Fatalf("retained nullable %v,%v", n, known)
		}
		dst := make([]driver.Value, 2)
		for _, row := range [][]driver.Value{{jsonbControlOutput, nil}, {jsonbControlDefaultOutput, "null"}} {
			if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst, row) {
				t.Fatalf("retained row %#v want %#v err=%v", dst, row, err)
			}
		}
		if err := held.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		if types.ColumnTypeDatabaseTypeName(0) != "JSONB" {
			t.Fatal("EOF metadata changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT v FROM jb_saved", []string{"v"}, []string{"TEXT"}, [][]any{{"new"}}))
}
