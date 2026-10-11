package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeIdentifierClipping(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = r
		long := strings.Repeat("é", 32)
		clipped := strings.Repeat("é", 31)
		q := `CREATE TYPE "` + long + `" AS ("` + long + `" text)`
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
		want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + long + `" will be truncated to "` + clipped + `"`}
		if len(notices) != 2 {
			t.Errorf("clipping notices got %#v want two %#v", notices, want)
		}
		for _, got := range notices {
			if got != want {
				t.Errorf("clipping notice got %#v want %#v", got, want)
			}
		}
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE holder(v "`+clipped+`")`)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(`INSERT INTO holder VALUES ('(word)')`, 1))
		fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{"(word)"}}, false)
		wantError := fixture.CompositeReferenceDiagnostic("42701", `column "`+clipped+`" specified more than once`)
		fixture.CompositeReferenceError(t, p, c, `CREATE TYPE bad AS ("`+long+`" text,"`+clipped+`" integer)`, "execute", wantError)
	})
}

func TestReferenceCompositeAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"composite_reader"})
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE SCHEMA composite_private`, `CREATE TYPE composite_private.child AS ENUM ('ok')`, `CREATE ROLE composite_reader`)
		defer func() {
			c.ExecContext(p.Ctx, `RESET ROLE`)
			c.ExecContext(p.Ctx, `DROP SCHEMA composite_private CASCADE`)
		}()
		fixture.CompositeReferenceSetup(t, p, b, c, `SET ROLE composite_reader`)
		want := fixture.CompositeReferenceDiagnostic("42501", `permission denied for schema composite_private`)
		fixture.CompositeReferenceError(t, p, c, `CREATE TYPE composite_private.denied AS (x integer)`, "execute", want)
		fixture.CompositeReferenceSetup(t, p, b, c, `RESET ROLE`)
		// External ACL setup is not new native GRANT/REVOKE syntax.
		if b.PG {
			fixture.CompositeReferenceSetup(t, p, b, c, `GRANT USAGE,CREATE ON SCHEMA composite_private TO composite_reader`, `REVOKE USAGE ON TYPE composite_private.child FROM PUBLIC`, `SET ROLE composite_reader`)
			want = fixture.CompositeReferenceDiagnostic("42501", `permission denied for type composite_private.child`)
			fixture.CompositeReferenceError(t, p, c, `CREATE TYPE composite_private.denied AS (x composite_private.child)`, "execute", want)
			fixture.CompositeReferenceSetup(t, p, b, c, `RESET ROLE`)
		}
	})
}
