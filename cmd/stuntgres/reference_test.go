package main

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/serverref"
	"testing"
	"time"
)

func TestServerReferenceOptions(t *testing.T) {
	clearConfigEnvironment(t)
	parse := func(t *testing.T, args ...string) serveOptions {
		t.Helper()
		c, err := parseCLI(args, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return c.options
	}
	t.Run("flags_and_reexec", func(t *testing.T) {
		o := parse(t, "serve", "-u", "users", "-L", "logs", "-q",
			"--reference", "app=postgres://bob:se%2Ccret@db.example:6543/remote?sslmode=require",
			"--reference", "other=postgresql://alice@localhost", "--reference-timeout", "5s")
		want := map[string]serverref.Config{
			"app":   {Spec: "app=postgres://bob:se%2Ccret@db.example:6543/remote?sslmode=require", Database: "app", Host: "db.example", Port: "6543", User: "bob", Password: "se,cret", PasswordSet: true, DBName: "remote", SSLMode: "require"},
			"other": {Spec: "other=postgresql://alice@localhost", Database: "other", Host: "localhost", Port: "5432", User: "alice", DBName: "other", SSLMode: "disable"},
		}
		if !reflect.DeepEqual(o.references, want) || o.referenceTimeout != 5*time.Second {
			t.Fatalf("%+v %v", o.references, o.referenceTimeout)
		}
		assertReexec(t, o)
	})
	t.Run("defaults", func(t *testing.T) {
		o := parse(t, "serve", "-u", "users")
		if o.references != nil || o.referenceTimeout != 30*time.Second {
			t.Fatalf("%+v %v", o.references, o.referenceTimeout)
		}
	})
	t.Run("environment", func(t *testing.T) {
		t.Setenv("STUNTGRES_REFERENCE", "a=postgres://u@h b=postgres://u@h:1/x")
		t.Setenv("STUNTGRES_REFERENCE_TIMEOUT", "2s")
		o := parse(t, "serve", "-u", "users", "-L", "logs", "-q")
		if got := serverref.Specs(o.references); !slices.Equal(got, []string{"a=postgres://u@h", "b=postgres://u@h:1/x"}) || o.referenceTimeout != 2*time.Second {
			t.Fatalf("%v %v", got, o.referenceTimeout)
		}
		o = parse(t, "serve", "-u", "users", "-L", "logs", "-q", "--reference", "c=postgres://u@h")
		if got := serverref.Specs(o.references); !slices.Equal(got, []string{"c=postgres://u@h"}) {
			t.Fatalf("flag over environment %v", got)
		}
	})
	t.Run("config", func(t *testing.T) {
		path := writeCLIConfig(t, t.TempDir(), "server.yaml", "userfile: users\nlogdir: logs\nquerylog: true\nreference:\n  - app=postgres://u@h/x\nreference-timeout: 1m\n")
		o := parse(t, "serve", "--config", path)
		if got := serverref.Specs(o.references); !slices.Equal(got, []string{"app=postgres://u@h/x"}) || o.referenceTimeout != time.Minute {
			t.Fatalf("%v %v", got, o.referenceTimeout)
		}
	})
	base := []string{"serve", "-u", "users", "-L", "logs", "-q"}
	for _, extra := range [][]string{
		{"--reference", "noequals"},
		{"--reference", "=postgres://u@h"},
		{"--reference", "app="},
		{"--reference", "../app=postgres://u@h"},
		{"--reference", "app=mysql://u@h"},
		{"--reference", "app=postgres://h"},
		{"--reference", "app=postgres://u@"},
		{"--reference", "app=postgres://u@h:0"},
		{"--reference", "app=postgres://u@h:x"},
		{"--reference", "app=postgres://u@h/a/b"},
		{"--reference", "app=postgres://u@h?sslmode=prefer"},
		{"--reference", "app=postgres://u@h?sslmode=disable&sslmode=require"},
		{"--reference", "app=postgres://u@h?application_name=x"},
		{"--reference", "app=postgres://u@h", "--reference", "app=postgres://v@h"},
		{"--reference-timeout", "0s"},
		{"--reference-timeout", "soon"},
	} {
		t.Run("reject_"+joinCLIName(extra), func(t *testing.T) {
			if c, err := parseCLI(append(slices.Clone(base), extra...), &bytes.Buffer{}); err == nil {
				t.Fatalf("accepted %v: %+v", extra, c.options)
			}
		})
	}
	t.Run("requires_querylog", func(t *testing.T) {
		if _, err := parseCLI([]string{"serve", "-u", "users", "--reference", "app=postgres://u@h"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "querylog") {
			t.Fatal(err)
		}
	})
	t.Run("redacted_rejection", func(t *testing.T) {
		_, err := parseCLI(append(slices.Clone(base), "--reference", "app=postgres://u:topsecret@h?bad=1"), &bytes.Buffer{})
		if err == nil || strings.Contains(err.Error(), "topsecret") {
			t.Fatal(err)
		}
	})
	t.Run("child_environment", func(t *testing.T) {
		got := childEnvironment([]string{"STUNTGRES_REFERENCE=a", "STUNTGRES_REFERENCE_TIMEOUT=1s", "PGPASSFILE=/p"})
		if !slices.Equal(got, []string{"PGPASSFILE=/p"}) {
			t.Fatal(got)
		}
	})
}
