package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCreateAggregateSQLCollation(t *testing.T) {
	for _, first := range []string{"POSIX", "C"} {
		t.Run(first+"_first", func(t *testing.T) {
			createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				createAggregateRun(t, p, b, c,
					fixture.Exec(`CREATE TABLE ca_body_collation(v text COLLATE "POSIX")`, 0),
					fixture.Exec("INSERT INTO ca_body_collation VALUES('z')", 1),
					fixture.Exec("CREATE FUNCTION ca_body_step(text,text) RETURNS text LANGUAGE SQL AS $$SELECT CASE WHEN $2 < v THEN $2 ELSE $1 END FROM ca_body_collation$$", 0),
					fixture.Exec("CREATE AGGREGATE ca_body_agg(text) (SFUNC=ca_body_step,STYPE=text,INITCOND='')", 0),
					fixture.Exec("CREATE FUNCTION ca_body_frozen_step(text,text) RETURNS text LANGUAGE SQL BEGIN ATOMIC SELECT CASE WHEN $2 < v THEN $2 ELSE $1 END FROM ca_body_collation; END", 0),
					fixture.Exec("CREATE AGGREGATE ca_body_frozen(text) (SFUNC=ca_body_frozen_step,STYPE=text,INITCOND='')", 0),
					fixture.Exec(`CREATE FUNCTION ca_body_override_step(text,text) RETURNS text LANGUAGE SQL AS $$SELECT CASE WHEN ($2 COLLATE "POSIX") < v THEN $2 ELSE $1 END FROM ca_body_collation$$`, 0),
					fixture.Exec("CREATE AGGREGATE ca_body_override(text) (SFUNC=ca_body_override_step,STYPE=text,INITCOND='')", 0))
				queries := make(map[string]string)
				held := make(map[string]fixture.HeldStatement)
				for _, aggregate := range []string{"ca_body_agg", "ca_body_frozen", "ca_body_override"} {
					for _, collation := range []string{"C", "POSIX"} {
						key := aggregate + "/" + collation
						query := `SELECT ` + aggregate + `(x COLLATE "` + collation + `") AS v FROM(VALUES('a'::text)) s(x)`
						queries[key] = query
						stmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						held[key] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					}
				}
				other := "C"
				if first == "C" {
					other = "POSIX"
				}
				// Each cold-start order and its returns alternate the SAME quoted
				// SFUNC through already-held statements and fresh direct/prepared
				// calls. A retained description for one collation must not mask the other.
				for _, collation := range []string{first, other, first, other, first} {
					for _, aggregate := range []string{"ca_body_agg", "ca_body_frozen", "ca_body_override"} {
						key := aggregate + "/" + collation
						query := queries[key]
						step := fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"a"}})
						if aggregate == "ca_body_agg" && collation == "C" {
							step = fixture.ImplicitError(query, fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly.", Context: `SQL function "ca_body_step" statement 1`})
							step.SelectRows, step.ErrorStage = true, "execute"
						}
						// Frozen SQL-standard bodies retain creation-analyzed
						// semantics; do not infer quoted-body inheritance for them.
						createAggregateBoth(t, p, b, c, step)
						fixture.RunStep(t, p.Ctx, held[key], b.Recorder, step)
					}
				}
				// With no transition invocation the quoted body's comparison is
				// not demanded, even though the aggregate input collation is C.
				for _, query := range []string{
					`SELECT ca_body_agg(x COLLATE "C") AS v FROM(VALUES('a'::text)) s(x) WHERE FALSE`,
					`SELECT ca_body_agg(x COLLATE "C") FILTER(WHERE FALSE) AS v FROM(VALUES('a'::text)) s(x)`,
				} {
					createAggregateBoth(t, p, b, c, fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{""}}))
				}
			})
		})
	}
}
