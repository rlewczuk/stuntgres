package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoElementTypesControls(t *testing.T) {
	etScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		etExec(t, p, b, c,
			"CREATE DOMAIN et_control_elem AS integer",
			"CREATE DOMAIN et_control_array AS integer[]",
			"CREATE TABLE et_control_table(a integer[],d et_control_elem[],nominal et_control_array)",
			"CREATE FUNCTION et_control_fun(a integer[]) RETURNS integer[] LANGUAGE SQL AS 'SELECT a'",
		)
		id := rsuIdentity(t, p, c, "public", "et_control_fun", "integer[]")
		where := "object_schema='public' AND (object_name IN ('et_control_elem','et_control_array','et_control_table') OR object_name='" + id + "')"
		expected := func(db, identity string) [][]any {
			return [][]any{
				etRow(db, "public", "et_control_array", "DOMAIN", 1, "integer", "pg_catalog", "int4"),
				etRow(db, "public", identity, "ROUTINE", 0, "integer", "pg_catalog", "int4"),
				etRow(db, "public", identity, "ROUTINE", 1, "integer", "pg_catalog", "int4"),
				etRow(db, "public", "et_control_table", "TABLE", 1, "integer", "pg_catalog", "int4"),
				etRow(db, "public", "et_control_table", "TABLE", 2, "USER-DEFINED", "public", "et_control_elem"),
			}
		}
		want := expected(database, id)
		step := etProjection(b.PG, where, want)
		etCheck(t, p, b, c, step)
		held := etHeld(t, p, b, c, step.Query)
		held(step)
		if b.PG {
			return // Dump/Reset/Restore are public native controls, not SQL backups.
		}
		old, err := c.QueryContext(p.Ctx, step.Query+" ORDER BY object_type,object_name,collection_type_identifier")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		types, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Len() == 0 {
			t.Fatal("empty declaration dump")
		}
		for _, forbidden := range []string{"CREATE VIEW information_schema.element_types", `CREATE VIEW "information_schema"."element_types"`, "INSERT INTO information_schema.element_types"} {
			if strings.Contains(dump.String(), forbidden) {
				t.Fatal("serialized provider rows", forbidden)
			}
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		etCheck(t, p, b, c, etProjection(false, "object_schema='public'", nil))
		stale := fixture.ErrorDiagnostic(step.Query, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
		stale.SelectRows, stale.ErrorStage = true, "execute"
		held(stale)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		held(stale)
		etConsume(t, old, types, false, want)
		id = rsuIdentity(t, p, c, "public", "et_control_fun", "integer[]")
		where = "object_schema='public' AND (object_name IN ('et_control_elem','et_control_array','et_control_table') OR object_name='" + id + "')"
		want = expected(database, id)
		step = etProjection(false, where, want)
		etCheck(t, p, b, c, step)
		held = etHeld(t, p, b, c, step.Query)
		held(step)

		connector, err := stuntgres.NewDriver().OpenConnector("et-independent-controls")
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		wrapped, recorder := fixture.RecordedNativePool(t, connector)
		fc, err := wrapped.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		var freshDB string
		if err := fc.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&freshDB); err != nil {
			t.Fatal(err)
		}
		freshID := rsuIdentity(t, p, fc, "public", "et_control_fun", "integer[]")
		freshStep := etProjection(false, "object_schema='public' AND (object_name IN ('et_control_elem','et_control_array','et_control_table') OR object_name='"+freshID+"')", expected(freshDB, freshID))
		fixture.RunStep(t, p.Ctx, fc, recorder, freshStep)
		fixture.RunStep(t, p.Ctx, fc, recorder, fixture.Prepared(freshStep))
		// The independent destination's catalog changes cannot retag saved metadata.
		fixture.RunStep(t, p.Ctx, fc, recorder, fixture.Exec("ALTER TABLE et_control_table ADD COLUMN extra text[]", 0))
		held(step)
		etMetadata(t, types, false)

		prefix := "CREATE DOMAIN et_staged_domain AS integer[]; CREATE TABLE et_staged(a et_staged_domain,n integer); INSERT INTO et_staged(n) VALUES("
		d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
		d.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+"'bad');"))
		fixture.AssertDiagnostic(t, err, &d)
		unchanged := func() {
			t.Helper()
			etCheck(t, p, b, c, step)
			held(step)
			etCheck(t, p, b, c, etProjection(false, "object_schema='public' AND object_name IN ('et_staged','et_staged_domain')", nil))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relnamespace='public'::regnamespace AND relname='et_staged'", []string{"relname"}, []string{"NAME"}, nil))
		}
		unchanged()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore=%v", err)
		}
		rows, err := c.QueryContext(ctx, step.Query)
		if rows != nil || !errors.Is(err, context.Canceled) {
			if rows != nil {
				rows.Close()
			}
			t.Fatalf("canceled rows=%v error=%v", rows, err)
		}
		unchanged()
	})
}
