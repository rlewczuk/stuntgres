package stuntgres_test

import (
	"errors"
	"path/filepath"
	"testing"

	"stuntgres"
)

func TestIdentFileStartupAPI(t *testing.T) {
	for _, newDriver := range []func() *stuntgres.Driver{stuntgres.NewDriver, func() *stuntgres.Driver { return new(stuntgres.Driver) }} {
		d := newDriver()
		for _, path := range []string{"", "relative.conf", "/tmp/ident\x00.conf"} {
			err := d.ConfigureIdentFile(path)
			if err == nil {
				t.Fatal("invalid path accepted", path)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("host registration has SQLSTATE", err)
			}
		}
		// Missing is deliberately not read at registration or connector construction.
		missing := filepath.Join(t.TempDir(), "not-created.conf")
		if err := d.ConfigureIdentFile(missing); err != nil {
			t.Fatal(err)
		}
		if err := d.ConfigureIdentFile(missing); err == nil {
			t.Fatal("duplicate registration accepted")
		}
		if _, err := d.OpenConnector("first"); err != nil {
			t.Fatal(err)
		}
		if err := d.ConfigureIdentFile(missing); err == nil {
			t.Fatal("frozen registration accepted")
		}
		if _, err := d.OpenConnector("second"); err != nil {
			t.Fatal(err)
		}
	}
	d := stuntgres.NewDriver()
	if _, err := d.OpenConnector(""); err != nil {
		t.Fatal(err)
	}
	if err := d.ConfigureIdentFile(filepath.Join(t.TempDir(), "late")); err == nil {
		t.Fatal("late first attachment accepted")
	}
}
