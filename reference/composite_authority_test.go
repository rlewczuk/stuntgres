package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceCompositeIdentifierClipping(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ *sql.Conn) {
		var notices []referenceDiagnostic
		_, c, r := noticeReferencePool(t, p, b.pg, &notices)
		b.recorder = r
		long := strings.Repeat("é", 32)
		clipped := strings.Repeat("é", 31)
		q := `CREATE TYPE "` + long + `" AS ("` + long + `" text)`
		referenceRunStep(t, p.ctx, c, r, referenceExec(q, 0))
		want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + long + `" will be truncated to "` + clipped + `"`}
		if len(notices) != 2 {
			t.Errorf("clipping notices got %#v want two %#v", notices, want)
		}
		for _, got := range notices {
			if got != want {
				t.Errorf("clipping notice got %#v want %#v", got, want)
			}
		}
		compositeReferenceSetup(t, p, b, c, `CREATE TABLE holder(v "`+clipped+`")`)
		referenceRunStep(t, p.ctx, c, r, referenceExec(`INSERT INTO holder VALUES ('(word)')`, 1))
		compositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{"(word)"}}, false)
		wantError := compositeReferenceDiagnostic("42701", `column "`+clipped+`" specified more than once`)
		compositeReferenceError(t, p, c, `CREATE TYPE bad AS ("`+long+`" text,"`+clipped+`" integer)`, "execute", wantError)
	})
}

func TestReferenceCompositeAuthority(t *testing.T) {
	roleReferenceCleanup(t, []string{"composite_reader"})
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		compositeReferenceSetup(t, p, b, c, `CREATE SCHEMA composite_private`, `CREATE TYPE composite_private.child AS ENUM ('ok')`, `CREATE ROLE composite_reader`)
		defer func() {
			c.ExecContext(p.ctx, `RESET ROLE`)
			c.ExecContext(p.ctx, `DROP SCHEMA composite_private CASCADE`)
		}()
		compositeReferenceSetup(t, p, b, c, `SET ROLE composite_reader`)
		want := compositeReferenceDiagnostic("42501", `permission denied for schema composite_private`)
		compositeReferenceError(t, p, c, `CREATE TYPE composite_private.denied AS (x integer)`, "execute", want)
		compositeReferenceSetup(t, p, b, c, `RESET ROLE`)
		// External ACL setup is not new native GRANT/REVOKE syntax.
		if b.pg {
			compositeReferenceSetup(t, p, b, c, `GRANT USAGE,CREATE ON SCHEMA composite_private TO composite_reader`, `REVOKE USAGE ON TYPE composite_private.child FROM PUBLIC`, `SET ROLE composite_reader`)
			want = compositeReferenceDiagnostic("42501", `permission denied for type composite_private.child`)
			compositeReferenceError(t, p, c, `CREATE TYPE composite_private.denied AS (x composite_private.child)`, "execute", want)
			compositeReferenceSetup(t, p, b, c, `RESET ROLE`)
		}
	})
}
