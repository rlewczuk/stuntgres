package serverref

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestServerReferenceParser(t *testing.T) {
	t.Run("display_hides_password", func(t *testing.T) {
		c, err := Parse("app=postgres://u:topsecret@h:7/db")
		if err != nil || c.Display() != "postgres://u@h:7/db" {
			t.Fatalf("%q %v", c.Display(), err)
		}
	})
	if got := redactedSpec("app=postgres://u:topsecret@h"); strings.Contains(got, "topsecret") {
		t.Fatal(got)
	}
	t.Run("grammar and immutable values", func(t *testing.T) {
		c, err := Parse("app=postgresql://alice:p%40ss@[::1]:6543/remote?sslmode=verify-full")
		if err != nil {
			t.Fatal(err)
		}
		if c.Database != "app" || c.User != "alice" || c.Password != "p@ss" || !c.PasswordSet || c.Host != "::1" || c.Port != "6543" || c.DBName != "remote" || c.SSLMode != "verify-full" || c.Address() != "[::1]:6543" {
			t.Fatalf("%+v", c)
		}
		defaults, err := Parse("app=postgres://alice@localhost")
		if err != nil || defaults.PasswordSet || defaults.Port != "5432" || defaults.DBName != "app" || defaults.SSLMode != "disable" {
			t.Fatalf("%+v %v", defaults, err)
		}
		for _, spec := range []string{"noequals", "=postgres://u@h", "app=", "../app=postgres://u@h", "app=mysql://u@h", "app=postgres://h", "app=postgres://u@", "app=postgres://u@h:0", "app=postgres://u@h:65536", "app=postgres://u@h:x", "app=postgres://u@h/a/b", "app=postgres://u@h?sslmode=prefer", "app=postgres://u@h?sslmode=disable&sslmode=require", "app=postgres://u:topsecret@h?application_name=x"} {
			if _, err := Parse(spec); err == nil || strings.Contains(err.Error(), "topsecret") {
				t.Fatalf("accepted invalid or exposed password for %q: %v", spec, err)
			}
		}
		all, err := ParseAll([]string{"b=postgres://u@h", "a=postgres://u@h"})
		if err != nil || !slices.Equal(Specs(all), []string{"a=postgres://u@h", "b=postgres://u@h"}) {
			t.Fatal(all, err)
		}
		if _, err := ParseAll([]string{"app=postgres://u@h", "app=postgres://v@h"}); err == nil {
			t.Fatal("duplicate database accepted")
		}
		if err := ValidateTimeout(0); err == nil {
			t.Fatal("zero timeout accepted")
		}
		if err := ValidateTimeout(-time.Second); err == nil {
			t.Fatal("negative timeout accepted")
		}
		if err := ValidateTimeout(DefaultTimeout); err != nil {
			t.Fatal(err)
		}
	})
}
