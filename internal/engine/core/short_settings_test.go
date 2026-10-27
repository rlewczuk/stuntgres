package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// Provider injection is internal-only. SQL here drives the publication owner;
// assertions concern captured typed state, override/journal ownership and resets.
func TestShortStatisticsProviderResetOwnership(t *testing.T) {
	for _, defaultOn := range []string{"log_statement_stats", "log_parser_stats", "log_planner_stats", "log_executor_stats"} {
		for _, reset := range []string{"RESET ", "DEFAULT", "NULL"} {
			t.Run(defaultOn+"/"+reset, func(t *testing.T) {
				e := engine.New()
				p := &settingsDefaultProviderForTest{values: map[string]string{defaultOn: "on"}, calls: map[string]int{}}
				s := sessionStart(t, e, p)
				peer := "log_statement_stats"
				if defaultOn == peer {
					peer = "log_parser_stats"
				}
				if !s.Settings.DefaultTyped[defaultOn].Boolean || !s.Settings.ProviderDefaults[defaultOn] {
					t.Fatal("provider default not captured")
				}
				p.values[defaultOn] = "off"
				sessionSQL(t, s, "SET "+defaultOn+"=off")
				sessionSQL(t, s, "SET "+peer+"=on")
				sessionSQL(t, s, "BEGIN")
				sessionSQL(t, s, "SAVEPOINT short_provider_save")
				query := "RESET " + defaultOn
				if reset == "DEFAULT" {
					query = "SET " + defaultOn + "=DEFAULT"
				}
				if reset == "NULL" {
					query = "SELECT set_config('" + defaultOn + "',NULL,false)"
				}
				sessionSQL(t, s, query)
				if !s.Settings.TypedValue(defaultOn).Boolean || !s.Settings.TypedValue(peer).Boolean {
					t.Fatal("cached reset revalidated peers")
				}
				if _, present := s.Settings.Values[defaultOn]; present {
					t.Fatal("reset retained override")
				}
				sessionSQL(t, s, "ROLLBACK TO short_provider_save")
				if s.Settings.TypedValue(defaultOn).Boolean || !s.Settings.TypedValue(peer).Boolean || len(s.Settings.Actions) != 0 {
					t.Fatal("savepoint failed to restore overrides/journal")
				}
				sessionSQL(t, s, query)
				sessionSQL(t, s, "ROLLBACK")
				if s.Settings.TypedValue(defaultOn).Boolean || !s.Settings.TypedValue(peer).Boolean {
					t.Fatal("rollback lost entry overrides")
				}
				sessionSQL(t, s, query)
				for _, name := range []string{defaultOn, peer} {
					beforeValues, beforeTyped := s.Settings.Values[name], s.Settings.Typed[name]
					op, err := s.Acquire(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					_, err = e.Execute(op, enginetest.Statement(t, "SET "+name+"=on"), 0, nil)
					op.Done()
					var d *sqlstate.Error
					if !errors.As(err, &d) || d.Code != "22023" || d.Message != `invalid value for parameter "`+name+`": 1` {
						t.Fatal(name, err)
					}
					if s.Settings.Values[name] != beforeValues || !reflect.DeepEqual(s.Settings.Typed[name], beforeTyped) || len(s.Settings.Actions) != 0 {
						t.Fatal("failed assignment published journal/state")
					}
				}
				sessionSQL(t, s, "BEGIN")
				sessionSQL(t, s, "RESET ALL")
				if !s.Settings.TypedValue(defaultOn).Boolean || s.Settings.TypedValue(peer).Boolean {
					t.Fatal("reset all did not restore captured defaults")
				}
				sessionSQL(t, s, "ROLLBACK")
				if !s.Settings.TypedValue(defaultOn).Boolean || !s.Settings.TypedValue(peer).Boolean {
					t.Fatal("rollback reran check hook")
				}
				sessionSQL(t, s, "SET "+defaultOn+"=off")
				sessionSQL(t, s, "RESET ALL")
				if len(s.Settings.Values) != 0 || len(s.Settings.Typed) != 0 || !s.Settings.TypedValue(defaultOn).Boolean || s.Settings.TypedValue(peer).Boolean {
					t.Fatal("reset all retained overrides")
				}
				for _, name := range e.Settings.Order() {
					if p.calls[name] != 1 {
						t.Fatal("provider reentered", name, p.calls[name])
					}
				}
				replacement := sessionStart(t, e, p)
				if replacement.Settings.DefaultTyped[defaultOn].Boolean {
					t.Fatal("new session failed to capture new provider default")
				}
			})
		}
	}
}

// Check failures must precede Touch for an admitted operation, including LOCAL.
func TestShortStatisticsFailedPublication(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "SET log_statement_stats=on")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	d, _ := e.Settings.Lookup("log_parser_stats")
	value, _ := d.Parse("on")
	e.Mu.Lock()
	s.Settings.Mu.Lock()
	s.Mu.Lock()
	_, err = s.Settings.ApplyLocked(op, core.DomainOf(e), e.Settings, d, &value, nil, session.SettingLocal)
	s.Mu.Unlock()
	s.Settings.Mu.Unlock()
	e.Mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || len(s.Settings.Actions) != 0 || len(s.Settings.Values) != 1 || len(s.Settings.Typed) != 1 || len(s.Settings.Placeholders) != 0 {
		t.Fatal("failure published state", err, s.Settings)
	}
}
