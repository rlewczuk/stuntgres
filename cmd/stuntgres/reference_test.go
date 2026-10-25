package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
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
		want := map[string]referenceConfig{
			"app":   {spec: "app=postgres://bob:se%2Ccret@db.example:6543/remote?sslmode=require", database: "app", host: "db.example", port: "6543", user: "bob", password: "se,cret", passwordSet: true, dbname: "remote", sslmode: "require"},
			"other": {spec: "other=postgresql://alice@localhost", database: "other", host: "localhost", port: "5432", user: "alice", dbname: "other", sslmode: "disable"},
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
		if got := referenceSpecs(o.references); !slices.Equal(got, []string{"a=postgres://u@h", "b=postgres://u@h:1/x"}) || o.referenceTimeout != 2*time.Second {
			t.Fatalf("%v %v", got, o.referenceTimeout)
		}
		o = parse(t, "serve", "-u", "users", "-L", "logs", "-q", "--reference", "c=postgres://u@h")
		if got := referenceSpecs(o.references); !slices.Equal(got, []string{"c=postgres://u@h"}) {
			t.Fatalf("flag over environment %v", got)
		}
	})
	t.Run("config", func(t *testing.T) {
		path := writeCLIConfig(t, t.TempDir(), "server.yaml", "userfile: users\nlogdir: logs\nquerylog: true\nreference:\n  - app=postgres://u@h/x\nreference-timeout: 1m\n")
		o := parse(t, "serve", "--config", path)
		if got := referenceSpecs(o.references); !slices.Equal(got, []string{"app=postgres://u@h/x"}) || o.referenceTimeout != time.Minute {
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
		if got := redactedSpec("app=postgres://u:topsecret@h"); strings.Contains(got, "topsecret") {
			t.Fatal(got)
		}
	})
	t.Run("display_hides_password", func(t *testing.T) {
		c, err := parseReference("app=postgres://u:topsecret@h:7/db")
		if err != nil || c.display() != "postgres://u@h:7/db" {
			t.Fatalf("%q %v", c.display(), err)
		}
	})
	t.Run("unknown_database", func(t *testing.T) {
		references, err := parseReferences([]string{"absent=postgres://u@h"})
		if err != nil {
			t.Fatal(err)
		}
		if err := attachReferences(map[string]*database{"app": {}}, references); err == nil {
			t.Fatal("reference for an unknown database accepted")
		}
		known := map[string]*database{"absent": {}}
		if err := attachReferences(known, references); err != nil || known["absent"].reference == nil || known["absent"].reference.user != "u" {
			t.Fatalf("%v %+v", err, known["absent"].reference)
		}
	})
	t.Run("startup_error", func(t *testing.T) {
		d := diagnostic(backendError(diagnosticBody(*referenceFailure("28P01", "password authentication failed"), "FATAL")))
		if d.Code != "28P01" || d.Severity != "FATAL" || d.Message != "password authentication failed" {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("child_environment", func(t *testing.T) {
		got := childEnvironment([]string{"STUNTGRES_REFERENCE=a", "STUNTGRES_REFERENCE_TIMEOUT=1s", "PGPASSFILE=/p"})
		if !slices.Equal(got, []string{"PGPASSFILE=/p"}) {
			t.Fatal(got)
		}
	})
}

func TestServerReferencePassfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pgpass")
	body := strings.Join([]string{
		"# comment",
		"",
		"malformed:line",
		"other:5432:*:bob:wrong",
		`db\:host:5432:app:bob:escaped\:pass\\word`,
		"*:6543:*:bob:wild:card",
		"*:*:*:*:fallback",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ host, port, db, user, want string }{
		{"db:host", "5432", "app", "bob", `escaped:pass\word`},
		{"h", "6543", "x", "bob", "wild:card"},
		{"other", "5432", "x", "carol", "fallback"},
	} {
		got, ok, err := lookupPassfile(path, tc.host, tc.port, tc.db, tc.user)
		if err != nil || !ok || got != tc.want {
			t.Fatalf("%+v: %q %v %v", tc, got, ok, err)
		}
	}
	if _, ok, err := lookupPassfile(filepath.Join(dir, "missing"), "h", "1", "d", "u"); ok || err != nil {
		t.Fatalf("missing file: %v %v", ok, err)
	}
	t.Setenv("PGPASSFILE", path)
	c, err := parseReference("app=postgres://bob@h:6543/x")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := referencePassword(&c); err != nil || !ok || got != "wild:card" {
		t.Fatalf("passfile: %q %v %v", got, ok, err)
	}
	c, err = parseReference("app=postgres://bob:url@h:6543/x")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := referencePassword(&c); err != nil || !ok || got != "url" {
		t.Fatalf("URL password wins: %q %v %v", got, ok, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := lookupPassfile(path, "h", "1", "d", "u"); err == nil {
			t.Fatal("group/world-readable password file accepted")
		}
	}
}

// TestServerReferenceSCRAM uses the RFC 7677 SCRAM-SHA-256 example exchange.
func TestServerReferenceSCRAM(t *testing.T) {
	const serverFirst = "r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096"
	s := &scramClient{user: "user", password: "pencil", nonce: "rOprNGfwEbeRWgbNEkqO"}
	if got := s.first(); got != "n,,n=user,r=rOprNGfwEbeRWgbNEkqO" {
		t.Fatal(got)
	}
	final, err := s.final(serverFirst)
	if err != nil || final != "c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,p=dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ=" {
		t.Fatalf("%q %v", final, err)
	}
	if err := s.verify("v=6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4="); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"v=AAAA", "e=invalid-proof", ""} {
		if err := s.verify(bad); err == nil {
			t.Fatalf("server final %q accepted", bad)
		}
	}
	for _, bad := range []string{
		"r=rOprNGfwEbeRWgbNEkqO,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096",
		"r=other,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096",
		"r=rOprNGfwEbeRWgbNEkqOx,s=!,i=4096",
		"r=rOprNGfwEbeRWgbNEkqOx,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=0",
	} {
		s := &scramClient{user: "user", password: "pencil", nonce: "rOprNGfwEbeRWgbNEkqO"}
		s.first()
		if _, err := s.final(bad); err == nil {
			t.Fatalf("server first %q accepted", bad)
		}
	}
}
