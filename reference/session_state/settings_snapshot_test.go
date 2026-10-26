package reference_test

import (
	"database/sql"
	"reflect"
	"sort"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSettingsUtilitySnapshotOwnership(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			fixture.LockExec(t, p.Ctx, c, "SET TIME ZONE 'UTC'", 0)
			fixture.LockExec(t, p.Ctx, c, "SET extra_float_digits=1", 0)
			fixture.LockExec(t, p.Ctx, c, "DO $$BEGIN END$$", 0)
			for _, prepared := range []bool{false, true} {
				fixture.LockExec(t, p.Ctx, c, "SET application_name TO initial", 0)
				for _, tc := range []struct {
					query, name string
					value       any
					nullable    bool
				}{
					{"SHOW application_name", "application_name", "initial", false},
					{"SELECT current_setting('application_name')", "current_setting", "initial", true},
					{"SELECT current_setting(NULL)", "current_setting", nil, true},
					{"SELECT set_config('application_name','changed',false)", "set_config", "changed", true},
				} {
					s := fixture.Select(tc.query, []string{tc.name}, []string{"TEXT"}, [][]any{{tc.value}})
					s.Prepared = prepared
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, backend.PG, tc.nullable) }
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
					e := fixture.Exec(tc.query, 0)
					if tc.nullable {
						e.Affected = 1
					}
					e.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, e)
				}
				fixture.LockExec(t, p.Ctx, c, "SET application_name TO initial", 0)
				var rows *sql.Rows
				if prepared {
					stmt, err := c.PrepareContext(p.Ctx, "SHOW ALL")
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					var err error
					rows, err = c.QueryContext(p.Ctx, "SHOW ALL")
					if err != nil {
						t.Fatal(err)
					}
				}
				columns, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(columns, []string{"name", "setting", "description"}) {
					rows.Close()
					t.Fatal(columns, err)
				}
				metadata, err := rows.ColumnTypes()
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
				for _, column := range metadata {
					nullable, known := column.Nullable()
					if column.DatabaseTypeName() != "TEXT" || column.ScanType() != reflect.TypeFor[string]() || nullable || known == backend.PG {
						rows.Close()
						t.Fatal("SHOW ALL metadata", column, nullable, known)
					}
				}
				expected := make(map[string]string)
				for _, row := range settingsSnapshotExpectedRows(backend.PG) {
					expected[row[0]] = row[1]
				}
				selectedRows, selectedIndex := settingsSnapshotExpectedRows(backend.PG), 0
				for rows.Next() {
					var name, value, description string
					if err := rows.Scan(&name, &value, &description); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					want, selected := expected[name]
					// Assert this scenario's selected values without imposing a
					// global registry size or excluding unrelated builtin settings.
					if !selected {
						continue
					}
					if selectedIndex >= len(selectedRows) || selectedRows[selectedIndex][0] != name {
						rows.Close()
						t.Fatalf("SHOW ALL selected order at %d: %s", selectedIndex, name)
					}
					selectedIndex++
					if want != value {
						rows.Close()
						t.Fatalf("%s got %q want %q", name, value, want)
					}
					delete(expected, name)
				}
				if err := rows.Err(); err != nil || len(expected) != 0 {
					rows.Close()
					t.Fatal(err, expected)
				}
				rows.Close()
				s := fixture.Exec("SHOW ALL", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
			}
		})
	}
}

func settingsSnapshotExpectedRows(postgres bool) [][]string {
	rows := [][]string{
		{"application_name", "initial"},
		{"bytea_output", "hex"},
		{"client_encoding", "UTF8"},
		{"client_min_messages", "notice"},
		{"DateStyle", "ISO, MDY"},
		{"default_transaction_deferrable", "off"},
		{"default_transaction_isolation", "read committed"},
		{"default_transaction_read_only", "off"},
		{"extra_float_digits", "1"},
		{"icu_validation_level", "warning"},
		{"idle_in_transaction_session_timeout", "0"},
		{"idle_session_timeout", "0"},
		{"IntervalStyle", "postgres"},
		{"lc_messages", "C"},
		{"lc_monetary", "C"},
		{"lc_numeric", "C"},
		{"lc_time", "C"},
		{"lock_timeout", "0"},
		{"log_statement_stats", "off"},
		{"max_parallel_workers_per_gather", "2"},
		{"plpgsql.check_asserts", "on"},
		{"quote_all_identifiers", "off"},
		{"row_security", "on"},
		{"search_path", `"$user", public`},
		{"server_encoding", "UTF8"},
		{"standard_conforming_strings", "on"},
		{"statement_timeout", "0"},
		{"TimeZone", "UTC"},
		{"timezone_abbreviations", "Default"},
		{"track_commit_timestamp", "off"},
		{"transaction_deferrable", "off"},
		{"transaction_isolation", "read committed"},
		{"transaction_read_only", "off"},
		{"transaction_timeout", "0"},
	}
	if !postgres {
		for _, def := range physicalSettings {
			rows = append(rows, []string{def.name, def.value})
		}
	}
	for _, def := range connectionSettings {
		rows = append(rows, []string{def.name, connectionInitial(def, postgres)})
	}
	for _, def := range loggingSettings {
		if def.name == "application_name" {
			continue
		}
		view := &physicalCase{pg: postgres}
		initial, _, _ := view.loggingInitial(def)
		rows = append(rows, []string{def.name, initial})
	}
	for _, def := range replicationSettings {
		if def.name != "track_commit_timestamp" {
			rows = append(rows, []string{def.name, def.initial})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return strings.ToLower(rows[i][0]) < strings.ToLower(rows[j][0]) })
	return rows
}
