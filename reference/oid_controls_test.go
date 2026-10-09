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

func TestReferenceOIDDumpReplay(t *testing.T) {
	p := newReferenceScenario(t)
	for _, q := range []string{`CREATE TABLE "MixedOID"(id INT,v OID DEFAULT '4294967295'::oid)`, `INSERT INTO "MixedOID" VALUES(1,'0'),(2,'2147483648'),(3,'4294967295'),(4,NULL)`, "CREATE TABLE empty(v OID)", "CREATE TABLE defaults(v OID DEFAULT NULL::oid,b BOOL DEFAULT ('4294967295'::oid > '2147483648'::oid))", "CREATE TABLE o(v OID DEFAULT '4294967295'::oid)", "INSERT INTO o VALUES('0'),('2147483648'),('4294967295'),(NULL)"} {
		count := int64(0)
		if strings.HasPrefix(q, "INSERT") {
			count = 4
		}
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(q, count))
	}
	want := [][]any{{int64(1), int64(0)}, {int64(2), int64(2147483648)}, {int64(3), int64(4294967295)}, {int64(4), nil}}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "OID") || !strings.Contains(dump.String(), "'4294967295'") {
		t.Fatal(dump.String())
	}
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		cursor, err := raw.(driver.QueryerContext).QueryContext(p.ctx, `SELECT * FROM "MixedOID" ORDER BY id`, nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "OID" || cursor.(driver.RowsColumnTypeScanType).ColumnTypeScanType(1) != reflect.TypeFor[int64]() {
			t.Fatal("retained metadata")
		}
		values := make([]driver.Value, 2)
		for _, row := range want {
			if err := cursor.Next(values); err != nil || !reflect.DeepEqual([]any{values[0], values[1]}, row) {
				t.Fatal(values, row, err)
			}
		}
		if err := cursor.Next(values); err != io.EOF {
			t.Fatal(err)
		}
		return cursor.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.backends() {
		oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect(`SELECT * FROM "MixedOID" ORDER BY id`, []string{"id", "v"}, []string{"INT4", "OID"}, want))
		oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect("SELECT v FROM empty", []string{"v"}, []string{"OID"}, nil))
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v::int8 FROM o ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}, {int64(2147483648)}, {int64(4294967295)}, {nil}}))
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO defaults DEFAULT VALUES", 1))
		oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect("SELECT * FROM defaults", []string{"v", "b"}, []string{"OID", "BOOL"}, [][]any{{nil, true}}))
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(`INSERT INTO "MixedOID"(id) VALUES(5)`, 1))
		oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect(`SELECT v FROM "MixedOID" WHERE id=5`, []string{"v"}, []string{"OID"}, [][]any{{int64(4294967295)}}))
	}
	for _, script := range []string{"CREATE TABLE replacement(v OID); INSERT INTO replacement VALUES('1'); INSERT INTO replacement VALUES('4294967296')", "CREATE TABLE replacement(v OID); INSERT INTO replacement VALUES('1'); INSERT INTO replacement VALUES((-1)::int8::oid)"} {
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
			t.Fatal("bad restore accepted")
		}
		for _, backend := range p.backends() {
			oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect(`SELECT v FROM "MixedOID" WHERE id=5`, []string{"v"}, []string{"OID"}, [][]any{{int64(4294967295)}}))
		}
	}
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, backend := range p.backends() {
		oidReferenceRows(t, p.ctx, backend, backend.db, referenceSelect(`SELECT v FROM "MixedOID" WHERE id=5`, []string{"v"}, []string{"OID"}, [][]any{{int64(4294967295)}}))
	}
}
