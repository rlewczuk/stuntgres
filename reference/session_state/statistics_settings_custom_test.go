package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

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
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT setting FROM pg_settings WHERE name='statement_timeout'", []string{"setting"}, []string{"TEXT"}, [][]any{{"0"}}))
	}
	// PostgreSQL has no Go registration API. Its SQL phase above remains real.
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
			s := fixture.Select("SELECT "+statisticsSettingsColumnsSQL+" FROM pg_settings WHERE name=$1", statisticsSettingsColumns, statisticsSettingsTypes, [][]any{want}, tc.name)
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { statisticsSettingsMetadata(t, rows, false) }
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
		}
	}
	fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT count(*) FROM pg_settings", []string{"count"}, []string{"INT8"}, [][]any{{int64(47)}}))
	for _, tc := range []struct{ query, raw string }{
		{"SET demo.real_time='1.23456789'", "1.23457"},
		{"SET demo.real_time='1500ms'", "1.5"},
	} {
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(tc.query, 0))
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT setting,unit,source,boot_val,reset_val FROM pg_settings WHERE name='demo.real_time'", []string{"setting", "unit", "source", "boot_val", "reset_val"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{tc.raw, "s", "session", "1", "1"}}))
	}
	for _, name := range []string{"demo.fixed", "demo.priv"} {
		code, message := "42501", fmt.Sprintf("permission denied to set parameter %q", name)
		if name == "demo.fixed" {
			code, message = "55P02", fmt.Sprintf("parameter %q cannot be changed", name)
		}
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.ImplicitError("SET "+name+"='invalid'", fixture.ControlsReferenceDiagnostic(code, message)))
	}
}
