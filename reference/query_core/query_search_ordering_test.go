package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQuerySearchOrdering(t *testing.T) {
	searchReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		searchSeed(subqueryRun(t, p, b, c))
		for _, mode := range searchModes {
			t.Run(mode, func(t *testing.T) {
				q := searchTree(mode) + "SELECT id,seq FROM r ORDER BY seq"
				recursiveCTERoutes(t, p, b, c, searchTreeStep(mode, q))
				want := searchTreeRows(mode)
				reversed := make([][]any, len(want))
				ids := make([][]any, len(want))
				for i := range want {
					reversed[i] = want[len(want)-1-i]
					ids[i] = []any{want[i][0]}
				}
				run := subqueryRun(t, p, b, c)
				s := searchTreeStep(mode, q+" DESC")
				s.Rows = reversed
				subqueryBoth(run, s)
				s = searchTreeStep(mode, searchTree(mode)+"SELECT id,seq FROM r")
				s.Bag = true
				subqueryBoth(run, s)
				// Joined self appears on the opposite side and seq is not returned.
				q = strings.Replace(searchTree(mode), "search_nodes n JOIN r ON", "r JOIN search_nodes n ON", 1) + "SELECT id FROM r ORDER BY seq"
				recursiveCTERoutes(t, p, b, c, fixture.Select(q, []string{"id"}, []string{"INT4"}, ids))
			})
		}
	})
}

func TestReferenceQuerySearchKeys(t *testing.T) {
	searchReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		t.Run("NULL current keys", func(t *testing.T) { searchNulls(t, p, b, c) })
		t.Run("key codecs and ordinary identifiers", func(t *testing.T) { searchKeyCodecs(t, p, b, c) })
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE search_multi(id INT,a INT,b TEXT)", 0))
		run(fixture.Exec("INSERT INTO search_multi VALUES(1,1,'z'),(2,1,'a'),(3,NULL,'b'),(4,2,NULL),(5,1,NULL)", 5))
		for _, mode := range searchModes {
			for _, tc := range []struct {
				keys     string
				ids      []int64
				dfs, bfs []string
			}{
				{"a,b", []int64{2, 1, 5, 4, 3}, []string{`{"(1,a)"}`, `{"(1,z)"}`, `{"(1,)"}`, `{"(2,)"}`, `{"(,b)"}`}, []string{"(0,1,a)", "(0,1,z)", "(0,1,)", "(0,2,)", "(0,,b)"}},
				{"b,a", []int64{2, 3, 1, 5, 4}, []string{`{"(a,1)"}`, `{"(b,)"}`, `{"(z,1)"}`, `{"(,1)"}`, `{"(,2)"}`}, []string{"(0,a,1)", "(0,b,)", "(0,z,1)", "(0,,1)", "(0,,2)"}},
			} {
				t.Run(mode+" multikey "+tc.keys, func(t *testing.T) {
					q := "WITH RECURSIVE r(id,a,b) AS (SELECT id,a,b FROM search_multi UNION ALL SELECT id,a,b FROM r WHERE FALSE)" + searchClause(mode, tc.keys) + "SELECT id,seq FROM r ORDER BY seq"
					var want [][]any
					for i, id := range tc.ids {
						seq := tc.dfs[i]
						if mode == "BREADTH" {
							seq = tc.bfs[i]
						}
						want = append(want, []any{id, seq})
					}
					recursiveCTERoutes(t, p, b, c, fixture.Select(q, []string{"id", "seq"}, []string{"INT4", searchType(mode)}, want))
				})
			}
			t.Run(mode+" generated DISTINCT identity", func(t *testing.T) {
				q := "WITH RECURSIVE r(n) AS (SELECT 1 UNION SELECT n FROM r)" + searchClause(mode, "n") + "SELECT n,seq FROM r LIMIT 3"
				want := [][]any{{int64(1), "{(1)}"}, {int64(1), "{(1),(1)}"}, {int64(1), "{(1),(1),(1)}"}}
				if mode == "BREADTH" {
					want = [][]any{{int64(1), "(0,1)"}, {int64(1), "(1,1)"}, {int64(1), "(2,1)"}}
				}
				subqueryBoth(run, fixture.Select(q, []string{"n", "seq"}, []string{"INT4", searchType(mode)}, want))
				q = strings.Replace(q, "SELECT n,seq FROM r LIMIT", "SELECT n FROM r LIMIT", 1)
				subqueryBoth(run, fixture.Select(q, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 1, 1)))
			})
			t.Run(mode+" ties and duplicate anchors", func(t *testing.T) {
				for _, union := range []string{"UNION ALL", "UNION"} {
					q := "WITH RECURSIVE r(id,k) AS (SELECT id,a FROM search_multi WHERE id IN (1,2) " + union + " SELECT id,k FROM r WHERE FALSE)" + searchClause(mode, "k") + "SELECT id,seq FROM r ORDER BY seq,id"
					seq := "{(1)}"
					if mode == "BREADTH" {
						seq = "(0,1)"
					}
					subqueryBoth(run, fixture.Select(q, []string{"id", "seq"}, []string{"INT4", searchType(mode)}, [][]any{{int64(1), seq}, {int64(2), seq}}))
					q = "WITH RECURSIVE r(n) AS (SELECT a FROM search_multi WHERE id IN (1,2) " + union + " SELECT n FROM r WHERE FALSE)" + searchClause(mode, "n") + "SELECT n,seq FROM r"
					want := [][]any{{int64(1), seq}}
					if union == "UNION ALL" {
						want = append(want, []any{int64(1), seq})
					}
					subqueryBoth(run, cteBag(q, []string{"n", "seq"}, []string{"INT4", searchType(mode)}, want))
				}
			})
			t.Run(mode+" aliases and duplicate labels", func(t *testing.T) {
				seq := "{(1)}"
				if mode == "BREADTH" {
					seq = "(0,1)"
				}
				q := `WITH RECURSIVE r("Key") AS (SELECT 1 AS original UNION ALL SELECT "Key" FROM r WHERE FALSE)` + searchClause(mode, `"Key"`) + `SELECT "Key",seq FROM r`
				subqueryBoth(run, fixture.Select(q, []string{"Key", "seq"}, []string{"INT4", searchType(mode)}, [][]any{{int64(1), seq}}))
				q = "WITH RECURSIVE r(n,n) AS (SELECT 1,2 UNION ALL SELECT 1,2 FROM r WHERE FALSE)" + searchClause(mode, "n") + "SELECT seq FROM r"
				subqueryBoth(run, fixture.Select(q, []string{"seq"}, []string{searchType(mode)}, [][]any{{seq}}))
			})
			t.Run(mode+" JSON demand", func(t *testing.T) { searchJSON(t, p, b, c, mode) })
		}
	})
}

