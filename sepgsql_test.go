package stuntgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func publicSepgsqlPolicy(install bool) SepgsqlSimulationConfig {
	return SepgsqlSimulationConfig{InstallSQL: install, Policy: SepgsqlPolicy{
		ID: "public-owner", Version: "1", Contexts: []string{"A", "R", "P", "S", "U"}, InitialContext: "A", UnlabeledContext: "U",
		DefaultSpec: "db_database * P\ndb_schema * P\ndb_table * P\ndb_column * P\ndb_sequence * P\ndb_view * P\ndb_procedure * P\n",
		NamedSpecs:  map[string]string{"secret": "db_table * S"}, Translations: []SepgsqlTranslation{{Raw: "P", Translated: "Public display"}},
		Rules: []SepgsqlRule{{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}}, {Source: "R", Target: "P", Class: "db_database", Allow: []string{"access"}}, {Source: "R", Target: "P", Class: "db_schema", Allow: []string{"search"}}, {Source: "R", Target: "P", Class: "db_table", Allow: []string{"select"}}, {Source: "R", Target: "P", Class: "db_column", Allow: []string{"select"}}, {Source: "R", Target: "P", Class: "db_procedure", Allow: []string{"execute"}}},
	}}
}
func publicSepgsqlConnector(t *testing.T, d *Driver, name string) *Connector {
	t.Helper()
	c, err := d.OpenConnector(name)
	if err != nil {
		t.Fatal(err)
	}
	return c.(*Connector)
}
func TestSepgsqlSimulationControls(t *testing.T) {
	t.Run("invalid_and_duplicate_registration_are_atomic", func(t *testing.T) {
		d := NewDriver()
		invalid := publicSepgsqlPolicy(true)
		invalid.Policy.InitialContext = "missing"
		err := d.RegisterSepgsqlSimulation(invalid)
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) || d.sepgsql != nil || len(d.settings) != 0 || d.settingsFrozen {
			t.Fatal("invalid registration published partial state", err)
		}
		if err = d.RegisterSepgsqlSimulation(publicSepgsqlPolicy(true)); err != nil {
			t.Fatal(err)
		}
		original, count := d.sepgsql, len(d.settings)
		replacement := publicSepgsqlPolicy(false)
		replacement.Policy.Version = "2"
		err = d.RegisterSepgsqlSimulation(replacement)
		if err == nil || err.Error() != "stuntgres: duplicate sepgsql simulation registration" || errors.As(err, &state) || d.sepgsql != original || len(d.settings) != count || d.sepgsql.Version() != "1" || !d.sepgsql.InstallSQL() {
			t.Fatal("duplicate registration mutated established policy", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d := NewDriver()
	config := publicSepgsqlPolicy(true)
	if err := d.RegisterSepgsqlSimulation(config); err != nil {
		t.Fatal(err)
	}
	config.Policy.Contexts[0] = "tampered"
	config.Policy.Rules[0].Allow[0] = "drop"
	config.Policy.Translations[0].Translated = "tampered"
	config.Policy.NamedSpecs["secret"] = "db_table * P"
	base := publicSepgsqlConnector(t, d, "sepgsql_public_owner")
	selected := base.WithSepgsqlContext("R")
	if selected == base || base.explicitSepgsqlContext || base.sepgsqlContext != "" || !selected.explicitSepgsqlContext || selected.sepgsqlContext != "R" {
		t.Fatal("context selection mutated original connector")
	}
	for _, test := range []struct {
		connector *Connector
		want      string
	}{{base, "A"}, {selected, "R"}} {
		raw, err := test.connector.Connect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		native := raw.(*conn)
		if native.engine != base.engine || native.session.Sepgsql == nil || native.session.Sepgsql.Original() != test.want || native.session.Sepgsql.Current() != test.want {
			raw.Close()
			t.Fatal("physical subject or engine attachment lost")
		}
		if err = raw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if translated := d.sepgsql.Translate("P", false); translated != "Public display" {
		t.Fatal("registered translations borrowed input", translated)
	}
	profile := "secret"
	label, matched := d.sepgsql.InitialLabel(&profile, "db_table", "any.table")
	if !matched || label != "S" {
		t.Fatal("registered virtual spec borrowed input", label, matched)
	}
	if err := d.RegisterSepgsqlSimulation(publicSepgsqlPolicy(true)); err == nil {
		t.Fatal("frozen registration changed live policy")
	}
	unknown := base.WithSepgsqlContext("unknown")
	if c, err := unknown.Connect(ctx); err == nil {
		c.Close()
		t.Fatal("undeclared context connected")
	} else if err.Error() != "stuntgres: invalid simulated sepgsql connection context" {
		t.Fatal(err)
	}
	inactive := publicSepgsqlConnector(t, NewDriver(), "sepgsql_inactive_owner").WithSepgsqlContext("A")
	if c, err := inactive.Connect(ctx); err == nil {
		c.Close()
		t.Fatal("explicit subject enabled inactive provider")
	} else if err.Error() != "stuntgres: sepgsql context without registered simulation" {
		t.Fatal(err)
	}
}

func TestSepgsqlSimulationCleanup(t *testing.T) {
	t.Run("registration_and_first_connector_are_one_freeze_boundary", func(t *testing.T) {
		for range 12 {
			d := NewDriver()
			start := make(chan struct{})
			registered, opened := make(chan error, 1), make(chan error, 1)
			var workers sync.WaitGroup
			workers.Add(2)
			go func() {
				defer workers.Done()
				<-start
				registered <- d.RegisterSepgsqlSimulation(publicSepgsqlPolicy(true))
			}()
			go func() { defer workers.Done(); <-start; _, err := d.OpenConnector("sepgsql_freeze"); opened <- err }()
			close(start)
			workers.Wait()
			registrationErr, openErr := <-registered, <-opened
			if openErr != nil {
				t.Fatal("first connector failed", openErr)
			}
			d.mu.Lock()
			frozen := d.settingsFrozen
			runtime := d.sepgsql
			definitions := 0
			for _, definition := range d.settings {
				if definition.Name == "sepgsql.permissive" {
					definitions++
				}
			}
			d.mu.Unlock()
			if !frozen || (runtime == nil && definitions != 0) || (runtime != nil && definitions != 1) {
				t.Fatal("registration published a partial frozen module", frozen, runtime != nil, definitions)
			}
			if runtime != nil && registrationErr != nil {
				t.Fatal("failed registration published runtime", registrationErr)
			}
			if runtime == nil && (registrationErr == nil || registrationErr.Error() != "stuntgres: sepgsql registration after first connector") {
				t.Fatal("freeze lost registration without its precise error", registrationErr)
			}
			if err := d.RegisterSepgsqlSimulation(publicSepgsqlPolicy(true)); err == nil {
				t.Fatal("late policy registration escaped freeze")
			}
		}
	})
}
