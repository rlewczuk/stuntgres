package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSettingsRegistryValidationAndLinearization(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{"on"}}))
	}
	// Native host registration/control phase; PostgreSQL has no RegisterSetting API.
	for _, bad := range []stuntgres.SettingDefinition{
		{Name: "demo.value", Type: stuntgres.SettingString, IntegerMax: 1},
		{Name: "demo.value", Type: stuntgres.SettingInteger, FloatMax: 1},
		{Name: "demo.value", Type: stuntgres.SettingFloating, FloatMin: math.NaN()},
		{Name: "demo.value", Type: stuntgres.SettingBoolean, Default: "o"},
		{Name: "demo.value", Type: stuntgres.SettingInteger, Default: "1"},
		{Name: "demo.value", Type: stuntgres.SettingFloating, Default: "NaN", FloatMax: 10},
		{Name: "demo.value", Type: stuntgres.SettingEnum, Default: "bad", EnumValues: []string{"ok"}},
		{Name: "demo.value", Type: stuntgres.SettingEnum, EnumValues: []string{"ok", ""}},
		{Name: "demo.value", Type: stuntgres.SettingString, Description: "bad\x00description"},
		{Name: "demo.value", Type: stuntgres.SettingString, Default: "bad\x00default"},
		{Name: "demo.value", Type: stuntgres.SettingString, Description: string([]byte{0xff})},
	} {
		d := stuntgres.NewDriver()
		err := d.RegisterSetting(bad)
		var diagnostic *stuntgres.Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("registration boundary", bad, err)
		}
		if err = d.RegisterSetting(stuntgres.SettingDefinition{Name: "demo.value", Type: stuntgres.SettingString, Default: "owned"}); err != nil {
			t.Fatal("failed publication reserved name", err)
		}
		db := sessionPool(t, d, t.Name())
		c, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		settingsRows(t, c, "SHOW demo.value", false, "demo.value", "owned")
		c.Close()
	}
	d := stuntgres.NewDriver()
	for _, name := range []string{"démo.值", "demo.deep.$bad", "demo.deep.a$1"} {
		err := d.RegisterSetting(stuntgres.SettingDefinition{Name: name, Type: stuntgres.SettingString, Default: "seed"})
		if name == "demo.deep.$bad" {
			if err == nil {
				t.Fatal("invalid component initial accepted")
			}
		} else if err != nil {
			t.Fatal(name, err)
		}
	}
	for range 20 {
		d := stuntgres.NewDriver()
		start := make(chan struct{})
		registered := make(chan error, 1)
		opened := make(chan driver.Connector, 1)
		go func() {
			<-start
			registered <- d.RegisterSetting(stuntgres.SettingDefinition{Name: "demo.race", Type: stuntgres.SettingString, Default: "winner"})
		}()
		go func() {
			<-start
			c, err := d.OpenConnector("linearized")
			if err != nil {
				opened <- nil
				return
			}
			opened <- c
		}()
		close(start)
		publication := <-registered
		connector := <-opened
		if connector == nil {
			t.Fatal("connector failed")
		}
		db := sql.OpenDB(connector)
		c, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if publication == nil {
			settingsRows(t, c, "SHOW demo.race", false, "demo.race", "winner")
		} else {
			_, err = c.ExecContext(context.Background(), "SHOW demo.race")
			settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "demo.race"`, "", "", "")
		}
		c.Close()
		db.Close()
	}
}

func TestReferenceSettingsShowAllCurrentOverrides(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{"on"}}))
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, prepared := range []bool{false, true} {
			for _, setting := range []string{"on", "off"} {
				step := fixture.Exec("SET quote_all_identifiers="+setting, 0)
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
				step = fixture.Select("SHOW quote_all_identifiers", []string{"quote_all_identifiers"}, []string{"TEXT"}, [][]any{{setting}})
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
			}
		}
	}
	// Native host registration/control phase; PostgreSQL has no RegisterSetting API.
	db := sessionPool(t, settingsTestDriver(t), "show-all-current")
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		expected := map[string]string{"demo.flag": "on", "demo.text": "current", "demo.count": "4", "demo.ratio": "2.12346", "demo.mode": "fast", "demo.memory": "2MB"}
		for name, value := range map[string]string{
			"application_name": "", "client_min_messages": "notice",
			"lock_timeout": "0", "log_statement_stats": "off",
			"plpgsql.check_asserts": "on",
			"search_path":           `"$user", public`, "server_encoding": "UTF8",
			"track_commit_timestamp":      "off",
			"standard_conforming_strings": "on", "statement_timeout": "0",
			"quote_all_identifiers": "off",
			"client_encoding":       "UTF8", "lc_messages": "C", "lc_monetary": "C",
			"lc_numeric": "C", "lc_time": "C", "icu_validation_level": "warning", "extra_float_digits": "1",
		} {
			expected[name] = value
		}
		for _, def := range compatibleSettings {
			expected[def.name] = def.initial
		}
		for _, def := range physicalSettings {
			expected[def.name] = def.value
		}
		for _, tc := range []struct{ name, input string }{{"demo.flag", "YES"}, {"demo.text", "current"}, {"demo.count", "3.5"}, {"demo.ratio", "2.1234567"}, {"demo.mode", "FAST"}, {"demo.memory", "2048"}} {
			sessionSet(t, c, "SET "+tc.name+" TO '"+tc.input+"'", prepared)
		}
		var rows *sql.Rows
		if prepared {
			s, e := c.PrepareContext(ctx, "SHOW ALL")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			r, e := s.ExecContext(ctx)
			if e != nil {
				t.Fatal(e)
			}
			n, e := r.RowsAffected()
			if e != nil || n != 0 {
				t.Fatal(n, e)
			}
			rows, err = s.QueryContext(ctx)
		} else {
			r, e := c.ExecContext(ctx, "SHOW ALL")
			if e != nil {
				t.Fatal(e)
			}
			n, e := r.RowsAffected()
			if e != nil || n != 0 {
				t.Fatal(n, e)
			}
			rows, err = c.QueryContext(ctx, "SHOW ALL")
		}
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name, value, description string
			if err = rows.Scan(&name, &value, &description); err != nil {
				t.Fatal(err)
			}
			if want, ok := expected[name]; ok {
				if value != want {
					t.Fatal(name, value, want)
				}
				delete(expected, name)
			}
		}
		if rows.Err() != nil || len(expected) != 0 {
			t.Fatal(rows.Err(), expected)
		}
		rows.Close()
		sessionSet(t, c, "RESET ALL", prepared)
	}
}

func TestReferenceSettingsAuthenticatedAccessClasses(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{"on"}}))
	}
	// Native host registration/control phase; PostgreSQL has no RegisterSetting API.
	ctx := context.Background()
	d := settingsTestDriver(t)
	adminPool := settingsAuthenticatedPool(t, d, t.Name(), "postgres")
	admin, err := adminPool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	settingsAuthenticatedCommand(t, admin, "CREATE USER settings_reader", 0, false)
	readerPool := settingsAuthenticatedPool(t, d, t.Name(), "settings_reader")
	reader, err := readerPool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, principal := range []struct {
		name string
		conn *sql.Conn
	}{{"postgres", admin}, {"settings_reader", reader}} {
		settingsAuthenticatedIdentity(t, principal.conn, principal.name, nil)
		var sessionUser, currentUser string
		if err := principal.conn.QueryRowContext(ctx, "SELECT session_user,current_user").Scan(&sessionUser, &currentUser); err != nil {
			t.Fatal(err)
		}
		if sessionUser != principal.name || currentUser != principal.name {
			t.Fatalf("authenticated identity got %q/%q want %q", sessionUser, currentUser, principal.name)
		}
		for _, definition := range []struct{ name, value, code, message string }{
			{"server_encoding", "UTF8", "55P02", `parameter "server_encoding" cannot be changed`},
			{"log_statement_stats", "off", "42501", `permission denied to set parameter "log_statement_stats"`},
			{"demo.fixed", "locked", "55P02", `parameter "demo.fixed" cannot be changed`},
			{"demo.priv", "off", "42501", `permission denied to set parameter "demo.priv"`},
		} {
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{
					"SET " + definition.name + " TO garbage",
					"SET " + definition.name + " TO DEFAULT",
					"RESET " + definition.name,
					"SELECT set_config('" + definition.name + "','garbage',false)",
				} {
					if prepared {
						statement, prepareErr := principal.conn.PrepareContext(ctx, query)
						if prepareErr != nil {
							t.Fatal(prepareErr)
						}
						_, err = statement.ExecContext(ctx)
						statement.Close()
					} else {
						_, err = principal.conn.ExecContext(ctx, query)
					}
					settingsDiagnostic(t, err, definition.code, definition.message, "", "", "")
					settingsRows(t, principal.conn, "SHOW "+definition.name, prepared, definition.name, definition.value)
				}
			}
		}
		settingsAuthenticatedCommand(t, principal.conn, "RESET ALL", 0, true)
		settingsRows(t, principal.conn, "SHOW log_statement_stats", false, "log_statement_stats", "off")
		settingsRows(t, principal.conn, "SHOW demo.priv", false, "demo.priv", "off")
		settingsAuthenticatedIdentity(t, principal.conn, principal.name, nil)
	}
}
