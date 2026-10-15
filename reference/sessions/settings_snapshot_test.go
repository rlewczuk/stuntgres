package reference_test

import (
	"database/sql"
	"reflect"
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
				expected := make(map[string][2]string)
				for _, row := range settingsSnapshotExpectedRows() {
					expected[row[0]] = [2]string{row[1], row[2]}
				}
				selectedRows, selectedIndex := settingsSnapshotExpectedRows(), 0
				for rows.Next() {
					var name, value, description string
					if err := rows.Scan(&name, &value, &description); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					want, selected := expected[name]
					// PostgreSQL exposes additional server settings outside the native
					// registry; the selected builtin inventory must still be complete.
					if !selected {
						if !backend.PG {
							rows.Close()
							t.Fatalf("unexpected native setting %s", name)
						}
						continue
					}
					if selectedIndex >= len(selectedRows) || selectedRows[selectedIndex][0] != name {
						rows.Close()
						t.Fatalf("SHOW ALL selected order at %d: %s", selectedIndex, name)
					}
					selectedIndex++
					if want != [2]string{value, description} {
						rows.Close()
						t.Fatalf("%s got %q/%q want %q/%q", name, value, description, want[0], want[1])
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

func settingsSnapshotExpectedRows() [][]string {
	all := [][]string{
		{"application_name", "initial", "Sets the application name to be reported in statistics and logs."},
		{"bytea_output", "hex", "Sets the output format for bytea."},
		{"client_encoding", "UTF8", "Sets the client's character set encoding."},
		{"client_min_messages", "notice", "Sets the message levels that are sent to the client."},
		{"DateStyle", "ISO, MDY", "Sets the display format for date and time values."},
		{"default_transaction_deferrable", "off", "Sets the default deferrable status of new transactions."},
		{"default_transaction_isolation", "read committed", "Sets the transaction isolation level of each new transaction."},
		{"default_transaction_read_only", "off", "Sets the default read-only status of new transactions."},
		{"extra_float_digits", "1", "Sets the number of digits displayed for floating-point values."},
		{"icu_validation_level", "warning", "Log level for reporting invalid ICU locale strings."},
		{"idle_in_transaction_session_timeout", "0", "Sets the maximum allowed idle time between queries, when in a transaction."},
		{"idle_session_timeout", "0", "Sets the maximum allowed idle time between queries, when not in a transaction."},
		{"IntervalStyle", "postgres", "Sets the display format for interval values."},
		{"lc_messages", "C", "Sets the language in which messages are displayed."},
		{"lc_monetary", "C", "Sets the locale for formatting monetary amounts."},
		{"lc_numeric", "C", "Sets the locale for formatting numbers."},
		{"lc_time", "C", "Sets the locale for formatting date and time values."},
		{"lock_timeout", "0", "Sets the maximum allowed duration of any wait for a lock."},
		{"log_statement_stats", "off", "Writes cumulative performance statistics to the server log."},
		{"max_parallel_workers_per_gather", "2", "Sets the maximum number of parallel processes per executor node."},
		{"quote_all_identifiers", "off", "When generating SQL fragments, quote all identifiers."},
		{"search_path", `"$user", public`, "Sets the schema search order for names that are not schema-qualified."},
		{"server_encoding", "UTF8", "Shows the server (database) character set encoding."},
		{"standard_conforming_strings", "on", "Causes '...' strings to treat backslashes literally."},
		{"statement_timeout", "0", "Sets the maximum allowed duration of any statement."},
		{"TimeZone", "UTC", "Sets the time zone for displaying and interpreting time stamps."},
		{"timezone_abbreviations", "Default", "Selects a file of time zone abbreviations."},
		{"track_commit_timestamp", "off", "Collects transaction commit time."},
		{"transaction_deferrable", "off", "Whether to defer a read-only serializable transaction until it can be executed with no possible serialization failures."},
		{"transaction_isolation", "read committed", "Sets the current transaction's isolation level."},
		{"transaction_read_only", "off", "Sets the current transaction's read-only status."},
		{"transaction_timeout", "0", "Sets the maximum allowed duration of any transaction within a session (not a prepared transaction)."},
	}
	return all
}
