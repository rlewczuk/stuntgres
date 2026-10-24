package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const statisticsSettingsColumnsSQL = "name,setting,unit,category,short_desc,extra_desc,context,vartype,source,min_val,max_val,enumvals,boot_val,reset_val,sourcefile,sourceline,pending_restart"

var statisticsSettingsColumns = []string{"name", "setting", "unit", "category", "short_desc", "extra_desc", "context", "vartype", "source", "min_val", "max_val", "enumvals", "boot_val", "reset_val", "sourcefile", "sourceline", "pending_restart"}
var statisticsSettingsTypes = []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL"}

type statisticsSettingsRun func(fixture.Step)

func statisticsSettingsCases(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, bool, statisticsSettingsRun)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			body(t, p, conn, backend.PG, run)
		})
	}
}

func statisticsSettingsMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, statisticsSettingsColumns) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 17 {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		if typ.Name() != statisticsSettingsColumns[i] || typ.DatabaseTypeName() != statisticsSettingsTypes[i] {
			t.Fatalf("column %d: %v", i, typ)
		}
		nullable, known := typ.Nullable()
		if known == pg || (!pg && !nullable) || (pg && nullable) {
			t.Fatalf("%s nullable=(%v,%v)", typ.Name(), nullable, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("%s decimal=(%d,%d,%v)", typ.Name(), precision, scale, known)
		}
		scan := reflect.TypeFor[string]()
		length, lengthKnown := int64(0), false
		switch statisticsSettingsTypes[i] {
		case "TEXT":
			length, lengthKnown = math.MaxInt64, true
		case "_TEXT":
			if pg {
				scan = reflect.TypeFor[any]()
			}
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		}
		if typ.ScanType() != scan {
			t.Fatalf("%s scan=%v want %v", typ.Name(), typ.ScanType(), scan)
		}
		if n, known := typ.Length(); n != length || known != lengthKnown {
			t.Fatalf("%s length=(%d,%v)", typ.Name(), n, known)
		}
	}
}

func statisticsSettingsTimeoutRow(value, source string) []any {
	return []any{"statement_timeout", value, "ms", "Client Connection Defaults / Statement Behavior", "Sets the maximum allowed duration of any statement.", "0 disables the timeout.", "user", "integer", source, "0", "2147483647", nil, "0", "0", nil, nil, false}
}

func statisticsSettingsTimeout(run statisticsSettingsRun, value, source string, prepared bool) {
	s := fixture.Select("SELECT "+statisticsSettingsColumnsSQL+" FROM pg_settings WHERE name='statement_timeout'", statisticsSettingsColumns, statisticsSettingsTypes, [][]any{statisticsSettingsTimeoutRow(value, source)})
	if prepared {
		s = fixture.Prepared(s)
	}
	run(s)
}

func TestReferenceStatisticsSettingsMetadata(t *testing.T) {
	statisticsSettingsCases(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, pg bool, run statisticsSettingsRun) {
		for _, prepared := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				predicate := "name='statement_timeout'"
				want := [][]any{statisticsSettingsTimeoutRow("0", "default")}
				if empty {
					predicate = "name='statistics_missing_parameter'"
					want = nil
				}
				s := fixture.Select("SELECT "+statisticsSettingsColumnsSQL+" FROM pg_settings WHERE "+predicate, statisticsSettingsColumns, statisticsSettingsTypes, want)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { statisticsSettingsMetadata(t, rows, pg) }
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
			}
		}
		// Full native registry membership is bounded; PostgreSQL has many more GUCs.
		if !pg {
			run(fixture.Select("SELECT count(*) FROM pg_settings", []string{"count"}, []string{"INT8"}, [][]any{{int64(34)}}))
		}
		run(fixture.Exec("SET statistics.placeholder='hidden'", 0))
		run(fixture.Select("SELECT name FROM pg_settings WHERE name='statistics.placeholder'", []string{"name"}, []string{"TEXT"}, nil))
		run(fixture.Select("SELECT name FROM pg_settings WHERE name IN ('role','session_authorization') ORDER BY name", []string{"name"}, []string{"TEXT"}, nil))
		statisticsSettingsBuiltinInventory(t, pg, run)
		for _, tc := range []struct{ command, name, display, value string }{
			{"SET client_encoding='UNICODE'", "client_encoding", "client_encoding", "UNICODE"},
			{"SET TIME ZONE 'UTC'", "TIMEZONE", "TimeZone", "UTC"},
			{"SET lc_numeric='C'", "LC_NUMERIC", "lc_numeric", "C"},
		} {
			run(fixture.Exec(tc.command, 0))
			run(fixture.Prepared(fixture.Select("SELECT current_setting($1)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{tc.value}}, tc.name)))
			run(fixture.Select("SELECT name,setting FROM pg_settings WHERE name=$1", []string{"name", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{tc.display, tc.value}}, tc.display))
		}
	})
}

