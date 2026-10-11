package reference_test

import (
	"bytes"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNameDump(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, q := range []string{`CREATE TABLE "MixedObject"(id INT,n NAME)`, "CREATE TABLE empty(n NAME)", "CREATE TABLE nulls(n NAME)", "INSERT INTO nulls VALUES(NULL)"} {
		if _, err := p.Native.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	want := [][]any{{int64(1), "MiXeD"}, {int64(2), "O'Brien\\tail\n🙂"}, {int64(3), strings.Repeat("a", 61) + "é"}, {int64(4), " x "}, {int64(5), ""}, {int64(6), nil}}
	for i, v := range []any{"MiXeD", "O'Brien\\tail\n🙂", strings.Repeat("a", 61) + "éZ", " x ", "", nil} {
		if _, err := p.Native.ExecContext(p.Ctx, `INSERT INTO "MixedObject" VALUES($1,$2)`, i+1, v); err != nil {
			t.Fatal(err)
		}
	}
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
		cursor, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, `SELECT * FROM "MixedObject" ORDER BY id`, nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "NAME" || cursor.(driver.RowsColumnTypeScanType).ColumnTypeScanType(1) != reflect.TypeFor[string]() {
			t.Fatal("retained NAME metadata")
		}
		values := make([]driver.Value, 2)
		for _, row := range want {
			if err := cursor.Next(values); err != nil {
				return err
			}
			if !reflect.DeepEqual([]any{values[0], values[1]}, row) {
				t.Fatal(values, row)
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
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("native NAME dump replay", err)
		}
	}
	step := fixture.Select(`SELECT * FROM "MixedObject" ORDER BY id`, []string{"id", "n"}, []string{"INT4", "NAME"}, want)
	p.Step(t, step)
	p.Step(t, fixture.Select("SELECT n FROM nulls", []string{"n"}, []string{"NAME"}, [][]any{{nil}}))
	p.Step(t, fixture.Select("SELECT n FROM empty", []string{"n"}, []string{"NAME"}, nil))
	for _, script := range []string{"CREATE TABLE replacement(n NAME); INSERT INTO replacement VALUES('ok'); nonsense", "CREATE TABLE replacement(n NAME); INSERT INTO replacement VALUES('ok'),('" + strings.Repeat("a", 63) + "\xff')"} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
			t.Fatal("bad staged restore accepted")
		}
		p.Step(t, step)
	}
}
