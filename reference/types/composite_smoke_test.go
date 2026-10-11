package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeFeatureSmoke(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE it AS (i integer,t text)`, `CREATE TYPE tt AS (a text,b text)`, `CREATE TYPE nested AS (p it,t tt)`, `CREATE TABLE holder(k integer,v nested)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,'("(7,foo)","("""",)")'),(2,'("(,)","(,)")')`, 2))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO holder VALUES ($1,$2)`, 1, 3, nil)))
		for _, prepared := range []bool{false, true} {
			fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{`("(7,foo)","("""",)")`}, {`("(,)","(,)")`}, {nil}}, prepared)
			fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder WHERE k=0`, [][]any{}, prepared)
		}
		want := fixture.CompositeReferenceDiagnostic("22P02", `invalid input syntax for type integer: "oops"`)
		q := `INSERT INTO holder VALUES (4,'("(oops,x,y)",)')`
		want.Position = fixture.CompositeReferenceLiteralPosition(q)
		fixture.CompositeReferenceError(t, p, c, q, "prepare", want)
		want = fixture.CompositeReferenceDiagnostic("22P02", `malformed record literal: "(x)"`)
		want.Detail = "Missing left parenthesis."
		want.Context = `unnamed portal parameter $2 = '...'`
		// The first nested field is decoded as x, then its child input fails.
		want.Message = `malformed record literal: "x"`
		fixture.CompositeReferenceError(t, p, c, `INSERT INTO holder VALUES ($1,$2)`, "execute", want, 4, "(x)")
		fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{`("(7,foo)","("""",)")`}, {`("(,)","(,)")`}, {nil}}, false)
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE constrained_source(id integer NOT NULL CHECK(id>0),note text)`, `CREATE TABLE standalone(k integer,v constrained_source NOT NULL)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO standalone VALUES (1,$1),(2,$2)`, 2, "(-1,negative)", "(,)")))
		fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT v FROM standalone ORDER BY k`, []string{"v"}, []string{""}, [][]any{{"(-1,negative)"}, {"(,)"}}))
	})
}
