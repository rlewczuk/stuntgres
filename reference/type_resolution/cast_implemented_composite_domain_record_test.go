package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// RECORD passthrough recognizes domains over named composites without replacing
// their logical identity. Array elements follow the same released ISCOMPLEX rule.
func TestReferenceCastImplementedCompositeDomainRecord(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			// RECORD is a lawful typename shortcut, not an executable handler.
			// Keep actual RECORD metadata; no outer TEXT cast hides its result.
			for _, tc := range []struct {
				expression string
				value      any
			}{
				{"record(ROW(1,'x'))", "(1,x)"},
				{"record(NULL::record)", nil},
				{"record(NULL::text)", nil},
			} {
				s := castResultStep(t, "SELECT "+tc.expression+" AS v", castInventoryType("cast_anonymous_record"), tc.value, b.PG)
				run(s)
				run(fixture.Prepared(s))
			}
			for _, form := range []string{"CAST('(1,x)'::text AS record)", "record('(1,x)'::text)"} {
				s := fixture.ImplicitError("SELECT "+form+" AS v", fixture.Diagnostic{Code: "0A000", Message: "input of anonymous composite types is not implemented"})
				s.SelectRows = true
				s.ErrorStage = "execute"
				castWitnessRoutes(t, p, b, s)
			}
			for _, tc := range []struct {
				expression string
				diagnostic fixture.Diagnostic
			}{
				{"record(NULL::integer)", fixture.Diagnostic{Code: "42883", Message: "function record(integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
				{"record('x')", fixture.Diagnostic{Code: "0A000", Message: "input of anonymous composite types is not implemented", Position: "15"}},
			} {
				s := fixture.ImplicitError("SELECT "+tc.expression+" AS v", tc.diagnostic)
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				s.ErrorStage = "prepare"
				run(fixture.Prepared(s))
			}
			for _, q := range []string{
				"CREATE TYPE cast_record_pair AS (n integer,s text)",
				"CREATE DOMAIN cast_record_domain AS cast_record_pair",
				"CREATE DOMAIN cast_record_nested AS cast_record_domain",
				"CREATE TABLE cast_record_rows(id integer,v cast_record_domain,a cast_record_domain[],n cast_record_nested)",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec(`INSERT INTO cast_record_rows VALUES (1,'(1,x)','{"(1,x)",NULL}','(1,x)'),(2,'(,)','{"(,)",NULL}','(,)'),(3,NULL,NULL,NULL)`, 3))
			for index, form := range []string{"CAST(%s AS %s)", "%s::%s"} {
				v, a, n := fmt.Sprintf(form, "v", "record"), fmt.Sprintf(form, "a", "record[]"), fmt.Sprintf(form, "n", "record")
				q := "SELECT id," + v + "::text AS v," + a + "::text AS a," + n + "::text AS n FROM cast_record_rows WHERE id=$1"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
				for _, executor := range []fixture.Executor{b.DB, held} {
					for _, tc := range []struct {
						id   int64
						v, a any
					}{
						{1, "(1,x)", `{"(1,x)",NULL}`},
						{2, "(,)", `{"(,)",NULL}`},
						{3, nil, nil},
					} {
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q,
							[]string{"id", "v", "a", "n"},
							[]string{"INT4", "TEXT", "TEXT", "TEXT"},
							[][]any{{tc.id, tc.v, tc.a, tc.v}}, tc.id))
					}
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				// Published view columns expose the logical result identity; wire
				// descriptions may flatten domains to their physical base type.
				view := fmt.Sprintf("cast_record_view_%d", index)
				run(fixture.Exec("CREATE VIEW "+view+" AS SELECT "+v+" AS v,"+a+" AS a,"+n+" AS n FROM cast_record_rows", 0))
				run(fixture.Select("SELECT attname,atttypid::regtype::text AS typename FROM pg_attribute WHERE attrelid=CAST($1 AS regclass) AND attnum>0 ORDER BY attnum",
					[]string{"attname", "typename"}, []string{"NAME", "TEXT"},
					[][]any{{"v", "cast_record_domain"}, {"a", "cast_record_domain[]"}, {"n", "cast_record_nested"}}, view))
			}
		})
	}
}
