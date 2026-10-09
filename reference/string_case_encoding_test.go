package reference_test

import (
	"context"
	"database/sql"
	"net/url"
	"testing"
	"time"
)

func TestReferenceStringCaseEncoding(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		stringCaseBoth(t, p, b, c, referenceSelect("SHOW server_encoding", []string{"server_encoding"}, []string{"TEXT"}, [][]any{{"UTF8"}}))
		if !b.pg {
			d := controlsReferenceDiagnostic("55P02", `parameter "server_encoding" cannot be changed`)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceErrorDiagnostic("SET server_encoding='LATIN1'", d))
			return
		}
		// Explicit PostgreSQL-only encoding control: native encoding is fixed UTF8.
		const name = "sc_case_latin1"
		if _, err := referenceAdmin.ExecContext(p.ctx, "CREATE DATABASE "+name+" TEMPLATE template0 ENCODING 'LATIN1' LC_COLLATE 'C' LC_CTYPE 'C'"); err != nil {
			t.Fatal(err)
		}
		var db *sql.DB
		var target *sql.Conn
		t.Cleanup(func() {
			if target != nil {
				if err := target.Close(); err != nil {
					t.Error(err)
				}
			}
			if db != nil {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := referenceAdmin.ExecContext(ctx, "DROP DATABASE "+name); err != nil {
				t.Error(err)
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
		target, err = db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		control := referenceBackend{name: "postgres-latin1", db: db, recorder: &referenceRecorder{}, pg: true}
		stringCaseBoth(t, p, control, target, referenceSelect("SHOW server_encoding", []string{"server_encoding"}, []string{"TEXT"}, [][]any{{"LATIN1"}}))
		stringCaseBoth(t, p, control, target, referenceSelect("SELECT lower('ABC'),upper('abc'),initcap('hi THERE'),casefold(NULL::text)", []string{"lower", "upper", "initcap", "casefold"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"abc", "ABC", "Hi There", nil}}))
		d := controlsReferenceDiagnostic("42601", "Unicode case folding can only be performed if server encoding is UTF8")
		for _, q := range []string{"SELECT casefold('ABC')", "SELECT casefold('')"} {
			stringCaseFailure(t, p, control, target, q, d, false)
		}
		q := "SELECT casefold($1) AS v"
		stmt, err := target.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: target, statement: stmt, query: q}
		referenceRunStep(t, p.ctx, held, control.recorder, referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}, nil))
		for _, arg := range []any{"ABC", ""} {
			s := referenceErrorDiagnostic(q, d, arg)
			s.selectRows = true
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, held, control.recorder, s)
		}
	})
}
