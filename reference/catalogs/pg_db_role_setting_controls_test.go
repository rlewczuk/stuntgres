package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGDBRoleSettingControls(t *testing.T) {
	pgDBRoleSettingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgDBRoleSettingShape(t, p, b)
		pgDBRoleSettingFacts(t, p, b)
		if b.PG {
			return
		}
		// Shared shape/discovery/attribute proofs and PG-only pg_type corroboration run above.
		// Native controls below have no PostgreSQL API equivalent.
		q := "SELECT * FROM pg_db_role_setting"
		held := fixture.ViewHold(t, p, b, q)
		catalog := func() {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgDBRoleSettingEmpty(q, false))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_db_role_setting", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		sentinel := func() {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id FROM dbrole18_portable", []string{"id"}, []string{"INT4"}, [][]any{{int64(18)}}))
		}
		old, err := b.DB.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		saved, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		pgDBRoleSettingMetadata(t, saved, false)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE dbrole18_portable(id integer)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO dbrole18_portable VALUES(18)", 1))
		dump := fixture.ViewDump(t, p)
		text := string(dump)
		if !strings.Contains(text, "dbrole18_portable") || !strings.Contains(text, "18") {
			t.Fatal("portable dump missing", text)
		}
		for _, forbidden := range []string{"pg_db_role_setting", "ALTER ROLE", "ALTER DATABASE"} {
			if strings.Contains(text, forbidden) {
				t.Fatal("serialized catalog/default population", forbidden, text)
			}
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(text+"CREATE SEQUENCE dbrole18_bad CACHE 0;")); err == nil {
			t.Fatal("invalid restore succeeded")
		} else {
			want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
			fixture.AssertDiagnostic(t, err, &want)
		}
		catalog()
		sentinel()
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		var canceledDump bytes.Buffer
		for _, control := range []func() error{
			func() error { return stuntgres.Reset(canceled, p.Native) },
			func() error { return stuntgres.Dump(canceled, p.Native, &canceledDump) },
			func() error { return stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump)) },
		} {
			pgDBRoleSettingCanceled(t, control())
			catalog()
			sentinel()
		}
		if canceledDump.Len() != 0 {
			t.Fatal("canceled dump published bytes")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		catalog()
		pgDBRoleSettingFacts(t, p, b)
		missing := fixture.ImplicitError("SELECT id FROM dbrole18_portable", fixture.Diagnostic{Code: "42P01", Message: "relation \"dbrole18_portable\" does not exist", Position: "16"})
		missing.SelectRows, missing.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, missing)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		catalog()
		sentinel()
		pgDBRoleSettingFacts(t, p, b)
		if again := fixture.ViewDump(t, p); !bytes.Equal(again, dump) {
			t.Fatal("round-trip dump differs", string(again), text)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained empty cursor", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		pgDBRoleSettingMetadata(t, saved, false)
		const destinationName = "  dbrole18 destination  "
		connector, err := stuntgres.NewDriver().OpenConnector(destinationName)
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		var name string
		if err := destination.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&name); err != nil || name != destinationName {
			t.Fatal("destination identity", name, err)
		}
		empty := pgDBRoleSettingEmpty(q, false)
		empty.NonContext = true
		fixture.RunStep(t, p.Ctx, destination, b.Recorder, empty)
		portable := fixture.Select("SELECT id FROM dbrole18_portable", []string{"id"}, []string{"INT4"}, [][]any{{int64(18)}})
		portable.NonContext = true
		fixture.RunStep(t, p.Ctx, destination, b.Recorder, portable)
	})
}
