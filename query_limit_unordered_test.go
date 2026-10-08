package stuntgres_test

import "testing"

func TestQueryLimitUnordered(t *testing.T) {
	db := queryLimitFixture(t)
	execSQL(t, db, "CREATE TABLE duplicates(id INT)")
	execSQL(t, db, "INSERT INTO duplicates VALUES(1),(1),(2),(2),(3),(3)")
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			q       string
			allowed map[int64]int
		}{{"SELECT id FROM t LIMIT 2", map[int64]int{1: 1, 2: 1, 3: 1, 4: 1}}, {"SELECT id FROM duplicates OFFSET 1 LIMIT 2", map[int64]int{1: 2, 2: 2, 3: 2}}} {
			rows, closeQuery, err := transactionQuery(t.Context(), db, tc.q, prepared)
			if closeQuery != nil {
				defer closeQuery()
			}
			if err != nil {
				t.Fatal(err)
			}
			seen := make(map[int64]int)
			count := 0
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				count++
				seen[id]++
				if seen[id] > tc.allowed[id] {
					rows.Close()
					t.Fatal("not a source submultiset", seen, tc.allowed)
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			rows.Close()
			if count != 2 {
				t.Fatal("wrong unordered cardinality", tc.q, count)
			}
		}
	}
}
