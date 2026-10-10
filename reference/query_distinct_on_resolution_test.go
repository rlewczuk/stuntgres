package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"
)

func queryDistinctOnMarked(marked, code, message string) (string, referenceDiagnostic) {
	i := strings.IndexByte(marked, '|')
	q := strings.Replace(marked, "|", "", 1)
	return q, referenceDiagnostic{Code: code, Message: message, Position: referencePosition(utf8.RuneCountInString(marked[:i]) + 1)}
}

func TestReferenceQueryDistinctOnResolution(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,1,10),(2,1,10),(3,1,20),(4,2,30),(5,NULL,10),(6,NULL,10),(7,NULL,NULL)", 7))
		// Every unordered/partial-prefix winner must be a source member; neither
		// peer choice nor incidental planner ordering is a portable contract.
		for _, on := range []string{"a,b", "a,a,b"} {
			for _, order := range []string{"", " ORDER BY a", " ORDER BY b", " ORDER BY a,b,id DESC", " ORDER BY b,a,id DESC", " ORDER BY a,a DESC,b,id DESC", " ORDER BY a,b,id DESC,a DESC"} {
				q := "SELECT DISTINCT ON(" + on + ") id,a,b FROM t" + order
				for _, prepared := range []bool{false, true} {
					var stmt *sql.Stmt
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, err = c.PrepareContext(p.ctx, q)
						if err == nil {
							rows, err = stmt.QueryContext(p.ctx)
						}
					} else {
						rows, err = c.QueryContext(p.ctx, q)
					}
					if err != nil {
						if stmt != nil {
							stmt.Close()
						}
						t.Fatal(q, err)
					}
					members := map[int64]string{1: "1/10", 2: "1/10", 3: "1/20", 4: "2/30", 5: "NULL/10", 6: "NULL/10", 7: "NULL/NULL"}
					seen := map[string]bool{}
					for rows.Next() {
						var id int64
						var a, bv sql.NullInt64
						if err := rows.Scan(&id, &a, &bv); err != nil {
							t.Fatal(err)
						}
						key := "NULL/NULL"
						if a.Valid {
							key = "1/10"
							if a.Int64 == 2 {
								key = "2/30"
							} else if bv.Int64 == 20 {
								key = "1/20"
							}
						} else if bv.Valid {
							key = "NULL/10"
						}
						if members[id] != key || seen[key] {
							t.Fatal("invalid winner", q, id, a, bv, key)
						}
						seen[key] = true
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					if stmt != nil {
						stmt.Close()
					}
					if len(seen) != 5 {
						t.Fatal("complete key cardinality", q, seen)
					}
				}
			}
		}
		for _, q := range []string{
			"SELECT DISTINCT ON(a) id FROM t ORDER BY a,id DESC,a ASC NULLS FIRST",
			"SELECT DISTINCT ON(t.a) id FROM t ORDER BY a,id DESC",
			"SELECT DISTINCT ON(x) id,a AS x FROM t ORDER BY x,id DESC",
			"SELECT DISTINCT ON(2) id,a FROM t ORDER BY a,id DESC",
			"SELECT DISTINCT ON(2) * FROM t ORDER BY a,id DESC",
			"SELECT DISTINCT ON(\"Key\") id,a AS \"Key\" FROM t ORDER BY \"Key\",id DESC",
		} {
			cols, types := []string{"id"}, []string{"INT4"}
			want := [][]any{{int64(3)}, {int64(4)}, {int64(7)}}
			if strings.Contains(q, " AS x") || strings.Contains(q, " AS \"Key\"") || strings.Contains(q, " id,a FROM") {
				cols = []string{"id", "a"}
				if strings.Contains(q, " AS x") {
					cols[1] = "x"
				}
				if strings.Contains(q, " AS \"Key\"") {
					cols[1] = "Key"
				}
				types = []string{"INT4", "INT4"}
				want = [][]any{{int64(3), int64(1)}, {int64(4), int64(2)}, {int64(7), nil}}
			}
			if strings.Contains(q, " * FROM") {
				cols = []string{"id", "a", "b"}
				types = []string{"INT4", "INT4", "INT4"}
				want = [][]any{{int64(3), int64(1), int64(20)}, {int64(4), int64(2), int64(30)}, {int64(7), nil, nil}}
			}
			queryDistinctBoth(run, referenceSelect(q, cols, types, want))
		}
		// Output labels precede input names; compound/qualified keys do not.
		queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON(a) b AS a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(20)}, {int64(30)}, {nil}}))
		queryDistinctBoth(run, referenceSelect("SELECT DISTINCT ON(x) a AS x,a AS x FROM t ORDER BY x", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {nil, nil}}))
	})
}

