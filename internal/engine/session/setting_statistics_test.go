package session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

type shortDefaults map[string]string
func (p shortDefaults) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) { v, ok := p[d.Name]; return v, ok, nil }

func TestStatisticsEffectiveCandidatesAndCachedReset(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	for _, defaultOn := range []string{"log_statement_stats", "log_parser_stats", "log_planner_stats", "log_executor_stats"} {
		t.Run(defaultOn, func(t *testing.T) {
			s := &SettingsStore{Defaults: map[string]string{}, Values: map[string]string{}}
			if err := s.Initialize(context.Background(), registry.Definitions(), registry.Order(), shortDefaults{defaultOn: "on"}); err != nil { t.Fatal(err) }
			peer := "log_statement_stats"; if defaultOn == peer { peer = "log_parser_stats" }
			d, _ := registry.Lookup(peer)
			beforeDefaults := s.DefaultTyped[defaultOn]
			if err := s.checkStatistics(d.Name, &settings.Value{Boolean: true}); err == nil { t.Fatal("ignored captured default") }
			if len(s.Values) != 0 || len(s.Typed) != 0 || len(s.Actions) != 0 || len(s.Placeholders) != 0 || !reflect.DeepEqual(s.DefaultTyped[defaultOn], beforeDefaults) { t.Fatal("failed validation mutated store") }
			s.Values[defaultOn], s.Typed[defaultOn] = "off", settings.Value{Boolean: false}
			if err := s.checkStatistics(peer, &settings.Value{Boolean: true}); err != nil { t.Fatal(err) }
			s.Values[peer], s.Typed[peer] = "on", settings.Value{Boolean: true}
			// Cached reset restores on, even though the effective peer is now on.
			if err := s.checkStatistics(defaultOn, nil); err != nil { t.Fatal("reset reran check hook", err) }
			delete(s.Values, defaultOn); delete(s.Typed, defaultOn)
			for _, name := range []string{peer, defaultOn} {
				err := s.checkStatistics(name, &settings.Value{Boolean: true})
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != `invalid value for parameter "`+name+`": 1` { t.Fatal(name, err) }
				if err := s.checkStatistics(name, &settings.Value{Boolean: false}); err != nil { t.Fatal("false escape refused", err) }
			}
			// RESET ALL restores captured defaults together, without sequential hooks.
			before := s.override(peer)
			s.ResetAllLocked(registry)
			if !s.TypedValue(defaultOn).Boolean || s.TypedValue(peer).Boolean || len(s.Values) != 0 || len(s.Typed) != 0 { t.Fatal("reset all did not restore cached defaults") }
			if !reflect.DeepEqual(s.Actions[peer].original, before) { t.Fatal("reset not journaled", s.Actions) }
			s.FinishTransaction(false)
			if !s.TypedValue(peer).Boolean || !s.TypedValue(defaultOn).Boolean { t.Fatal("rollback revalidated cached state") }
		})
	}
}
