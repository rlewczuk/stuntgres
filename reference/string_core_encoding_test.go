//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"net/url"
	"testing"
	"time"
)

func TestReferenceStringCoreEncodingOracle(t *testing.T) {
	p := newReferencePair(t)
	const name = "string_core_latin1"
	if _, err := referenceAdmin.ExecContext(p.ctx, "CREATE DATABASE "+name+" WITH TEMPLATE template0 ENCODING 'LATIN1' LOCALE 'C'"); err != nil {
		t.Fatal(err)
	}
	var db *sql.DB
	// Close every LATIN1 connection before dropping; cleanup uses its own bounded context.
	t.Cleanup(func() {
		if db != nil {
			if err := db.Close(); err != nil {
				t.Error(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := referenceAdmin.ExecContext(ctx, "DROP DATABASE "+name); err != nil {
			t.Errorf("LATIN1 cleanup: %v", err)
		}
	})
	u, err := url.Parse(referenceDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err = sql.Open("stuntgres_reference_postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	recorder := &referenceRecorder{}
	referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SHOW server_encoding", []string{"server_encoding"}, []string{"TEXT"}, [][]any{{"LATIN1"}}))
	for _, q := range []string{"SELECT normalize('x')", "SELECT 'x' IS NORMALIZED", `SELECT "normalize"('x','bad')`, "SELECT is_normalized('x','bad')"} {
		tc := stringCoreDiagnosticCase{q, referenceDiagnostic{Code: "42601", Message: "Unicode normalization can only be performed if server encoding is UTF8"}, false}
		for _, prepared := range []bool{false, true} {
			referenceRunStep(t, p.ctx, db, recorder, stringCoreDiagnosticStep(tc, prepared))
		}
	}
	for _, tc := range []stringCoreReferenceCase{
		{`SELECT "normalize"(NULL,'bad') AS v`, nil, "TEXT"},
		{`SELECT "normalize"('x',NULL) AS v`, nil, "TEXT"},
		{"SELECT NULL::text IS NORMALIZED AS v", nil, "BOOL"},
		{"SELECT NULL::text IS NOT NORMALIZED AS v", nil, "BOOL"},
		{"SELECT is_normalized(NULL,'bad') AS v", nil, "BOOL"},
		{"SELECT is_normalized('x',NULL) AS v", nil, "BOOL"},
	} {
		for _, prepared := range []bool{false, true} {
			s := stringCoreSelect(tc)
			if prepared {
				s = referencePrepared(s)
			}
			referenceRunStep(t, p.ctx, db, recorder, s)
		}
	}
}
