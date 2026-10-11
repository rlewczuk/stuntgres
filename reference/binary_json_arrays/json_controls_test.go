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

const jsonControlDocument = " \t\n{\"z\":1.230e-5,\"a\":1,\"a\":2,\"apostrophe\":\"it's\",\"path\":\"\\\\x\"}\r\n "
const jsonControlDefault = ` {"escaped":"\u0000\uD800","k":1,"k":2} `

func TestReferenceJSONControlsAndStoredExpressions(t *testing.T) {
	p := fixture.NewScenario(t)
	jsonReferenceControls(t, p)
}

// Native controls have no PostgreSQL equivalent. Replay the actual native dump
// on PostgreSQL, then assert independently listed rows and stored defaults.
func jsonReferenceControls(t *testing.T, p *fixture.Pair) {
	t.Helper()
	nativeRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s) }
	nativeRun(fixture.Exec("CREATE TABLE json_saved(id INTEGER,v JSON DEFAULT json "+jsonReferenceQuote(jsonControlDefault)+",n JSON)", 0))
	nativeRun(fixture.Exec("CREATE TABLE json_empty(v JSON)", 0))
	nativeRun(fixture.Exec("INSERT INTO json_saved VALUES(1,$1,NULL)", 1, jsonControlDocument))
	nativeRun(fixture.Prepared(fixture.Exec("INSERT INTO json_saved(id,n) VALUES(2,'null')", 1)))
	want := [][]any{{int64(1), jsonControlDocument, nil}, {int64(2), jsonControlDefault, "null"}}
	assertNative := func() {
		t.Helper()
		nativeRun(fixture.Select("SELECT id,v,n FROM json_saved ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSON", "JSON"}, want))
		s := fixture.Select("SELECT v FROM json_empty", []string{"v"}, []string{"JSON"}, nil)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { jsonReferenceMetadata(t, rows, false, true) }
		nativeRun(s)
	}
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		held, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT v,n FROM json_saved ORDER BY id", nil)
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
		assertNative()
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			return err
		}
		if redump.String() != dump.String() {
			t.Fatalf("nondeterministic JSON dump\n%s\n%s", dump.String(), redump.String())
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				return err
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT id,v,n FROM json_saved ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSON", "JSON"}, want))
		}
		// A late codec failure must not publish even the preceding valid staged row.
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE r(v JSON); INSERT INTO r VALUES('null'),('bad')"))
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type json", Detail: `Token "bad" is invalid.`, Context: "JSON data, line 1: bad", Position: "55"})
		assertNative()
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			return err
		}
		if after.String() != dump.String() {
			t.Fatal("failed Restore changed catalog or rows")
		}
		for _, backend := range p.Backends() {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Prepared(fixture.Exec("INSERT INTO json_saved(id,n) VALUES(3,NULL)", 1)))
			run(fixture.Select("SELECT v FROM json_saved WHERE id=3", []string{"v"}, []string{"JSON"}, [][]any{{jsonControlDefault}}))
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE json_saved(v TEXT); INSERT INTO json_saved VALUES('new')")); err != nil {
			return err
		}
		metadata := held.(driver.RowsColumnTypeDatabaseTypeName)
		if metadata.ColumnTypeDatabaseTypeName(0) != "JSON" || metadata.ColumnTypeDatabaseTypeName(1) != "JSON" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
			t.Fatal("retained JSON metadata changed")
		}
		dst := make([]driver.Value, 2)
		for _, row := range [][]driver.Value{{jsonControlDocument, nil}, {jsonControlDefault, "null"}} {
			if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst, row) {
				t.Fatalf("retained row=%#v want=%#v err=%v", dst, row, err)
			}
		}
		if err := held.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		if metadata.ColumnTypeDatabaseTypeName(0) != "JSON" {
			t.Fatal("EOF metadata changed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	nativeRun(fixture.Select("SELECT v FROM json_saved", []string{"v"}, []string{"TEXT"}, [][]any{{"new"}}))
}
