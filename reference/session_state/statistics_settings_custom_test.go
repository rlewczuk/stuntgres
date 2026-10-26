package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const statisticsSettingsCustomColumnsSQL = "name,setting,unit,category,short_desc,extra_desc,context,vartype,source,min_val,max_val,enumvals,boot_val,reset_val,sourcefile,sourceline,pending_restart"

var statisticsSettingsCustomColumns = []string{"name", "setting", "unit", "category", "short_desc", "extra_desc", "context", "vartype", "source", "min_val", "max_val", "enumvals", "boot_val", "reset_val", "sourcefile", "sourceline", "pending_restart"}
var statisticsSettingsCustomTypes = []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL"}

func TestReferenceStatisticsSettingsCustom(t *testing.T) {
	p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
		definitions := append(settingsDefinitionsForTest(),
			stuntgres.SettingDefinition{Name: "stats.megabytes", Description: "megabytes", Type: stuntgres.SettingInteger, Unit: stuntgres.SettingMegabytes, Default: "2", IntegerMax: 100},
			stuntgres.SettingDefinition{Name: "stats.minutes", Description: "minutes", Type: stuntgres.SettingInteger, Unit: stuntgres.SettingMinutes, Default: "2", IntegerMax: 100})
		for _, def := range definitions {
			if err := d.RegisterSetting(def); err != nil {
				return err
			}
			if len(def.EnumValues) != 0 {
				def.EnumValues[0] = "caller-mutated"
			}
		}
		return nil
	})
	for _, backend := range p.Backends() {
		for _, tc := range []struct {
			name, kind, value string
			unit, min, max, enums any
		}{
			{"standard_conforming_strings", "bool", "on", nil, nil, nil, nil},
			{"application_name", "string", "", nil, nil, nil, nil},
			{"statement_timeout", "integer", "0", "ms", "0", "2147483647", nil},
			{"bytea_output", "enum", "hex", nil, nil, nil, "{escape,hex}"},
			{"random_page_cost", "real", "4", nil, "0", "1.79769e+308", nil},
		} {
			// The real-valued planner cost is an external analogue, not a
			// supported native setting or a host-registration substitute.
			if tc.name == "random_page_cost" && !backend.PG {
				continue
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT vartype,setting,unit,min_val,max_val,enumvals,boot_val,reset_val FROM pg_settings WHERE name=$1",
					[]string{"vartype", "setting", "unit", "min_val", "max_val", "enumvals", "boot_val", "reset_val"},
					[]string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "TEXT", "TEXT"},
					[][]any{{tc.kind, tc.value, tc.unit, tc.min, tc.max, tc.enums, tc.value, tc.value}}, tc.name)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
		}
	}
	// PostgreSQL has no Go registration API; custom definitions are native.
	backend := p.Backends()[0]
	conn, err := backend.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, tc := range []struct {
		name, kind, context, boot string
		unit, min, max, enums     any
	}{
		{"demo.flag", "bool", "user", "off", nil, nil, nil, nil},
		{"demo.text", "string", "user", "seed", nil, nil, nil, nil},
		{"demo.count", "integer", "user", "2", nil, "-10", "10", nil},
		{"demo.ratio", "real", "user", "1", nil, "0", "10", nil},
		{"demo.mode", "enum", "user", "safe", nil, nil, nil, "{safe,fast}"},
		{"demo.memory", "integer", "user", "1024", "kB", "0", "2147483647", nil},
		{"demo.bytes", "integer", "user", "0", "B", "0", "2147483647", nil},
		{"demo.duration", "integer", "user", "1500", "ms", "0", "2147483647", nil},
		{"demo.real_time", "real", "user", "1", "s", "0", "10", nil},
		{"demo.fixed", "string", "internal", "locked", nil, nil, nil, nil},
		{"demo.priv", "bool", "superuser", "off", nil, nil, nil, nil},
		{"stats.megabytes", "integer", "user", "2", "MB", "0", "100", nil},
		{"stats.minutes", "integer", "user", "2", "min", "0", "100", nil},
	} {
		description := map[string]string{"demo.flag": "flag", "demo.text": "text", "demo.count": "count", "demo.ratio": "ratio", "demo.mode": "mode", "demo.memory": "memory", "demo.bytes": "bytes", "demo.duration": "duration", "demo.real_time": "real time", "demo.fixed": "fixed", "demo.priv": "privileged", "stats.megabytes": "megabytes", "stats.minutes": "minutes"}[tc.name]
		want := []any{tc.name, tc.boot, tc.unit, "Customized Options", description, nil, tc.context, tc.kind, "default", tc.min, tc.max, tc.enums, tc.boot, tc.boot, nil, nil, false}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT "+statisticsSettingsCustomColumnsSQL+" FROM pg_settings WHERE name=$1", statisticsSettingsCustomColumns, statisticsSettingsCustomTypes, [][]any{want}, tc.name)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				statisticsSettingsCheckMetadata(t, rows, false, statisticsSettingsCustomColumns, statisticsSettingsCustomTypes)
			}
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
		}
	}
	for _, tc := range []struct{ query, raw string }{
		{"SET demo.real_time='1.23456789'", "1.23457"},
		{"SET demo.real_time='1500ms'", "1.5"},
	} {
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(tc.query, 0))
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT setting,unit,source,boot_val,reset_val FROM pg_settings WHERE name='demo.real_time'", []string{"setting", "unit", "source", "boot_val", "reset_val"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{tc.raw, "s", "session", "1", "1"}}))
	}
	for _, tc := range []struct{ command, name, value, source, boot string }{
		{"SET demo.text=''", "demo.text", "", "session", "seed"},
		{"RESET demo.text", "demo.text", "seed", "default", "seed"},
		{"SET demo.mode='FAST'", "demo.mode", "fast", "session", "safe"},
	} {
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(tc.command, 0))
		var enums any
		if tc.name == "demo.mode" {
			enums = "{safe,fast}"
		}
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(fixture.Select("SELECT setting,source,boot_val,reset_val,enumvals FROM pg_settings WHERE name=$1",
			[]string{"setting", "source", "boot_val", "reset_val", "enumvals"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "_TEXT"},
			[][]any{{tc.value, tc.source, tc.boot, tc.boot, enums}}, tc.name)))
	}
	retained, err := conn.QueryContext(p.Ctx, "SELECT setting,source,enumvals,boot_val,reset_val FROM pg_settings WHERE name='demo.mode'")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("RESET demo.mode", 0))
	var mode, source, enums, boot, reset string
	if !retained.Next() {
		t.Fatal("missing retained enum row", retained.Err())
	}
	if err := retained.Scan(&mode, &source, &enums, &boot, &reset); err != nil || mode != "fast" || source != "session" || enums != "{safe,fast}" || boot != "safe" || reset != "safe" {
		t.Fatal(mode, source, enums, boot, reset, err)
	}
	if retained.Next() || retained.Err() != nil {
		t.Fatal("retained enum tail", retained.Err())
	}
	retained.Close()
	fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT setting,source,enumvals FROM pg_settings WHERE name='demo.mode'", []string{"setting", "source", "enumvals"}, []string{"TEXT", "TEXT", "_TEXT"}, [][]any{{"safe", "default", "{safe,fast}"}}))
	for _, name := range []string{"demo.fixed", "demo.priv"} {
		code, message := "42501", fmt.Sprintf("permission denied to set parameter %q", name)
		if name == "demo.fixed" {
			code, message = "55P02", fmt.Sprintf("parameter %q cannot be changed", name)
		}
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.ImplicitError("SET "+name+"='invalid'", fixture.ControlsReferenceDiagnostic(code, message)))
	}
}
