package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func searchKeyCodecs(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	run(fixture.Exec("CREATE TABLE search_text_keys(id INT,k TEXT)", 0))
	run(fixture.Exec(`INSERT INTO search_text_keys VALUES(1,NULL),(2,''),(3,'NULL'),(4,'a,b'),(5,'a"b'),(6,E'a\\b')`, 6))
	for _, mode := range searchModes {
		t.Run(mode+" codecs", func(t *testing.T) {
			q := "WITH RECURSIVE r(id,k) AS (SELECT id,k FROM search_text_keys UNION ALL SELECT id,k FROM r WHERE FALSE)" + searchClause(mode, "k") + "SELECT id,k,seq,seq::text FROM r ORDER BY id"
			rows, err := c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			want := []any{nil, "", "NULL", "a,b", `a"b`, `a\b`}
			wantSeq := []string{`{()}`, `{"(\"\")"}`, `{(NULL)}`, `{"(\"a,b\")"}`, `{"(\"a\"\"b\")"}`, `{"(\"a\\\\b\")"}`}
			if mode == "BREADTH" {
				wantSeq = []string{`(0,)`, `(0,"")`, `(0,NULL)`, `(0,"a,b")`, `(0,"a""b")`, `(0,"a\\b")`}
			}
			i := 0
			for rows.Next() {
				var id int64
				var key, seq any
				var text string
				if err := rows.Scan(&id, &key, &seq, &text); err != nil {
					t.Fatal(err)
				}
				if i >= len(want) {
					t.Fatal("unexpected extra codec row")
				}
				if id != int64(i+1) || fixture.Normalize(key) != want[i] {
					t.Errorf("key row=%d id=%d key=%#v want=%#v", i, id, key, want[i])
				}
				if seq == nil || fixture.Normalize(seq) != text {
					t.Errorf("non-NULL raw generated value=%#v text=%q", seq, text)
				}
				if text != wantSeq[i] {
					t.Errorf("codec row=%d seq=%q want=%q", i, text, wantSeq[i])
				}
				searchRawSequence(t, b, seq, wantSeq[i])
				i++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if i != len(want) {
				t.Errorf("rows=%d want=%d", i, len(want))
			}
		})
		// Preserve key typmods and use the existing C collation provider.
		q := `WITH RECURSIVE r(id,k,v) AS (SELECT id,k::varchar(4) COLLATE "C",id::numeric(5,1) FROM search_text_keys WHERE id IN (2,3,4) UNION ALL SELECT id,k,v FROM r WHERE FALSE)` + searchClause(mode, "k,v") + "SELECT id FROM r ORDER BY seq"
		subqueryBoth(run, fixture.Select(q, []string{"id"}, []string{"INT4"}, recursiveCTEInts(2, 3, 4)))
		// The formerly unreserved words remain legal ordinary identifiers.
		q = "WITH RECURSIVE search(depth,breadth,first) AS (SELECT 1,2,3 UNION ALL SELECT depth,breadth,first FROM search WHERE FALSE) SEARCH " + mode + " FIRST BY depth SET seq SELECT depth,breadth,first FROM search"
		subqueryBoth(run, fixture.Select(q, []string{"depth", "breadth", "first"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3)}}))
		// Both occurrences of an overlength quoted key resolve to the same
		// PostgreSQL identifier after the ordinary 63-byte clipping rule.
		key := `"` + strings.Repeat("k", 64) + `"`
		q = "WITH RECURSIVE r(" + key + ") AS (SELECT 1 UNION ALL SELECT 1 FROM r WHERE FALSE)" + searchClause(mode, key) + "SELECT seq FROM r"
		seq := "{(1)}"
		if mode == "BREADTH" {
			seq = "(0,1)"
		}
		subqueryBoth(run, fixture.Select(q, []string{"seq"}, []string{searchType(mode)}, [][]any{{seq}}))
	}
}
