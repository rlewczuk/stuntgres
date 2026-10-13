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
	"stuntgres/reference/fixture"
	"testing"
)

func xidReferencePersistentSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE xid_persist(id INT,x XID DEFAULT '4294967295'::xid,y XID8 DEFAULT '18446744073709551615'::xid8,c CID DEFAULT NULL::cid,CHECK(y >= '0'::xid8),CHECK(c = '0'::cid))", 0),
		fixture.Exec("INSERT INTO xid_persist VALUES(1,'0','0','0'),(2,'4294967295','18446744073709551615','0'),(3,NULL,NULL,NULL)", 3),
		fixture.Exec("CREATE TABLE xid_empty(x XID,y XID8,c CID)", 0),
		fixture.Exec("CREATE VIEW xid_view AS SELECT '18446744073709551615'::xid8 AS y,('4294967297'::xid8)::xid AS x,NULL::cid AS c", 0),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func xidReferencePersistentRows(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT id,x,y,c FROM xid_persist ORDER BY id", []string{"id", "x", "y", "c"}, []string{"INT4", "XID", "XID8", "CID"}, [][]any{{int64(1), "0", "0", "0"}, {int64(2), "4294967295", "18446744073709551615", "0"}, {int64(3), nil, nil, nil}}))
	xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT x,y,c FROM xid_empty", []string{"x", "y", "c"}, []string{"XID", "XID8", "CID"}, nil))
	xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT y,x,c FROM xid_view", []string{"y", "x", "c"}, []string{"XID8", "XID", "CID"}, [][]any{{"18446744073709551615", "1", nil}}))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO xid_persist(id) VALUES(4)", 1))
	xidReferenceRoutes(t, p.Ctx, b, fixture.Select("SELECT x,y,c FROM xid_persist WHERE id=4", []string{"x", "y", "c"}, []string{"XID", "XID8", "CID"}, [][]any{{"4294967295", "18446744073709551615", nil}}))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError("INSERT INTO xid_persist VALUES(5,'0','0','1')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "xid_persist" violates check constraint "xid_persist_c_check"`, Detail: "Failing row contains (5, 0, 0, 1).", Schema: "public", Table: "xid_persist", Constraint: "xid_persist_c_check"}))
}

func TestReferenceXIDDumpReplay(t *testing.T) {
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	xidReferencePersistentSetup(t, p, native)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		cursor, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT x,y,c FROM xid_persist ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		check := func() {
			for i, name := range []string{"XID", "XID8", "CID"} {
				if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != name || cursor.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != reflect.TypeFor[[]byte]() {
					t.Fatal("retained identifier metadata", i)
				}
			}
		}
		check()
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		check()
		values := make([]driver.Value, 3)
		for _, want := range [][]any{{"0", "0", "0"}, {"4294967295", "18446744073709551615", "0"}, {nil, nil, nil}} {
			if err = cursor.Next(values); err != nil {
				return err
			}
			for i, value := range values {
				if value != nil {
					if _, ok := value.([]byte); !ok {
						t.Fatalf("raw retained %T", value)
					}
				}
				if fixture.Normalize(value) != want[i] {
					t.Fatal(values, want)
				}
			}
		}
		if err = cursor.Next(values); err != io.EOF {
			t.Fatal("retained EOF", err)
		}
		check()
		if err = cursor.Close(); err != nil {
			return err
		}
		check()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal("redump mismatch", redump.String(), err)
	}
	if p.Postgres != nil {
		if _, err = p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual native dump replay", err, dump.String())
		}
	}
	for _, b := range p.Backends() {
		xidReferencePersistentRows(t, p, b)
	}
	for _, script := range []string{"CREATE TABLE replacement(v XID); INSERT INTO replacement VALUES('1'); INSERT INTO replacement VALUES('4294967296')", "CREATE TABLE replacement(v XID8); INSERT INTO replacement VALUES('1'); INSERT INTO replacement VALUES('18446744073709551616')", "CREATE TABLE replacement(v CID); INSERT INTO replacement VALUES('1'); INSERT INTO replacement VALUES('bad')"} {
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore accepted")
		}
		xidReferenceRows(t, p.Ctx, native, native.DB, fixture.Select("SELECT x,y,c FROM xid_persist WHERE id=4", []string{"x", "y", "c"}, []string{"XID", "XID8", "CID"}, [][]any{{"4294967295", "18446744073709551615", nil}}))
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	xidReferenceRows(t, p.Ctx, native, native.DB, fixture.Select("SELECT x,y,c FROM xid_persist WHERE id=4", []string{"x", "y", "c"}, []string{"XID", "XID8", "CID"}, [][]any{{"4294967295", "18446744073709551615", nil}}))
}