func searchJSON(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mode string) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	table := "search_json_" + strings.ToLower(mode)
	run(fixture.Exec("CREATE TABLE "+table+"(id INT,k JSON)", 0))
	run(fixture.Exec("INSERT INTO "+table+" VALUES(1,json '1'),(2,json '2')", 2))
	base := "WITH RECURSIVE r(id,k) AS (SELECT id,k FROM " + table + " UNION ALL SELECT id,k FROM r WHERE FALSE)" + searchClause(mode, "k")
	want := [][]any{{int64(1), "{(1)}"}, {int64(2), "{(2)}"}}
	if mode == "BREADTH" {
		want = [][]any{{int64(1), "(0,1)"}, {int64(2), "(0,2)"}}
	}
	subqueryBoth(run, cteBag(base+"SELECT id,seq FROM r", []string{"id", "seq"}, []string{"INT4", searchType(mode)}, want))
	division := fixture.ControlsReferenceDiagnostic("42883", "could not identify a comparison function for type json")
	searchAssertDiagnostic(t, p, b, c, base+"SELECT seq FROM r ORDER BY seq", &division)
	subqueryBoth(run, fixture.Select(base+"SELECT seq FROM r ORDER BY seq LIMIT 0", []string{"seq"}, []string{searchType(mode)}, nil))
	q := strings.Replace(base, "SELECT id,k FROM "+table, "SELECT id,k FROM "+table+" WHERE id=1", 1) + "SELECT id,seq FROM r ORDER BY seq"
	subqueryBoth(run, fixture.Select(q, []string{"id", "seq"}, []string{"INT4", searchType(mode)}, want[:1]))
	// First unequal key short-circuits before unsupported JSON comparison.
	q = strings.Replace(base, "BY k SET", "BY id,k SET", 1) + "SELECT id FROM r ORDER BY seq"
	subqueryBoth(run, fixture.Select(q, []string{"id"}, []string{"INT4"}, recursiveCTEInts(1, 2)))
	if mode == "BREADTH" {
		q = "WITH RECURSIVE r(id,k) AS (SELECT 1,json '1' UNION ALL SELECT id+1,k FROM r WHERE id<2)" + searchClause(mode, "k") + "SELECT id FROM r ORDER BY seq"
		subqueryBoth(run, fixture.Select(q, []string{"id"}, []string{"INT4"}, recursiveCTEInts(1, 2)))
	}
	run(fixture.Exec("UPDATE "+table+" SET k=NULL", 2))
	searchAssertDiagnostic(t, p, b, c, base+"SELECT seq FROM r ORDER BY seq", &division)
}