func TestReferenceStatisticsSettingsFeatureSmoke(t *testing.T) {
	statisticsSettingsCases(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, pg bool, run statisticsSettingsRun) {
		want := [][]any{
			{"application_name", "", nil, "Reporting and Logging / What to Log", "Sets the application name to be reported in statistics and logs.", nil, "user", "string", "default", nil, nil, nil, "", "", nil, nil, false},
			{"log_statement_stats", "off", nil, "Statistics / Monitoring", "Writes cumulative performance statistics to the server log.", nil, "superuser", "bool", "default", nil, nil, nil, "off", "off", nil, nil, false},
			{"row_security", "on", nil, "Client Connection Defaults / Statement Behavior", "Enables row security.", "When enabled, row security will be applied to all users.", "user", "bool", "default", nil, nil, nil, "on", "on", nil, nil, false},
			statisticsSettingsTimeoutRow("0", "default"),
			{"track_commit_timestamp", "off", nil, "Replication / Sending Servers", "Collects transaction commit time.", nil, "postmaster", "bool", "default", nil, nil, nil, "off", "off", nil, nil, false},
		}
		if pg {
			// The pinned fixture starts PostgreSQL with -c track_commit_timestamp=off.
			want[4][8] = "command line"
		}
		inventory := fixture.Select("SELECT "+statisticsSettingsColumnsSQL+" FROM pg_settings WHERE name IN ('application_name','row_security','statement_timeout','log_statement_stats','track_commit_timestamp') ORDER BY name", statisticsSettingsColumns, statisticsSettingsTypes, want)
		inventory.CheckMetadata = func(t *testing.T, rows *sql.Rows) { statisticsSettingsMetadata(t, rows, pg) }
		run(inventory)
		if !pg {
			run(fixture.Select("SELECT count(*) FROM pg_settings", []string{"count"}, []string{"INT8"}, [][]any{{int64(34)}}))
		}
		query := "SELECT setting,unit,source FROM pg_settings WHERE name='statement_timeout'"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec("SET statement_timeout='1s'", 0))
		statisticsSettingsTimeout(run, "1000", "session", false)
		run(fixture.Select("SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{"1s"}}))
		run(fixture.Prepared(fixture.Select("SELECT current_setting('statement_timeout')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"1s"}})))
		var value, unit, source string
		if err := stmt.QueryRowContext(p.Ctx).Scan(&value, &unit, &source); err != nil || value != "1000" || unit != "ms" || source != "session" {
			t.Fatal(value, unit, source, err)
		}
		all, err := conn.QueryContext(p.Ctx, "SHOW ALL")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for all.Next() {
			var name, setting, description string
			if err := all.Scan(&name, &setting, &description); err != nil {
				all.Close()
				t.Fatal(err)
			}
			if name == "statement_timeout" {
				found = true
				if setting != "1s" || description != "Sets the maximum allowed duration of any statement." {
					all.Close()
					t.Fatal(setting, description)
				}
			}
		}
		if err := all.Err(); err != nil || !found {
			all.Close()
			t.Fatal(found, err)
		}
		all.Close()
		var retained *sql.Rows
		if !pg {
			retained, err = conn.QueryContext(p.Ctx, "SELECT "+statisticsSettingsColumnsSQL+" FROM pg_settings WHERE name='statement_timeout'")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			statisticsSettingsMetadata(t, retained, false)
		}
		run(fixture.Exec("RESET statement_timeout", 0))
		if retained != nil {
			cells, dest := make([]any, 17), make([]any, 17)
			for i := range cells {
				dest[i] = &cells[i]
			}
			if !retained.Next() {
				t.Fatal("missing retained smoke row", retained.Err())
			}
			if err := retained.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			for i := range cells {
				cells[i] = fixture.Normalize(cells[i])
			}
			if !reflect.DeepEqual(cells, statisticsSettingsTimeoutRow("1000", "session")) {
				t.Fatal(cells)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal(retained.Err())
			}
			retained.Close()
		}
		statisticsSettingsTimeout(run, "0", "default", true)
		run(fixture.Exec("SET application_name=''", 0))
		run(fixture.Select("SELECT setting,source FROM pg_settings WHERE name='application_name'", []string{"setting", "source"}, []string{"TEXT", "TEXT"}, [][]any{{"", "session"}}))
		run(fixture.Exec("RESET application_name", 0))
		restoredSource := "default"
		run(fixture.Select("SELECT setting,source FROM pg_settings WHERE name='application_name'", []string{"setting", "source"}, []string{"TEXT", "TEXT"}, [][]any{{"", restoredSource}}))
		if !pg {
			run(fixture.ImplicitError("SHOW track_counts", fixture.ControlsReferenceDiagnostic("42704", `unrecognized configuration parameter "track_counts"`)))
			run(fixture.ImplicitError("SET log_statement_stats=on", fixture.ControlsReferenceDiagnostic("42501", `permission denied to set parameter "log_statement_stats"`)))
			run(fixture.Select("SHOW log_statement_stats", []string{"log_statement_stats"}, []string{"TEXT"}, [][]any{{"off"}}))
			run(fixture.Select("SELECT name FROM pg_settings WHERE name='track_counts'", []string{"name"}, []string{"TEXT"}, nil))
			s := fixture.ErrorDiagnostic("UPDATE pg_settings SET setting='smoke' WHERE name='application_name'", statisticsSettingsWriteDiagnostic("update", "updating the view"))
			run(s)
			run(fixture.Select("SELECT current_setting('application_name')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{""}}))
		} else {
			run(fixture.Select("SELECT name,setting FROM pg_settings WHERE name='track_counts'", []string{"name", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{"track_counts", "on"}}))
			run(fixture.Exec("BEGIN", 0))
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("UPDATE pg_settings SET setting='smoke' WHERE name='application_name'", 0))
			run(fixture.Select("SELECT current_setting('application_name')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"smoke"}}))
			run(fixture.Exec("ROLLBACK", 0))
		}
	})
}
