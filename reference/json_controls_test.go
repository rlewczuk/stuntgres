package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

const jsonControlDocument = " \t\n{\"z\":1.230e-5,\"a\":1,\"a\":2,\"apostrophe\":\"it's\",\"path\":\"\\\\x\"}\r\n "
const jsonControlDefault = ` {"escaped":"\u0000\uD800","k":1,"k":2} `

func TestReferenceJSONControlsAndStoredExpressions(t *testing.T) {
	p := newReferenceScenario(t)
	jsonReferenceControls(t, p)
}

// Native controls have no PostgreSQL equivalent. Replay the actual native dump
// on PostgreSQL, then assert independently listed rows and stored defaults.
func jsonReferenceControls(t *testing.T, p *referencePair) {
	t.Helper()
	nativeRun := func(s referenceStep) { referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, s) }
	nativeRun(referenceExec("CREATE TABLE json_saved(id INTEGER,v JSON DEFAULT json "+jsonReferenceQuote(jsonControlDefault)+",n JSON)", 0))
	nativeRun(referenceExec("CREATE TABLE json_empty(v JSON)", 0))
	nativeRun(referenceExec("INSERT INTO json_saved VALUES(1,$1,NULL)", 1, jsonControlDocument))
	nativeRun(referencePrepared(referenceExec("INSERT INTO json_saved(id,n) VALUES(2,'null')", 1)))
	want := [][]any{{int64(1), jsonControlDocument, nil}, {int64(2), jsonControlDefault, "null"}}
	assertNative := func() {
		t.Helper()
		nativeRun(referenceSelect("SELECT id,v,n FROM json_saved ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSON", "JSON"}, want))
		s := referenceSelect("SELECT v FROM json_empty", []string{"v"}, []string{"JSON"}, nil)
		s.checkMetadata = func(t *testing.T, rows *sql.Rows) { jsonReferenceMetadata(t, rows, false, true) }
		nativeRun(s)
	}
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		held, err := raw.(driver.QueryerContext).QueryContext(p.ctx, "SELECT v,n FROM json_saved ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer held.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		assertNative()
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
			return err
		}
		if redump.String() != dump.String() {
			t.Fatalf("nondeterministic JSON dump\n%s\n%s", dump.String(), redump.String())
		}
		if p.postgres != nil {
			if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
				return err
			}
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT id,v,n FROM json_saved ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSON", "JSON"}, want))
		}
		// A late codec failure must not publish even the preceding valid staged row.
		err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE r(v JSON); INSERT INTO r VALUES('null'),('bad')"))
		referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type json", Detail: `Token "bad" is invalid.`, Context: "JSON data, line 1: bad", Position: "55"})
		assertNative()
		var after bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
			return err
		}
		if after.String() != dump.String() {
			t.Fatal("failed Restore changed catalog or rows")
		}
		for _, backend := range p.backends() {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referencePrepared(referenceExec("INSERT INTO json_saved(id,n) VALUES(3,NULL)", 1)))
			run(referenceSelect("SELECT v FROM json_saved WHERE id=3", []string{"v"}, []string{"JSON"}, [][]any{{jsonControlDefault}}))
		}
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE json_saved(v TEXT); INSERT INTO json_saved VALUES('new')")); err != nil {
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
	nativeRun(referenceSelect("SELECT v FROM json_saved", []string{"v"}, []string{"TEXT"}, [][]any{{"new"}}))
}