func TestReferenceQueryDistinctOnDiagnostics(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(id INT,a INT,b INT,j JSON)", 0))
		for _, marked := range []string{
			"SELECT DISTINCT ON(a,|b) id FROM t ORDER BY a,id,b",
			"SELECT DISTINCT ON(|a,b) id FROM t ORDER BY id,a,b",
			"SELECT DISTINCT ON(a,|b) id FROM t ORDER BY a,id",
			"SELECT DISTINCT ON(|a,b) id FROM t ORDER BY id",
			"/* λ */ SELECT DISTINCT ON(|a+0) id FROM t ORDER BY a,id",
			"SELECT DISTINCT ON(|x) random() AS x,random() AS y FROM t ORDER BY y",
		} {
			q, want := queryDistinctOnMarked(marked, "42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
			queryDistinctAnalysis(t, p, b, c, q, want)
		}
		for _, tc := range []struct{ marked, code, message string }{
			{"SELECT DISTINCT ON(|0) id FROM t", "42P10", "DISTINCT ON position 0 is not in select list"},
			{"SELECT DISTINCT ON(|2) id FROM t", "42P10", "DISTINCT ON position 2 is not in select list"},
			{"SELECT DISTINCT ON(|'a') id FROM t", "42601", "non-integer constant in DISTINCT ON"},
			{"SELECT DISTINCT ON(|1.1) id FROM t", "42601", "non-integer constant in DISTINCT ON"},
			{"SELECT DISTINCT ON(|x) a AS x,b AS x FROM t", "42702", "DISTINCT ON \"x\" is ambiguous"},
			{"SELECT DISTINCT ON(|absent) id FROM t", "42703", "column \"absent\" does not exist"},
			{"SELECT DISTINCT ON(id) id FROM |absent", "42P01", "relation \"absent\" does not exist"},
			{"SELECT DISTINCT ON(|a) count(*) FROM t", "42803", "column \"t.a\" must appear in the GROUP BY clause or be used in an aggregate function"},
		} {
			q, want := queryDistinctOnMarked(tc.marked, tc.code, tc.message)
			queryDistinctAnalysis(t, p, b, c, q, want)
		}
		for _, populated := range []bool{false, true} {
			if populated {
				run(referenceExec("INSERT INTO t VALUES(1,1,1,'{}'),(2,1,1,NULL)", 2))
			}
			for _, suffix := range []string{"", " WHERE FALSE", " LIMIT 0"} {
				for _, marked := range []string{"SELECT DISTINCT ON(|j) id FROM t", "SELECT DISTINCT ON(|x) j AS x FROM t", "SELECT DISTINCT ON(|1) j FROM t", "SELECT DISTINCT ON(|setseed(0)) id FROM t"} {
					typ := "json"
					if strings.Contains(marked, "setseed") {
						typ = "void"
					}
					q, want := queryDistinctOnMarked(marked+suffix, "42883", "could not identify an equality operator for type "+typ)
					queryDistinctAnalysis(t, p, b, c, q, want)
				}
			}
		}
		for _, mode := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
			q := "SELECT DISTINCT ON(a) id FROM t FOR " + mode
			queryDistinctAnalysis(t, p, b, c, q, referenceDiagnostic{Code: "0A000", Message: "FOR " + mode + " is not allowed with DISTINCT clause"})
			q, want := queryDistinctOnMarked("SELECT DISTINCT ON(|a) id FROM t ORDER BY id FOR "+mode, "42P10", "SELECT DISTINCT ON expressions must match initial ORDER BY expressions")
			queryDistinctAnalysis(t, p, b, c, q, want)
		}
	})
}
