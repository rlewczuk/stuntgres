package pgserver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"stuntgres/internal/serverref"
	"testing"
)

func TestServerReferenceCore(t *testing.T) {
	t.Run("unknown_database", func(t *testing.T) {
		references, err := serverref.ParseAll([]string{"absent=postgres://u@h"})
		if err != nil {
			t.Fatal(err)
		}
		if err := attachReferences(map[string]*database{"app": {}}, references); err == nil {
			t.Fatal("reference for an unknown database accepted")
		}
		known := map[string]*database{"absent": {}}
		if err := attachReferences(known, references); err != nil || known["absent"].reference == nil || known["absent"].reference.User != "u" {
			t.Fatalf("%v %+v", err, known["absent"].reference)
		}
	})
	t.Run("startup_error", func(t *testing.T) {
		d := diagnostic(backendError(diagnosticBody(*referenceFailure("28P01", "password authentication failed"), "FATAL")))
		if d.Code != "28P01" || d.Severity != "FATAL" || d.Message != "password authentication failed" {
			t.Fatalf("%+v", d)
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
	c, err := serverref.Parse("app=postgres://bob@h:6543/x")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := referencePassword(&c); err != nil || !ok || got != "wild:card" {
		t.Fatalf("passfile: %q %v %v", got, ok, err)
	}
	c, err = serverref.Parse("app=postgres://bob:url@h:6543/x")
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
