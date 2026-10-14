package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONKeysReviewMixedFrom(t *testing.T) {
	jsonKeysScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, source string
			rows [][]any
		}{
			{"keys_longer", `ROWS FROM(json_object_keys('{"a":1,"b":2}'),regexp_split_to_table('x',','))`, [][]any{{"a", "x", int64(1)}, {"b", nil, int64(2)}}},
			{"regexp_longer_duplicates", `ROWS FROM(json_object_keys('{"a":1,"a":2}'),regexp_split_to_table('x,y,z',','))`, [][]any{{"a", "x", int64(1)}, {"a", "y", int64(2)}, {nil, "z", int64(3)}}},
			{"keys_empty", `ROWS FROM(json_object_keys('{}'),regexp_split_to_table('x,y',','))`, [][]any{{nil, "x", int64(1)}, {nil, "y", int64(2)}}},
			{"keys_null", `ROWS FROM(json_object_keys(NULL::json),regexp_split_to_table('x',','))`, [][]any{{nil, "x", int64(1)}}},
			{"both_empty", `ROWS FROM(json_object_keys('{}'),regexp_matches('x','a'))`, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				types := []string{"TEXT", "TEXT", "INT8"}
				if tc.name == "both_empty" {
					types[1] = "_TEXT"
				}
				s := fixture.Select("SELECT * FROM "+tc.source+" WITH ORDINALITY AS r(k,s,n) ORDER BY n", []string{"k", "s", "n"}, types, tc.rows)
				if tc.name != "both_empty" {
					s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonKeysMetadata(t, r, b.PG, s.Columns, s.Types) }
				}
				jsonKeysBoth(t, p, b, c, s)
			})
		}
		// Constants in the same mixed group exercise stored literal ownership,
		// not the separate-FROM Cartesian branch.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE VIEW jk_review_mixed AS SELECT * FROM ROWS FROM(json_object_keys('{"a":1,"a":2}'),regexp_split_to_table('x,y,z',',')) WITH ORDINALITY AS r(k,s,n)`, 0))
		check := fixture.Select("SELECT k,s,n FROM jk_review_mixed ORDER BY n", []string{"k", "s", "n"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{"a", "x", int64(1)}, {"a", "y", int64(2)}, {nil, "z", int64(3)}})
		check.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			// Stored view columns are catalog-origin nullable attributes natively;
			// pq still reports unknown nullability. Direct function outputs above
			// remain computed columns with unknown nullability in both adapters.
			fixture.DriverBasicMetadata(t, r, b.PG, check.Types, []bool{true, true, true})
			meta, err := r.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for i, ct := range meta {
				if ct.Name() != check.Columns[i] {
					t.Fatalf("stored column %d name=%q want=%q", i, ct.Name(), check.Columns[i])
				}
				if precision, scale, known := ct.DecimalSize(); precision != 0 || scale != 0 || known {
					t.Fatalf("stored column %d DecimalSize=%d/%d/%t", i, precision, scale, known)
				}
			}
		}
		jsonKeysBoth(t, p, b, c, check)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER VIEW jk_review_mixed RENAME COLUMN k TO key", 0))
		check.Query = "SELECT key AS k,s,n FROM jk_review_mixed ORDER BY n"
		jsonKeysBoth(t, p, b, c, check)
	})
}
