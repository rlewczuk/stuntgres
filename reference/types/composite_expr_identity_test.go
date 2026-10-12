package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Assignment must distinguish anonymous RECORD construction from an already
// named datum, even when the bound expression still retains a ROW constructor.
func TestReferenceCompositeExprNamedAssignmentIdentity(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c,
			`CREATE TYPE pair_a AS (i integer,t text)`,
			`CREATE TYPE pair_b AS (i integer,t text)`,
			`CREATE TYPE outer_a AS (p pair_a)`,
			`CREATE DOMAIN pair_a_domain AS pair_a`,
			`CREATE TABLE destination(v pair_a)`,
			`CREATE TABLE nested_destination(v outer_a)`,
			`CREATE TABLE named_source(i integer,t text)`,
			`CREATE TABLE identity_sentinel(k integer,t text)`)
		for _, s := range []fixture.Step{
			fixture.Exec(`INSERT INTO destination VALUES(ROW(9,'sentinel'))`, 1),
			fixture.Exec(`INSERT INTO nested_destination VALUES(ROW(ROW(9,'nested')))`, 1),
			fixture.Exec(`INSERT INTO named_source VALUES(3,'source')`, 1),
			fixture.Exec(`INSERT INTO identity_sentinel VALUES(7,'untouched')`, 1),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		// These complete logical records and Prepare phases were independently
		// observed by root on pinned PostgreSQL18.0 (tests-188).
		for _, x := range []struct {
			q, code, message, detail, hint, position string
		}{
			{`INSERT INTO destination(v) SELECT ROW(1, 'x')::pair_b`, "42804", `column "v" is of type pair_a but expression is of type pair_b`, "", `You will need to rewrite or cast the expression.`, "46"},
			{`UPDATE destination SET v = ROW(2, 'y')::pair_b`, "42804", `column "v" is of type pair_a but expression is of type pair_b`, "", `You will need to rewrite or cast the expression.`, "39"},
			{`INSERT INTO nested_destination(v) SELECT ROW(ROW(1,'x')::pair_b)`, "42846", `cannot cast type record to outer_a`, "", "", "42"},
			{`UPDATE nested_destination SET v = ROW(ROW(2,'y')::pair_b)`, "42846", `cannot cast type record to outer_a`, `Cannot cast type pair_b to pair_a in column 1.`, "", "49"},
			{`INSERT INTO destination(v) SELECT named_source FROM named_source`, "42804", `column "v" is of type pair_a but expression is of type named_source`, "", `You will need to rewrite or cast the expression.`, "35"},
			{`UPDATE destination SET v = named_source FROM named_source`, "42804", `column "v" is of type pair_a but expression is of type named_source`, "", `You will need to rewrite or cast the expression.`, "28"},
			{`INSERT INTO destination(v) SELECT ROW(4,'anon')`, "42846", `cannot cast type record to pair_a`, "", "", "35"},
			{`INSERT INTO destination(v) SELECT ROW(4,'anon'::text)`, "42846", `cannot cast type record to pair_a`, "", "", "35"},
			{`INSERT INTO destination(v) SELECT s FROM (VALUES(11,'derived'::text)) s(i,t)`, "42846", `cannot cast type record to pair_a`, "", "", "35"},
			{`INSERT INTO destination(v) SELECT s FROM (VALUES(1)) a(k) LEFT JOIN (VALUES(11,'derived'::text)) s(i,t) ON false`, "42846", `cannot cast type record to pair_a`, "", "", "35"},
		} {
			t.Run(x.q, func(t *testing.T) {
				d := fixture.CompositeReferenceDiagnostic(x.code, x.message)
				d.Detail, d.Hint, d.Position = x.detail, x.hint, x.position
				fixture.CompositeReferenceError(t, p, c, x.q, "prepare", d)
				// A successful read on the very same connection also witnesses
				// recovery after all failed direct/prepared result/handle routes.
				compositeExprBoth(t, p, b, c, `SELECT v::text AS v FROM destination`, []string{"v"}, []string{"TEXT"}, [][]any{{"(9,sentinel)"}})
				compositeExprBoth(t, p, b, c, `SELECT ((v).p)::text AS p FROM nested_destination`, []string{"p"}, []string{"TEXT"}, [][]any{{"(9,nested)"}})
				compositeExprBoth(t, p, b, c, `SELECT k,t FROM identity_sentinel`, []string{"k", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "untouched"}})
			})
		}

		fixture.CompositeReferenceSetup(t, p, b, c,
			`CREATE TABLE domain_destination(v pair_a_domain)`,
			`CREATE TABLE named_destination(k integer,v named_source)`,
			`CREATE DOMAIN named_source_domain AS named_source`,
			`CREATE TABLE named_domain_destination(k integer,v named_source_domain)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO domain_destination VALUES(NULL)`, 1))
		// Anonymous ROW is admitted in a direct assignment context, not through
		// INSERT SELECT, even with the explicitly typed TEXT member above.
		for _, prepared := range []bool{false, true} {
			for _, x := range []struct {
				q    string
				want any
			}{
				{`UPDATE destination SET v = ROW(4,'anon'::text)`, "(4,anon)"},
				{`UPDATE destination SET v = ROW(5,'same')::pair_a`, "(5,same)"},
				{`UPDATE destination SET v = ROW(6,'domain')::pair_a_domain`, "(6,domain)"},
				{`UPDATE destination SET v = NULL::pair_a`, nil},
				{`UPDATE destination SET v = NULL::pair_a_domain`, nil},
				{`UPDATE destination SET v = ROW(NULL::integer,NULL::text)::pair_a`, "(,)"},
				// Unlike a named datum, anonymous whole-row conversion expands
				// the absent source's cells into a present all-NULL constructor.
				{`UPDATE destination SET v=s FROM (VALUES(11,'derived'::text)) s(i,t)`, "(11,derived)"},
				{`UPDATE destination SET v=s FROM (VALUES(1)) a(k) LEFT JOIN (VALUES(11,'derived'::text)) s(i,t) ON false`, "(,)"},
			} {
				s := fixture.Exec(x.q, 1)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				nulls := int64(0)
				if x.want == nil {
					nulls = 1
				}
				compositeExprBoth(t, p, b, c, `SELECT v::text AS v,num_nulls(v) AS n FROM destination`, []string{"v", "n"}, []string{"TEXT", "INT4"}, [][]any{{x.want, nulls}})
			}
			for _, x := range []struct {
				q    string
				want any
			}{
				{`INSERT INTO destination(v) VALUES(ROW(4,'anon'::text))`, "(4,anon)"},
				{`INSERT INTO destination(v) SELECT ROW(5,'same')::pair_a`, "(5,same)"},
				{`INSERT INTO destination(v) SELECT ROW(6,'domain')::pair_a_domain`, "(6,domain)"},
				{`INSERT INTO destination(v) SELECT NULL::pair_a`, nil},
				{`INSERT INTO destination(v) SELECT NULL::pair_a_domain`, nil},
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DELETE FROM destination`, 1))
				s := fixture.Exec(x.q, 1)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				compositeExprBoth(t, p, b, c, `SELECT v::text AS v FROM destination`, []string{"v"}, []string{"TEXT"}, [][]any{{x.want}})
			}
			for _, x := range []struct {
				q    string
				want any
			}{
				{`UPDATE domain_destination SET v = ROW(8,'base')::pair_a`, "(8,base)"},
				{`UPDATE domain_destination SET v = ROW(9,'domain')::pair_a_domain`, "(9,domain)"},
				{`UPDATE domain_destination SET v = NULL::pair_a`, nil},
				{`UPDATE domain_destination SET v = NULL::pair_a_domain`, nil},
			} {
				s := fixture.Exec(x.q, 1)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				compositeExprBoth(t, p, b, c, `SELECT v::text AS v FROM domain_destination`, []string{"v"}, []string{"TEXT"}, [][]any{{x.want}})
			}
			// Same-identity whole-row assignment must preserve absence, not
			// rebuild missing source fields into a non-NULL ROW(NULL,NULL).
			for _, table := range []string{"named_destination", "named_domain_destination"} {
				s := fixture.Exec(`INSERT INTO `+table+` SELECT a.k,s FROM (VALUES(1),(2)) a(k) LEFT JOIN named_source s ON a.k=1`, 2)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				compositeExprBoth(t, p, b, c, `SELECT k,v::text AS v,num_nulls(v) AS n FROM `+table+` ORDER BY k`, []string{"k", "v", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "(3,source)", int64(0)}, {int64(2), nil, int64(1)}})
				s = fixture.Exec(`UPDATE `+table+` d SET v=s FROM (VALUES(1),(2)) a(k) LEFT JOIN named_source s ON a.k=2 WHERE d.k=a.k`, 2)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				compositeExprBoth(t, p, b, c, `SELECT k,v::text AS v,num_nulls(v) AS n FROM `+table+` ORDER BY k`, []string{"k", "v", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), nil, int64(1)}, {int64(2), "(3,source)", int64(0)}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DELETE FROM `+table, 2))
			}
		}
	})
}
