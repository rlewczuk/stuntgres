package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringLiteralsSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			peer := db
			if !backend.PG {
				peer = p.Native
			}
			if err := peer.Ping(); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("CREATE TABLE t(id INT,label TEXT,flag BOOL)"); err != nil {
				t.Fatal(err)
			}
			want := [][]any{}
			for i, tc := range stringLiteralValues() {
				for _, prepared := range []bool{false, true} {
					q := fmt.Sprintf("INSERT INTO t VALUES (%d,%s,TRUE)", len(want)+1, tc.source)
					var result sql.Result
					var err error
					if prepared {
						stmt, e := db.Prepare(q)
						if e != nil {
							t.Fatal(e)
						}
						result, err = stmt.Exec()
						stmt.Close()
					} else {
						result, err = db.Exec(q)
					}
					if err != nil {
						t.Fatal(err)
					}
					if n, e := result.RowsAffected(); e != nil || n != 1 {
						t.Fatalf("affected %d: %v", n, e)
					}
					want = append(want, []any{int64(len(want) + 1), tc.text, true})
				}
				if tc.source == `$$a;'b$$` {
					s := fixture.Select(`SELECT id FROM t WHERE label=$$a;'b$$ ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(2*i + 1)}, {int64(2*i + 2)}})
					rows, err := db.Query(s.Query)
					if err != nil {
						t.Fatal(err)
					}
					fixture.LexicalRows(t, backend.PG, rows, s, nil)
					stmt, err := db.Prepare(s.Query)
					if err != nil {
						t.Fatal(err)
					}
					rows, err = stmt.Query()
					if err != nil {
						stmt.Close()
						t.Fatal(err)
					}
					fixture.LexicalRows(t, backend.PG, rows, s, nil)
					stmt.Close()
				}
			}
			rows, err := db.Query("SELECT * FROM phantom")
			if rows != nil {
				rows.Close()
				t.Fatal("missing relation exposed rows")
			}
			fixture.State(t, err, "42P01")
			s := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "label", "flag"}, []string{"INT4", "TEXT", "BOOL"}, want)
			rows, err = peer.Query(s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, s, nil)
			rows, err = db.Query(s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, s, nil)
			s = fixture.Select(`SELECT id FROM t WHERE label=E'a\nb' ORDER BY id`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
			stmt, err := db.Prepare(s.Query)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = stmt.Query()
			if err != nil {
				stmt.Close()
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, s, nil)
			stmt.Close()
			stmt, err = db.Prepare(`INSERT INTO t VALUES ($1,$2,E'\164')`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, id := range []int64{100, 101} {
				result, err := stmt.Exec(id, `a\nb`)
				if err != nil {
					t.Fatal(err)
				}
				if n, e := result.RowsAffected(); e != nil || n != 1 {
					t.Fatalf("affected %d: %v", n, e)
				}
			}
			s = fixture.Select("SELECT label,flag FROM t WHERE id=100", []string{"label", "flag"}, []string{"TEXT", "BOOL"}, [][]any{{`a\nb`, true}})
			rows, err = peer.Query(s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, s, nil)
		})
	}
}
