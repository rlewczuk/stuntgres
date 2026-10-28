package reference_test

import (
	"bytes"
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSettingsOptionsStoredControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	definitions := []string{
		`CREATE VIEW options_flags_stored AS SELECT pg_catalog.pg_settings_get_flags('work_mem') AS flags`,
		`CREATE VIEW options_source_stored AS SELECT * FROM pg_catalog.pg_options_to_table(ARRAY['x=1','a=b=c','bare','empty=',' x = y ','x=1'])`,
		`CREATE VIEW options_empty_stored AS SELECT * FROM pg_catalog.pg_options_to_table(NULL::text[])`,
	}
	optionRows := [][]any{{"x", "1"}, {"a", "b=c"}, {"bare", nil}, {"empty", ""}, {" x ", " y "}, {"x", "1"}}
	check := func(t *testing.T, b fixture.Backend, conn *sql.Conn) {
		t.Helper()
		for _, prepared := range []bool{false, true} {
			flags := settingsOptionsFlags(b.PG, `SELECT * FROM options_flags_stored`, "{EXPLAIN}")
			flags.Prepared = prepared
			flags.Columns = []string{"flags"}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, flags)
			for _, tc := range []struct {
				query string
				rows  [][]any
			}{
				{`SELECT * FROM options_source_stored`, optionRows},
				{`SELECT * FROM options_empty_stored`, nil},
			} {
				step := fixture.Select(tc.query, []string{"option_name", "option_value"}, []string{"TEXT", "TEXT"}, tc.rows)
				step.Prepared, step.Bag = prepared, true
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					settingsOptionsStoredTextMetadata(t, rows, b.PG)
				}
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
			}
		}
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			owner, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			for _, definition := range definitions {
				fixture.RunStep(t, p.Ctx, owner, backend.Recorder, fixture.Exec(definition, 0))
			}
			// The same ordinary SQL definitions and independent outcomes run on
			// both backends before exercising backend-specific native controls.
			check(t, backend, owner)
			if backend.PG {
				if len(emitted) == 0 {
					t.Fatal("missing actual native dump bytes for PostgreSQL replay")
				}
				for _, name := range []string{"options_flags_stored", "options_source_stored", "options_empty_stored"} {
					fixture.RunStep(t, p.Ctx, owner, backend.Recorder, fixture.Exec("DROP VIEW "+name, 0))
				}
				if _, err := owner.ExecContext(p.Ctx, string(emitted)); err != nil {
					t.Fatal("actual native stored definitions replay", err)
				}
				check(t, backend, owner)
				return
			}

			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = bytes.Clone(dump.Bytes())
			if len(emitted) == 0 {
				t.Fatal("empty native stored-consumer dump")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"options_flags_stored", "options_source_stored", "options_empty_stored"} {
				fixture.RunStep(t, p.Ctx, owner, backend.Recorder, settingsLabelFailure("SELECT * FROM "+name, fixture.Diagnostic{
					Code: "42P01", Message: `relation "` + name + `" does not exist`, Position: "15",
				}))
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check(t, backend, owner)

			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector("settings-options-restored")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			recorded, recorder := fixture.RecordedNativePool(t, connector)
			target, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			destinationBackend := backend
			destinationBackend.DB, destinationBackend.Recorder = recorded, recorder
			check(t, destinationBackend, target)
		})
	}
}

func settingsOptionsStoredTextMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	// A stored view exposes declared relation attributes, unlike the unknown
	// computed nullability of a direct function-source member. PostgreSQL's pq
	// adapter still has no RowDescription nullability information.
	names := []string{"option_name", "option_value"}
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, names) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(names) {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		if typ.Name() != names[i] || typ.DatabaseTypeName() != "TEXT" || typ.ScanType() != reflect.TypeFor[string]() {
			t.Fatal(typ)
		}
		if length, known := typ.Length(); length != math.MaxInt64 || !known {
			t.Fatal(length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal(precision, scale, known)
		}
		nullable, known := typ.Nullable()
		if pg && (nullable || known) || !pg && (!nullable || !known) {
			t.Fatal("stored view attribute nullability", pg, nullable, known)
		}
	}
}
