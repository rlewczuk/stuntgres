package stuntgres

import (
	"sync"
	"testing"

	"stuntgres/internal/engine/settings"
)

func TestPasswordCheckRegistration(t *testing.T) {
	for _, zero := range []bool{false, true} {
		var d *Driver
		if zero { d = new(Driver) } else { d = NewDriver() }
		for _, before := range []bool{true, false} {
			if !before { if err := d.RegisterPasswordCheck(); err != nil { t.Fatal(err) } }
			for _, name := range []string{"passwordcheck.min_password_length", "PASSWORDCHECK.fake", "passwordcheck.other"} {
				if err := d.RegisterSetting(SettingDefinition{Name: name, Type: SettingInteger, IntegerMax: 100}); err == nil { t.Fatal("reserved source-owned prefix", name, before) }
			}
		}
		if err := d.RegisterPasswordCheck(); err == nil { t.Fatal("duplicate activation") }
		for _, dsn := range []string{"one", "two", "one"} {
			c, err := d.OpenConnector(dsn)
			if err != nil { t.Fatal(err) }
			e := c.(*Connector).engine
			def, err := e.Settings.Lookup(settings.PasswordCheckMinimum)
			if err != nil || def.BootValue().Number != 8 || def.Format(def.DefaultValue()) != "8B" || def.Access != settings.Privileged || def.Unit != settings.Bytes { t.Fatal("trusted immutable default", def, err) }
		}
		if err := d.RegisterPasswordCheck(); err == nil { t.Fatal("late activation") }
		peer := NewDriver()
		c, err := peer.OpenConnector("one")
		if err != nil { t.Fatal(err) }
		if _, active := c.(*Connector).engine.Settings.Definitions()[settings.PasswordCheckMinimum]; active { t.Fatal("independent Driver activation leaked") }
		if err := peer.RegisterPasswordCheck(); err == nil { t.Fatal("first connector must freeze without Ping") }
	}
	t.Run("concurrent first connector", func(t *testing.T) {
		for range 12 {
			d := new(Driver)
			start := make(chan struct{})
			var wg sync.WaitGroup
			var registerErr, openErr error
			wg.Add(2)
			go func() { defer wg.Done(); <-start; registerErr = d.RegisterPasswordCheck() }()
			go func() { defer wg.Done(); <-start; _, openErr = d.OpenConnector("race") }()
			close(start)
			wg.Wait()
			if openErr != nil { t.Fatal(openErr) }
			_, active := d.engines["race"].Settings.Definitions()[settings.PasswordCheckMinimum]
			if active != (registerErr == nil) { t.Fatal("nonlinear registration", active, registerErr) }
			if err := d.RegisterPasswordCheck(); err == nil { t.Fatal("frozen registry mutated") }
		}
	})
	t.Run("concurrent duplicate", func(t *testing.T) {
		d := NewDriver()
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 { wg.Add(1); go func() { defer wg.Done(); results <- d.RegisterPasswordCheck() }() }
		wg.Wait()
		close(results)
		successes := 0
		for err := range results { if err == nil { successes++ } }
		if successes != 1 || len(d.settings) != 1 { t.Fatal("duplicate registration did not linearize", successes, len(d.settings)) }
	})
}
