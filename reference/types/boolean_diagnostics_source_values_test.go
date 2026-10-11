package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Preserve the original two-slot consumer separately from accepted(flag).
func TestReferenceBooleanDiagnosticsSourceValues(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			if _, err := db.Exec("CREATE TABLE b(id INT,flag BOOL)"); err != nil {
				t.Fatal(err)
			}
			stmt, err := db.Prepare("INSERT INTO b VALUES ($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, group := range []struct {
				values []string
				want   bool
			}{
				{[]string{"t", "tr", "tru", "true", "y", "ye", "yes", "on", "1", "TrUe", " YES ", "\tOn\n"}, true},
				{[]string{"f", "fa", "fal", "fals", "false", "n", "no", "of", "off", "0", "FaLsE", " NO ", "\tOfF\n"}, false},
			} {
				for index, value := range group.values {
					for _, prepared := range []bool{false, true} {
						for _, arg := range []any{value, []byte(value)} {
							var result sql.Result
							if prepared {
								result, err = stmt.Exec(1, arg)
							} else {
								result, err = db.Exec("INSERT INTO b VALUES ($1,$2)", 1, arg)
							}
							if err != nil {
								t.Fatal(err)
							}
							if count, e := result.RowsAffected(); e != nil || count != 1 {
								t.Fatalf("affected %d: %v", count, e)
							}
							if _, e := result.LastInsertId(); e == nil {
								t.Fatal("LastInsertId unexpectedly supported")
							}
						}
					}
					want := make([][]any, 4*(index+1))
					for i := range want {
						want[i] = []any{group.want}
					}
					s := fixture.Select("SELECT flag FROM b WHERE flag=$1", []string{"flag"}, []string{"BOOL"}, want, value)
					s.Bag = true
					rows, err := db.Query(s.Query, value)
					if err != nil {
						t.Fatal(err)
					}
					fixture.LexicalRows(t, backend.PG, rows, s, nil)
				}
			}
			if _, err := db.Exec("CREATE TABLE native(flag BOOL)"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO native VALUES (TRUE),(FALSE),(NULL)"); err != nil {
				t.Fatal(err)
			}
			for _, value := range []any{true, false, nil, int64(1), int64(0)} {
				if _, err := db.Exec("INSERT INTO native VALUES ($1)", value); err != nil {
					t.Fatal(err)
				}
			}
			for _, s := range []fixture.Step{
				fixture.Select("SELECT flag FROM native ORDER BY flag", []string{"flag"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {true}, {true}, {true}, {nil}, {nil}}),
				fixture.Select("SELECT flag FROM native WHERE flag=NULL", []string{"flag"}, []string{"BOOL"}, nil),
			} {
				rows, err := db.Query(s.Query)
				if err != nil {
					t.Fatal(err)
				}
				fixture.LexicalRows(t, backend.PG, rows, s, nil)
			}
			meta := fixture.Select("SELECT flag FROM native", []string{"flag"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {true}, {true}, {true}, {nil}, {nil}})
			meta.Bag = true
			metaRows, metaErr := db.Query(meta.Query)
			if metaErr != nil {
				t.Fatal(metaErr)
			}
			fixture.LexicalRows(t, backend.PG, metaRows, meta, nil)
		})
	}
}
