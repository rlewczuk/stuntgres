package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceSettingsPreparedBindingAndStages(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{"on"}}))
	}
	// Native host registration/control phase; PostgreSQL has no RegisterSetting API.
	ctx := context.Background()
	db := sessionPool(t, settingsTestDriver(t), "binding")
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, q := range []string{"SHOW demo.text", "SELECT current_setting('demo.text')", "SELECT current_setting($1)"} {
		s, err := a.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		sessionSet(t, a, "SET demo.text TO changed", false)
		var value string
		if strings.Contains(q, "$1") {
			err = s.QueryRowContext(ctx, "demo.text").Scan(&value)
		} else {
			err = s.QueryRowContext(ctx).Scan(&value)
		}
		s.Close()
		if err != nil || value != "changed" {
			t.Fatal(q, value, err)
		}
		sessionSet(t, a, "RESET demo.text", false)
	}
	for _, q := range []string{"SET demo.text TO prepared", "SELECT set_config('demo.text','prepared',false)"} {
		s, err := a.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		settingsRows(t, a, "SHOW demo.text", false, "demo.text", "seed")
		if _, err = s.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		s.Close()
		settingsRows(t, a, "SHOW demo.text", false, "demo.text", "prepared")
		sessionSet(t, a, "RESET demo.text", false)
	}
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ query, signature string }{
				{"SELECT current_setting()", "current_setting()"},
				{"SELECT current_setting('demo.text',true)", ""},
				{"SELECT set_config('demo.text','v')", "set_config(unknown, unknown)"},
				{"SELECT current_setting(true)", "current_setting(boolean)"},
			} {
				step := referenceErrorDiagnostic(tc.query, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsupported setting function signature", Position: "8"})
				if backend.pg {
					if tc.signature == "" {
						step = referenceSelect(tc.query, []string{"current_setting"}, []string{"TEXT"}, [][]any{{nil}})
					} else {
						step = referenceErrorDiagnostic(tc.query, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
					}
				}
				step.prepared = prepared
				step.selectRows = true
				step.errorStage = "execute"
				if prepared {
					step.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
			}
		}
	}
	_, err = a.PrepareContext(ctx, "SHOW absent_setting")
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "absent_setting"`, "", "", "")
	s, err := a.PrepareContext(ctx, "SET absent_setting TO v")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ExecContext(ctx)
	s.Close()
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "absent_setting"`, "", "", "")
	for _, args := range [][]any{{}, {1, 2}, {sql.Named("name", "demo.text")}} {
		_, err = a.ExecContext(ctx, "SELECT current_setting($1)", args...)
		var d *stuntgres.Error
		if err == nil || errors.As(err, &d) {
			t.Fatalf("adapter arguments %#v: %v", args, err)
		}
	}
}

func TestReferenceSettingsEveryUnitBase(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{"on"}}))
	}
	// Native host registration/control phase; PostgreSQL has no RegisterSetting API.
	d := stuntgres.NewDriver()
	for _, v := range []stuntgres.SettingDefinition{{Name: "demo.mb", Type: stuntgres.SettingInteger, Unit: stuntgres.SettingMegabytes, Default: "1", IntegerMax: math.MaxInt32}, {Name: "demo.minutes", Type: stuntgres.SettingInteger, Unit: stuntgres.SettingMinutes, Default: "1", IntegerMax: math.MaxInt32}} {
		if err := d.RegisterSetting(v); err != nil {
			t.Fatal(err)
		}
	}
	db := sessionPool(t, d, "units")
	a, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	sessionSet(t, a, "SET demo.mb TO '1GB'", true)
	settingsRows(t, a, "SHOW demo.mb", true, "demo.mb", "1GB")
	sessionSet(t, a, "SET demo.minutes TO '2h'", true)
	settingsRows(t, a, "SHOW demo.minutes", true, "demo.minutes", "2h")
}

func TestReferenceSettingsControlsDefaultOnParser(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{"on"}}))
	}
	// Native host registration/control phase; PostgreSQL has no RegisterSetting API.
	ctx := context.Background()
	db := sessionPool(t, settingsTestDriver(t), "controls")
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	sessionSet(t, a, "SET standard_conforming_strings TO off", false)
	sessionSet(t, a, "SET demo.text TO retained", false)
	if err = stuntgres.Restore(ctx, db, strings.NewReader(`CREATE TABLE literals(v TEXT); INSERT INTO literals VALUES('a\nb')`)); err != nil {
		t.Fatal(err)
	}
	var v string
	if err = a.QueryRowContext(ctx, "SELECT v FROM literals").Scan(&v); err != nil || v != `a\nb` {
		t.Fatal(v, err)
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	settingsRows(t, a, "SHOW demo.text", false, "demo.text", "retained")
	settingsRows(t, a, "SHOW standard_conforming_strings", false, "standard_conforming_strings", "off")
	if err = a.QueryRowContext(ctx, "SELECT v FROM literals").Scan(&v); err != nil || v != `a\nb` {
		t.Fatal(v, err)
	}
}

