package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceCitextComparisonDiagnostics(t *testing.T) {
	t.Run("resolution", func(t *testing.T) {
		citextComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextInstall(t, p, b, c)
			for _, tc := range []struct {
				q, message, hint, token string
			}{
				{"/*é*/ SELECT 'ABC'::citext = 1", "operator does not exist: citext = integer", "No operator matches the given name and argument types. You might need to add explicit type casts.", "="},
				{"/*é*/ SELECT citext_eq('ABC'::citext,'abc'::text)", "function citext_eq(citext, text) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "citext_eq"},
				{`/*é*/ SELECT 'ABC'::citext COLLATE "C" = 1`, "operator does not exist: citext = integer", "No operator matches the given name and argument types. You might need to add explicit type casts.", "="},
			} {
				want := citextDiagnostic("42883", tc.message)
				want.Hint, want.Position = tc.hint, citextPosition(tc.q, tc.token)
				for _, prepared := range []bool{false, true} {
					citextError(t, p, b, c, tc.q, prepared, true, true, want)
				}
			}
		})
	})
	t.Run("collation_stages", func(t *testing.T) {
		citextComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextComparisonCollationSetup(t, p, b, c)
			demand := citextDiagnostic("42P22", "could not determine which collation to use for string comparison")
			demand.Hint = "Use the COLLATE clause to set the collation explicitly."
			for _, op := range []string{"<", "<=", ">", ">="} {
				q := "SELECT a" + op + "b AS v FROM citext_compare_coll WHERE id=1"
				for _, prepared := range []bool{false, true} {
					citextError(t, p, b, c, q, prepared, true, false, demand)
				}
			}
			for _, q := range []string{
				`/*é*/ SELECT a COLLATE "C" = b COLLATE "POSIX" FROM citext_compare_coll WHERE FALSE`,
				`/*é*/ SELECT NULL::citext COLLATE "C" = NULL::citext COLLATE "POSIX"`,
				`/*é*/ SELECT CASE WHEN FALSE THEN a COLLATE "C" < b COLLATE "POSIX" ELSE TRUE END FROM citext_compare_coll`,
			} {
				want := citextDiagnostic("42P21", `collation mismatch between explicit collations "C" and "POSIX"`)
				want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q, "COLLATE")]) + 1)
				for _, prepared := range []bool{false, true} {
					citextError(t, p, b, c, q, prepared, true, true, want)
				}
			}
			q := "SELECT a < COALESCE($1::citext,b) AS v FROM citext_compare_coll WHERE id=$2"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, nil, 3))
			citextError(t, p, b, held, q, false, true, false, demand, nil, 1)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, nil, 3))
			resolved := `SELECT a COLLATE "C" < b AS v FROM citext_compare_coll WHERE id=$1`
			citextComparisonRoutes(t, p, b, c, fixture.Select(resolved, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, 2))
		})
	})
	t.Run("check_atomicity", func(t *testing.T) {
		citextComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextInstall(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE citext_compare_guard(id integer,a citext,b citext,CONSTRAINT citext_compare_equal CHECK(a=b))", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO citext_compare_guard VALUES (1,'ABC','abc'),(4,NULL,'abc')", 2))
			q := "INSERT INTO citext_compare_guard VALUES (2,'ABC','abc'),(3,'ABC','abd')"
			for _, prepared := range []bool{false, true} {
				citextError(t, p, b, c, q, prepared, false, false, citextComparisonCheckDiagnostic())
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,a::text AS a,b::text AS b FROM citext_compare_guard ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "ABC", "abc"}, {int64(4), nil, "abc"}}))
			}
			q = "INSERT INTO citext_compare_guard VALUES ($1,$2,$3)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			citextError(t, p, b, held, q, false, false, false, citextComparisonCheckDiagnostic(), 3, "ABC", "abd")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 5, "AbC", "aBc"))
		})
	})
}
