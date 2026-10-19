package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtensionDropControls(t *testing.T) {
	extensionDropCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{"CREATE EXTENSION unaccent", "CREATE EXTENSION fuzzystrmatch", "CREATE TABLE ext_drop_control(id int,txt text DEFAULT unaccent('Hôtel'),CONSTRAINT ext_drop_control_check CHECK(unaccent(txt)=txt))", "CREATE VIEW ext_drop_control_gone AS SELECT unaccent('Hôtel') AS v", "CREATE VIEW ext_drop_control_keep AS SELECT soundex('Anne') AS v"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO ext_drop_control(id) VALUES(1)", 1))
		keep := fixture.Select("SELECT v FROM ext_drop_control_keep", []string{"v"}, []string{"TEXT"}, [][]any{{"A500"}})
		run(keep)
		run(fixture.Exec("DROP EXTENSION unaccent CASCADE", 0))
		run(fixture.Exec("INSERT INTO ext_drop_control(id) VALUES(2)", 1))
		run(fixture.Exec("INSERT INTO ext_drop_control(id,txt) VALUES(3,'Hôtel')", 1))
		rows := fixture.Select("SELECT id,txt FROM ext_drop_control ORDER BY id", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Hotel"}, {int64(2), nil}, {int64(3), "Hôtel"}})
		check := func(x fixture.Executor, recorder *fixture.Recorder) {
			for _, s := range []fixture.Step{keep, rows, fixture.Select("SELECT extname FROM pg_extension WHERE extname IN ('unaccent','fuzzystrmatch')", []string{"extname"}, []string{"NAME"}, [][]any{{"fuzzystrmatch"}}), fixture.Select("SELECT count(*) FROM pg_attrdef WHERE adrelid='ext_drop_control'::regclass::oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}), fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='ext_drop_control'::regclass::oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})} {
				fixture.RunStep(t, p.Ctx, x, recorder, s)
				fixture.RunStep(t, p.Ctx, x, recorder, fixture.Prepared(s))
			}
			fixture.RunStep(t, p.Ctx, x, recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid='soundex(text)'::regprocedure::oid AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='fuzzystrmatch') AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
			fixture.RunStep(t, p.Ctx, x, recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=2618 AND refclassid=1255 AND refobjid='soundex(text)'::regprocedure::oid AND deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
			missing := extensionMissing("SELECT unaccent(NULL::text)", "unaccent", "text")
			missing.SelectRows = true
			fixture.RunStep(t, p.Ctx, x, recorder, missing)
		}
		check(c, r)
		if pg {
			return
		} // Native replacement APIs have no PostgreSQL counterpart.
		q := "SELECT soundex($1::text)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		frame := fixture.Select(q, []string{"soundex"}, []string{"TEXT"}, [][]any{{"A500"}}, "Anne")
		fixture.RunStep(t, p.Ctx, held, r, frame)
		retained, err := c.QueryContext(p.Ctx, rows.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname IN ('unaccent','fuzzystrmatch')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, r)
		fixture.RunStep(t, p.Ctx, held, r, frame)
		frame.Args, frame.Rows = []any{nil}, [][]any{{nil}}
		fixture.RunStep(t, p.Ctx, held, r, frame)
		extensionDropControlMetadata(t, retained, rows)
		levenshteinControlRows(t, retained, rows)
		other := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "extension_drop_remaining")
		if _, err := other.ExecContext(p.Ctx, "CREATE TABLE ext_drop_displaced(v int)"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, other, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, s := range []fixture.Step{keep, rows} {
			cursor, err := other.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			extensionDropControlMetadata(t, cursor, s)
			levenshteinControlRows(t, cursor, s)
		}
		var count int
		if err := other.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_extension WHERE extname='unaccent'").Scan(&count); err != nil || count != 0 {
			t.Fatal("removed provider resurrected", count, err)
		}
		missingRows, err := other.QueryContext(p.Ctx, "SELECT unaccent(NULL::text)")
		if missingRows != nil {
			missingRows.Close()
			t.Error("absent provider published Rows")
		}
		want := fixture.ControlsReferenceDiagnostic("42883", "function unaccent(text) does not exist")
		want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		want.Position = "8"
		fixture.AssertDiagnostic(t, err, &want)
	})
}

func extensionDropControlMetadata(t *testing.T, rows *sql.Rows, step fixture.Step) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, step.Columns) {
		t.Fatal("retained columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(step.Types) {
		t.Fatal("retained metadata", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		if step.Types[i] == "INT4" {
			scan = reflect.TypeFor[int64]()
		}
		if typ.DatabaseTypeName() != step.Types[i] || typ.ScanType() != scan {
			t.Fatal("retained column", i, typ)
		}
	}
}
