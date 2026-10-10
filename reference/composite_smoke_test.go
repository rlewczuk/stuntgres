package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceCompositeFeatureSmoke(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		compositeReferenceSetup(t, p, b, c, `CREATE TYPE it AS (i integer,t text)`, `CREATE TYPE tt AS (a text,b text)`, `CREATE TYPE nested AS (p it,t tt)`, `CREATE TABLE holder(k integer,v nested)`)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO holder VALUES (1,'("(7,foo)","("""",)")'),(2,'("(,)","(,)")')`, 2))
		referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(referenceExec(`INSERT INTO holder VALUES ($1,$2)`, 1, 3, nil)))
		for _, prepared := range []bool{false, true} {
			compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{`("(7,foo)","("""",)")`}, {`("(,)","(,)")`}, {nil}}, prepared)
			compositeReferenceRows(t, p, b, c, `SELECT v FROM holder WHERE k=0`, [][]any{}, prepared)
		}
		want := compositeReferenceDiagnostic("22P02", `invalid input syntax for type integer: "oops"`)
		q := `INSERT INTO holder VALUES (4,'("(oops,x,y)",)')`
		want.Position = compositeReferenceLiteralPosition(q)
		compositeReferenceError(t, p, c, q, "prepare", want)
		want = compositeReferenceDiagnostic("22P02", `malformed record literal: "(x)"`)
		want.Detail = "Missing left parenthesis."
		want.Context = `unnamed portal parameter $2 = '...'`
		// The first nested field is decoded as x, then its child input fails.
		want.Message = `malformed record literal: "x"`
		compositeReferenceError(t, p, c, `INSERT INTO holder VALUES ($1,$2)`, "execute", want, 4, "(x)")
		compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{`("(7,foo)","("""",)")`}, {`("(,)","(,)")`}, {nil}}, false)
		compositeReferenceSetup(t, p, b, c, `CREATE TABLE constrained_source(id integer NOT NULL CHECK(id>0),note text)`, `CREATE TABLE standalone(k integer,v constrained_source NOT NULL)`)
		referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(referenceExec(`INSERT INTO standalone VALUES (1,$1),(2,$2)`, 2, "(-1,negative)", "(,)")))
		enumReferenceRunStep(t, p.ctx, c, b.recorder, referenceSelect(`SELECT v FROM standalone ORDER BY k`, []string{"v"}, []string{""}, [][]any{{"(-1,negative)"}, {"(,)"}}))
	})
}
