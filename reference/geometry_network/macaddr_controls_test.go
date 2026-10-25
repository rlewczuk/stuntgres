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

func macaddrSavedSteps() []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT id,v FROM mac_saved ORDER BY id", []string{"id", "v"}, []string{"INT4", "MACADDR"}, [][]any{{int64(1), macaddrSample}, {int64(2), "ff:ff:ff:ff:ff:ff"}, {int64(3), nil}}),
		fixture.Select("SELECT id,v,origin,missing FROM mac_view ORDER BY id", []string{"id", "v", "origin", "missing"}, []string{"INT4", "MACADDR", "MACADDR", "MACADDR"}, [][]any{{int64(1), macaddrSample, macaddrSample, nil}, {int64(2), "ff:ff:ff:ff:ff:ff", macaddrSample, nil}, {int64(3), nil, macaddrSample, nil}}),
	}
}

func macaddrCheckDiagnostic() fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "mac_saved" violates check constraint "mac_saved_check"`)
	d.Detail = "Failing row contains (4, 00:00:00:00:00:00)."
	d.Schema = "public"
	d.Table = "mac_saved"
	d.Constraint = "mac_saved_check"
	return d
}

// Reuse these same SQL assertions for the original database, native restored
// database and the actual native dump executed on PostgreSQL, never a handwritten
// approximation of the serialized script.
func macaddrSavedAssertions(t *testing.T, p *fixture.Pair, executor fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, recorder, s) }
	for _, s := range macaddrSavedSteps() {
		run(s)
	}
	// The actual native dump is also exercised inside a rollback-protected
	// PostgreSQL transaction. Recover the expected CHECK failure there before
	// testing subsequent writes; autocommit callers need no savepoint.
	_, transactional := executor.(*sql.Tx)
	if transactional {
		run(fixture.Exec("SAVEPOINT mac_check", 0))
	}
	run(fixture.ErrorDiagnostic("INSERT INTO mac_saved VALUES(4,'000000000000')", macaddrCheckDiagnostic()))
	if transactional {
		run(fixture.Exec("ROLLBACK TO SAVEPOINT mac_check", 0))
		run(fixture.Exec("RELEASE SAVEPOINT mac_check", 0))
	}
	run(fixture.Exec("INSERT INTO mac_saved(id) VALUES(4)", 1))
	run(macaddrValue("SELECT v FROM mac_saved WHERE id=4", macaddrSample))
	run(fixture.Exec("DELETE FROM mac_saved WHERE id=4", 1))
}

func macaddrNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		rows, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT v FROM mac_saved ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer rows.Close()
		metadata := rows.(driver.RowsColumnTypeDatabaseTypeName)
		checkMetadata := func() {
			if metadata.ColumnTypeDatabaseTypeName(0) != "MACADDR" || rows.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
				t.Fatal("retained MACADDR metadata")
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
		d := fixture.ControlsReferenceDiagnostic("42P01", `relation "mac_saved" does not exist`)
		d.Position = "15"
		s := fixture.ErrorDiagnostic("SELECT v FROM mac_saved", d)
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
			t.Fatal("MACADDR nondeterministic redump", dump.String(), redump.String())
		}
		macaddrSavedAssertions(t, p, b.DB, b.Recorder)
		if p.Postgres != nil {
			tx, err := p.Postgres.BeginTx(p.Ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(p.Ctx, dump.String()); err != nil {
				return err
			}
			macaddrSavedAssertions(t, p, tx, p.PostgresRecord)
			if err := tx.Rollback(); err != nil {
				return err
			}
		}
		script := "CREATE TABLE mac_staged(v macaddr); INSERT INTO mac_staged VALUES('bad')"
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		d = macaddrDiagnostic("bad", "22P02")
		d.Position = fixture.Position(strings.Index(script, "'bad'") + 1)
		fixture.AssertDiagnostic(t, err, &d)
		macaddrSavedAssertions(t, p, b.DB, b.Recorder)
		d = fixture.ControlsReferenceDiagnostic("42P01", `relation "mac_staged" does not exist`)
		d.Position = "15"
		s = fixture.ErrorDiagnostic("SELECT v FROM mac_staged", d)
		s.SelectRows = true
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE mac_replacement(v text); INSERT INTO mac_replacement VALUES('new')")); err != nil {
			return err
		}
		checkMetadata()
		dst := make([]driver.Value, 1)
		for _, want := range []driver.Value{macaddrSample, "ff:ff:ff:ff:ff:ff", nil} {
			if err := rows.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{want}) {
				t.Fatal("retained MACADDR row", dst, want, err)
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
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM mac_replacement", []string{"v"}, []string{"TEXT"}, [][]any{{"new"}}))
}

func TestReferenceMACADDRControls(t *testing.T) {
	macaddrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mac_saved(id integer,v macaddr DEFAULT '08002b010203'::macaddr CONSTRAINT mac_saved_check CHECK(v >= '08:00:2b:01:02:03'::macaddr))", 0))
		run(fixture.Exec("INSERT INTO mac_saved(id) VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO mac_saved VALUES(2,'ff-ff-ff-ff-ff-ff'),(3,NULL)", 2))
		run(fixture.Exec("CREATE VIEW mac_view AS SELECT id,v,'0800.2b01.0203'::macaddr AS origin,NULL::macaddr AS missing FROM mac_saved", 0))
		macaddrSavedAssertions(t, p, b.DB, b.Recorder)
		if !b.PG {
			macaddrNativeControls(t, p, b)
		}
	})
}