func TestReferenceSettingsNumericPrefixEdges(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{"on"}}))
	}
	// Native host registration/control phase; PostgreSQL has no RegisterSetting API.
	ctx := context.Background()
	d := settingsTestDriver(t)
	for _, def := range []stuntgres.SettingDefinition{{Name: "edge.count", Type: stuntgres.SettingInteger, Default: "0", IntegerMin: -200, IntegerMax: 200}, {Name: "edge.real", Type: stuntgres.SettingFloating, Default: "0", FloatMin: -200, FloatMax: 200}} {
		if err := d.RegisterSetting(def); err != nil {
			t.Fatal(err)
		}
	}
	db := sessionPool(t, d, "numeric-prefix")
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ name, input, want string }{
			{"edge.count", "010", "8"}, {"edge.count", "0xA", "10"}, {"edge.count", "010.5", "10"},
			{"edge.count", "010e1", "100"}, {"edge.count", "0x1.8p1", "3"}, {"edge.count", ".5", "0"}, {"edge.count", " 2.5 ", "2"}, {"edge.count", "9223372036854775808e-18", "9"}, {"edge.count", "0e-999", "0"},
			{"edge.real", "010", "10"}, {"edge.real", "0xA", "10"}, {"edge.real", "010.5", "10.5"}, {"edge.real", "010e1", "100"}, {"edge.real", "08.5", "8.5"}, {"edge.real", "0x1p0", "1"}, {"edge.real", "+.5", "0.5"}, {"edge.real", "-.5", "-0.5"}, {"edge.real", " .5", "0.5"}, {"edge.real", "0e-999", "0"}, {"edge.real", "0x1p-1074", "4.94066e-324"},
		} {
			sessionSet(t, c, "SET "+tc.name+" TO '"+tc.input+"'", prepared)
			settingsRows(t, c, "SHOW "+tc.name, prepared, tc.name, tc.want)
			settingsRows(t, c, "SELECT current_setting($1)", prepared, "current_setting", tc.want, tc.name)
			settingsRows(t, c, "SELECT set_config($1,$2,false)", prepared, "set_config", tc.want, tc.name, tc.input)
		}
		for _, name := range []string{"edge.count", "edge.real"} {
			inputs := []string{"0b10", "0o10", "1_000", "1e", "0x", "1e-999", "1e999", "NaN"}
			if name == "edge.count" {
				inputs = append(inputs, "+.5", "-.5", " .5", "08.5", "0x1p0", "Infinity", "-inf", "0x1p-1074")
			} else {
				inputs = append(inputs, "0x1.1p-1074", "1e-320")
			}
			for _, input := range inputs {
				settingsRows(t, c, "SELECT set_config($1,'1',false)", prepared, "set_config", "1", name)
				q := "SELECT set_config($1,$2,false)"
				if prepared {
					s, e := c.PrepareContext(ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = s.ExecContext(ctx, name, input)
					s.Close()
				} else {
					_, err = c.ExecContext(ctx, q, name, input)
				}
				settingsDiagnostic(t, err, "22023", `invalid value for parameter "`+name+`": "`+input+`"`, "", "", "")
				settingsRows(t, c, "SHOW "+name, prepared, name, "1")
			}
		}
		for _, input := range []string{"08.5", "0x1p0"} {
			_, err = c.ExecContext(ctx, "SELECT set_config('statement_timeout',$1,false)", input)
			settingsDiagnostic(t, err, "22023", `invalid value for parameter "statement_timeout": "`+input+`"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`, "", "")
		}
		_, err = c.ExecContext(ctx, "SELECT set_config('demo.ratio','-inf',false)")
		settingsDiagnostic(t, err, "22023", `-Infinity is outside the valid range for parameter "demo.ratio" (0 .. 10)`, "", "", "")
		settingsRows(t, c, "SHOW demo.ratio", prepared, "demo.ratio", "1")
		sessionSet(t, c, "SET demo.ratio TO '2.1234567'", prepared)
		for _, tc := range []struct{ input, prefix string }{{"Infinity", "Infinity"}, {"-inf", "-Infinity"}} {
			const query = "SELECT set_config('demo.ratio',$1,false)"
			if prepared {
				statement, prepareErr := c.PrepareContext(ctx, query)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				_, err = statement.ExecContext(ctx, tc.input)
				statement.Close()
			} else {
				_, err = c.ExecContext(ctx, query, tc.input)
			}
			settingsDiagnostic(t, err, "22023", tc.prefix+` is outside the valid range for parameter "demo.ratio" (0 .. 10)`, "", "", "")
			settingsRows(t, c, "SHOW demo.ratio", prepared, "demo.ratio", "2.12346")
		}
		sessionSet(t, c, "RESET demo.ratio", prepared)
		for _, name := range []string{"edge.count", "edge.real"} {
			sessionSet(t, c, "SET "+name+" TO DEFAULT", prepared)
			sessionSet(t, c, "RESET "+name, prepared)
			settingsRows(t, c, "SELECT set_config($1,NULL,NULL)", prepared, "set_config", "0", name)
			settingsRows(t, c, "SHOW "+name, prepared, name, "0")
		}
	}
}
