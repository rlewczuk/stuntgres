package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

type uuidReferenceFailingReader struct{ err error }

func (r uuidReferenceFailingReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceUUIDControlsAndRetainedSnapshot(t *testing.T) {
	p := newReferenceScenario(t)
	// Keep original direct setup and the existing Restore as distinct witnesses.
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE empty(v UUID)", 0),
		referenceExec("CREATE TABLE saved(v UUID NOT NULL,n UUID)", 0),
		referenceExec("INSERT INTO saved VALUES('00000000000000000000000000000000',NULL),('{A0EEBC999C0B4EF8BB6D6BB9BD380A11}','ffffffffffffffffffffffffffffffff')", 2),
		referenceSelect("SELECT * FROM saved ORDER BY v", []string{"v", "n"}, []string{"UUID", "UUID"}, [][]any{{uuidZ, nil}, {uuidA, uuidF}}),
		referenceSelect("SELECT * FROM empty", []string{"v"}, []string{"UUID"}, nil),
	} {
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, step)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	script := "CREATE TABLE empty(v UUID); CREATE TABLE saved(v UUID NOT NULL,n UUID); INSERT INTO saved VALUES('00000000000000000000000000000000',NULL),('{A0EEBC999C0B4EF8BB6D6BB9BD380A11}','ffffffffffffffffffffffffffffffff')"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		held, err := raw.(driver.QueryerContext).QueryContext(p.ctx, "SELECT * FROM saved ORDER BY v", nil)
		if err != nil {
			return err
		}
		defer held.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			return err
		}
		if _, err := p.memory.ExecContext(p.ctx, "INSERT INTO saved VALUES($1,NULL)", uuidF); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
			t.Fatal(redump.String(), err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(redump.Bytes())); err != nil {
			return err
		}
		if p.postgres != nil {
			if _, err := p.postgres.ExecContext(p.ctx, redump.String()); err != nil {
				return err
			}
		}
		assertSaved := func() {
			t.Helper()
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT * FROM saved ORDER BY v", []string{"v", "n"}, []string{"UUID", "UUID"}, [][]any{{uuidZ, nil}, {uuidA, uuidF}}))
			referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT * FROM empty", []string{"v"}, []string{"UUID"}, nil))
			rows, err := p.memory.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, p.memoryRecord), "SELECT * FROM saved")
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for i, typ := range types {
				n, known := typ.Nullable()
				if typ.Name() != []string{"v", "n"}[i] || typ.DatabaseTypeName() != "UUID" || typ.ScanType() != reflect.TypeFor[string]() || !known || n != (i == 1) {
					t.Fatal(typ)
				}
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
		assertSaved()
		for _, prefix := range []string{"", "/*é*/ "} {
			q := prefix + "CREATE TABLE replacement(v UUID); INSERT INTO replacement VALUES('00000000000000000000000000000000'),('bad')"
			position := "103"
			if prefix != "" {
				position = "109"
			}
			err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(q))
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`, Position: position})
			assertSaved()
		}
		canceled, cancel := context.WithCancel(p.ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		failure := errors.New("uuid restore source failure")
		if err := stuntgres.Restore(p.ctx, p.native, uuidReferenceFailingReader{failure}); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		assertSaved()
		err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(v UUID); INSERT INTO replacement VALUES($1)"))
		referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "parameters are not allowed in restore", Position: "66"})
		assertSaved()
		p.step(t, referenceSelect("SELECT * FROM saved ORDER BY v", []string{"v", "n"}, []string{"UUID", "UUID"}, [][]any{{uuidZ, nil}, {uuidA, uuidF}}))
		p.step(t, referenceSelect("SELECT * FROM empty", []string{"v"}, []string{"UUID"}, nil))
		p.step(t, referenceImplicitError("INSERT INTO saved VALUES(NULL,NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "saved" violates not-null constraint`, Detail: "Failing row contains (null, null).", Schema: "public", Table: "saved", Column: "v"}))
		p.step(t, referenceExec("ALTER TABLE saved DROP CONSTRAINT saved_v_not_null", 0))
		p.step(t, referenceExec("INSERT INTO saved VALUES(NULL,NULL)", 1))
		p.step(t, referenceSelect("SELECT * FROM saved WHERE v=$1", []string{"v", "n"}, []string{"UUID", "UUID"}, [][]any{{uuidA, uuidF}}, uuidA))
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE saved(v TEXT); INSERT INTO saved VALUES('new')")); err != nil {
			return err
		}
		metadata := held.(driver.RowsColumnTypeDatabaseTypeName)
		if metadata.ColumnTypeDatabaseTypeName(0) != "UUID" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
			t.Fatal("retained metadata changed")
		}
		dst := make([]driver.Value, 2)
		for _, want := range [][]driver.Value{{uuidZ, nil}, {uuidA, uuidF}} {
			if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
				t.Fatal(dst, want, err)
			}
		}
		if err := held.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		if metadata.ColumnTypeDatabaseTypeName(0) != "UUID" {
			t.Fatal("EOF metadata changed")
		}
		return held.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT v FROM saved", []string{"v"}, []string{"TEXT"}, [][]any{{"new"}}))
}
