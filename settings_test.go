package stuntgres

import (
	"sync"
	"testing"
)

func TestSettingsConcurrentFreeze(t *testing.T) {
	for range 20 {
		d := NewDriver()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = d.RegisterSetting(SettingDefinition{Name: "demo.race", Type: SettingString})
		}()
		go func() { defer wg.Done(); _, _ = d.OpenConnector("race") }()
		wg.Wait()
		if err := d.RegisterSetting(SettingDefinition{Name: "demo.late", Type: SettingString}); err == nil {
			t.Fatal("not frozen")
		}
	}
}
