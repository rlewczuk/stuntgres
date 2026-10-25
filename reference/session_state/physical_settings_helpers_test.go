package reference_test

import (
	"database/sql"
	"fmt"
	"path"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var physicalSettings = []struct{ name, value, description string }{
	{"config_file", "/stuntgres/simulated/postgresql.conf", "Sets the server's main configuration file."},
	{"data_directory", "/stuntgres/simulated/data", "Sets the server's data directory."},
	{"external_pid_file", "", "Writes the postmaster PID to the specified file."},
	{"hba_file", "/stuntgres/simulated/pg_hba.conf", `Sets the server's "hba" configuration file.`},
	{"ident_file", "/stuntgres/simulated/pg_ident.conf", `Sets the server's "ident" configuration file.`},
}

const physicalPredicate = "name IN ('config_file','data_directory','external_pid_file','hba_file','ident_file')"
const physicalColumnsSQL = "name,setting,unit,category,short_desc,extra_desc,context,vartype,source,min_val,max_val,enumvals,boot_val,reset_val,sourcefile,sourceline,pending_restart"

var physicalColumns = []string{"name", "setting", "unit", "category", "short_desc", "extra_desc", "context", "vartype", "source", "min_val", "max_val", "enumvals", "boot_val", "reset_val", "sourcefile", "sourceline", "pending_restart"}
var physicalTypes = []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL"}

type physicalCase struct {
	t        *testing.T
	p        *fixture.Pair
	conn     *sql.Conn
	pg       bool
	recorder *fixture.Recorder
}

func physicalCases(t *testing.T, roles []string, body func(*physicalCase)) {
	t.Helper()
	if len(roles) != 0 {
		fixture.RoleReferenceCleanup(t, roles)
	}
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body(&physicalCase{t, p, conn, backend.PG, backend.Recorder})
		})
	}
}

func (s *physicalCase) run(step fixture.Step) {
	s.t.Helper()
	fixture.RunStep(s.t, s.p.Ctx, s.conn, s.recorder, step)
}

func (s *physicalCase) login(name string) *physicalCase {
	s.t.Helper()
	var database string
	if err := s.conn.QueryRowContext(s.p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
		s.t.Fatal(err)
	}
	pool, _, recorder := fixture.DatabaseScenarioPool(s.t, s.p, s.pg, database, name)
	conn, err := pool.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { conn.Close() })
	return &physicalCase{s.t, s.p, conn, s.pg, recorder}
}

func physicalDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}

func physicalDenied(name string) fixture.Diagnostic {
	d := physicalDiagnostic("42501", fmt.Sprintf("permission denied to examine %q", name))
	d.Detail = `Only roles with privileges of the "pg_read_all_settings" role may examine this parameter.`
	return d
}

func physicalRestart(name string) fixture.Diagnostic {
	return physicalDiagnostic("55P02", fmt.Sprintf("parameter %q cannot be changed without restarting the server", name))
}

func physicalError(query string, d fixture.Diagnostic, prepared, rows bool, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(query, d, args...)
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, rows, "execute"
	return s
}

func physicalLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// Oracle paths are environmental, but all other fields have independent release
// expectations. Native paths are fixed literals, never inferred from the engine.
func (s *physicalCase) inventory(prepared bool) map[string]string {
	s.t.Helper()
	query := "SELECT " + physicalColumnsSQL + " FROM pg_settings WHERE " + physicalPredicate + " ORDER BY name"
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, query)
		if e != nil {
			s.t.Fatal(e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else {
		rows, err = s.conn.QueryContext(s.p.Ctx, query)
	}
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	statisticsSettingsCheckMetadata(s.t, rows, s.pg, physicalColumns, physicalTypes)
	values := make(map[string]string)
	for i := 0; rows.Next(); i++ {
		cells, dest := make([]any, len(physicalColumns)), make([]any, len(physicalColumns))
		for j := range cells {
			dest[j] = &cells[j]
		}
		if err := rows.Scan(dest...); err != nil {
			s.t.Fatal(err)
		}
		for j := range cells {
			cells[j] = fixture.Normalize(cells[j])
		}
		if i >= len(physicalSettings) {
			s.t.Fatalf("unexpected physical row: %v", cells)
		}
		def := physicalSettings[i]
		value := def.value
		if s.pg && def.name != "external_pid_file" {
			var ok bool
			value, ok = cells[1].(string)
			if !ok || !path.IsAbs(value) || value == "/" {
				s.t.Fatalf("%s oracle path is not a nonempty absolute file/directory label: %v", def.name, cells)
			}
		}
		var reset any = value
		source := "override"
		if def.name == "external_pid_file" {
			reset, source = nil, "default"
		}
		want := []any{def.name, value, nil, "File Locations", def.description, nil, "postmaster", "string", source, nil, nil, nil, nil, reset, nil, nil, false}
		if !reflect.DeepEqual(cells, want) {
			s.t.Fatalf("%s pg_settings got %#v want %#v", def.name, cells, want)
		}
		values[def.name] = value
	}
	if err := rows.Err(); err != nil || len(values) != len(physicalSettings) {
		s.t.Fatalf("physical inventory count=%d want=5: %v", len(values), err)
	}
	return values
}

func (s *physicalCase) reads(values map[string]string, allowed, prepared bool) {
	s.t.Helper()
	for _, def := range physicalSettings {
		for _, query := range []string{"SHOW " + def.name, "SELECT current_setting(" + physicalLiteral(def.name) + ")"} {
			if !allowed {
				step := physicalError(query, physicalDenied(def.name), prepared, true)
				if prepared && strings.HasPrefix(query, "SHOW ") {
					step.ErrorStage = "prepare"
				}
				s.run(step)
				continue
			}
			column := "current_setting"
			if strings.HasPrefix(query, "SHOW ") {
				column = def.name
			}
			step := fixture.Select(query, []string{column}, []string{"TEXT"}, [][]any{{values[def.name]}})
			step.Prepared = prepared
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				settingsTextMetadata(t, rows, s.pg, column == "current_setting")
			}
			s.run(step)
		}
	}
	s.aggregates(values, allowed, prepared)
}

func (s *physicalCase) aggregates(values map[string]string, allowed, prepared bool) {
	s.t.Helper()
	var want [][]any
	if allowed {
		for _, def := range physicalSettings {
			want = append(want, []any{def.name, values[def.name], def.description})
		}
	}
	step := fixture.Select("SELECT name,setting,short_desc FROM pg_settings WHERE "+physicalPredicate+" ORDER BY name", []string{"name", "setting", "short_desc"}, []string{"TEXT", "TEXT", "TEXT"}, want)
	step.Prepared = prepared
	s.run(step)
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, "SHOW ALL")
		if e != nil {
			s.t.Fatal(e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else {
		rows, err = s.conn.QueryContext(s.p.Ctx, "SHOW ALL")
	}
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	s.checkAll(rows, values, allowed)
}

func (s *physicalCase) checkAll(rows *sql.Rows, values map[string]string, allowed bool) {
	s.t.Helper()
	found := map[string]bool{}
	for rows.Next() {
		var name, value, description string
		if err := rows.Scan(&name, &value, &description); err != nil {
			s.t.Fatal(err)
		}
		for _, def := range physicalSettings {
			if name == def.name {
				if !allowed || found[name] || value != values[name] || description != def.description {
					s.t.Fatalf("SHOW ALL unexpected row: %s %q %q allowed=%v", name, value, description, allowed)
				}
				found[name] = true
			}
		}
	}
	if err := rows.Err(); err != nil || (allowed && len(found) != 5) {
		s.t.Fatal("SHOW ALL physical inventory", found, err)
	}
}
