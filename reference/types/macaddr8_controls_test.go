package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func macaddr8SavedAssertions(t *testing.T, p *fixture.Pair, executor fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	pg := recorder.RawBackend == fixture.RawPostgres
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, recorder, s) }
	run(macaddr8Select(pg, "SELECT id,v FROM mac8_saved ORDER BY id", []string{"id", "v"}, []string{"INT4", "MACADDR8"}, [][]any{{int64(1), macaddr8Six}, {int64(2), "ff:ff:ff:ff:ff:ff:ff:ff"}, {int64(3), nil}}))
	run(macaddr8Select(pg, "SELECT id,v,origin,missing,accepted FROM mac8_view ORDER BY id", []string{"id", "v", "origin", "missing", "accepted"}, []string{"INT4", "MACADDR8", "MACADDR8", "MACADDR8", "BOOL"}, [][]any{{int64(1), macaddr8Six, macaddr8Six, nil, true}, {int64(2), "ff:ff:ff:ff:ff:ff:ff:ff", macaddr8Six, nil, true}, {int64(3), nil, macaddr8Six, nil, nil}}))
	_, transactional := executor.(*sql.Tx)
	if transactional {
		run(fixture.Exec("SAVEPOINT mac8_check", 0))
	}
	d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "mac8_saved" violates check constraint "mac8_saved_check"`)
	d.Detail = "Failing row contains (4, 00:00:00:ff:fe:00:00:00)."
	d.Schema = "public"
	d.Table = "mac8_saved"
	d.Constraint = "mac8_saved_check"
	run(fixture.ErrorDiagnostic("INSERT INTO mac8_saved VALUES(4,'000000000000')", d))
	if transactional {
		run(fixture.Exec("ROLLBACK TO SAVEPOINT mac8_check", 0))
		run(fixture.Exec("RELEASE SAVEPOINT mac8_check", 0))
	}
	run(fixture.Select("SELECT id FROM mac8_saved WHERE id=4", []string{"id"}, []string{"INT4"}, nil))
	run(fixture.Exec("INSERT INTO mac8_saved(id) VALUES(4)", 1))
	run(macaddr8Value(pg, "SELECT v FROM mac8_saved WHERE id=4", macaddr8Six))
	run(fixture.Exec("DELETE FROM mac8_saved WHERE id=4", 1))
}

func macaddr8NativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		rows, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT v FROM mac8_saved ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer rows.Close()
		metadata := rows.(driver.RowsColumnTypeDatabaseTypeName)
		checkMetadata := func() {
			if metadata.ColumnTypeDatabaseTypeName(0) != "MACADDR8" || rows.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
				t.Fatal("retained MACADDR8 metadata")
			}
		}
		checkMetadata()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		d := fixture.ControlsReferenceDiagnostic("42P01", `relation "mac8_saved" does not exist`)
		d.Position = "15"
		s := fixture.ErrorDiagnostic("SELECT v FROM mac8_saved", d)
		s.SelectRows = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			return err
		}
		if dump.String() != redump.String() {
			t.Fatal("nondeterministic MACADDR8 dump", dump.String(), redump.String())
		}
		macaddr8SavedAssertions(t, p, b.DB, b.Recorder)
		fresh := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		macaddr8SavedAssertions(t, p, fresh.Memory, fresh.MemoryRecord)
		var freshDump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, fresh.Native, &freshDump); err != nil {
			return err
		}
		if dump.String() != freshDump.String() {
			t.Fatal("fresh-driver MACADDR8 dump changed")
		}
		if p.Postgres != nil {
			tx, err := p.Postgres.BeginTx(p.Ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(p.Ctx, dump.String()); err != nil {
				return err
			}
			macaddr8SavedAssertions(t, p, tx, p.PostgresRecord)
			if err := tx.Rollback(); err != nil {
				return err
			}
		}
		script := "/*é*/ CREATE TABLE mac8_staged(v macaddr8); INSERT INTO mac8_staged VALUES('bad')"
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		d = macaddr8Diagnostic("bad")
		d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "'bad'")])) + 1)
		fixture.AssertDiagnostic(t, err, &d)
		macaddr8SavedAssertions(t, p, b.DB, b.Recorder)
		d = fixture.ControlsReferenceDiagnostic("42P01", `relation "mac8_staged" does not exist`)
		d.Position = "15"
		s = fixture.ErrorDiagnostic("SELECT v FROM mac8_staged", d)
		s.SelectRows = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE mac8_saved(v text); INSERT INTO mac8_saved VALUES('new')")); err != nil {
			return err
		}
		checkMetadata()
		dst := make([]driver.Value, 1)
		for _, want := range []driver.Value{macaddr8Six, "ff:ff:ff:ff:ff:ff:ff:ff", nil} {
			if err := rows.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{want}) {
				t.Fatal("retained MACADDR8 row", dst, want, err)
			}
		}
		if err := rows.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		checkMetadata()
		if err := rows.Close(); err != nil {
			return err
		}
		checkMetadata()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM mac8_saved", []string{"v"}, []string{"TEXT"}, [][]any{{"new"}}))
}

func TestReferenceMACADDR8Controls(t *testing.T) {
	macaddr8Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mac8_saved(id integer,v macaddr8 DEFAULT '08002b010203'::macaddr8 CONSTRAINT mac8_saved_check CHECK(v >= '08:00:2b:ff:fe:01:02:03'::macaddr8))", 0))
		run(fixture.Exec("INSERT INTO mac8_saved(id) VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO mac8_saved VALUES(2,'ff-ff-ff-ff-ff-ff-ff-ff'),(3,NULL)", 2))
		run(fixture.Exec("CREATE VIEW mac8_view AS SELECT id,v,'0800.2b01.0203'::macaddr8 AS origin,NULL::macaddr8 AS missing,pg_catalog.macaddr8_ge(v,'08002b010203'::macaddr8) AS accepted FROM mac8_saved", 0))
		macaddr8SavedAssertions(t, p, b.DB, b.Recorder)
		if !b.PG {
			macaddr8NativeControls(t, p, b)
		}
	})
}
