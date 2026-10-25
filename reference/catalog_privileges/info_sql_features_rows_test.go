package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceInfoSQLFeaturesFixture(t *testing.T) {
	rows := sfRelease(t)
	keys := make(map[[2]string]bool)
	counts := make(map[string]int)
	for _, row := range rows {
		key := [2]string{row[0].(string), row[2].(string)}
		if keys[key] {
			t.Fatalf("duplicate release key %v", key)
		}
		keys[key] = true
		counts[row[4].(string)]++
		if row[2] == "" {
			counts["parent"]++
		} else {
			counts["child"]++
		}
		if row[6] == "" {
			counts["blank_comment"]++
		} else {
			counts["comment"]++
		}
	}
	want := map[string]int{"YES": 459, "NO": 296, "parent": 615, "child": 140, "blank_comment": 719, "comment": 36}
	if len(rows) != 755 || len(keys) != 755 || !reflect.DeepEqual(counts, want) {
		t.Fatalf("release rows=%d keys=%d counts=%v", len(rows), len(keys), counts)
	}
	for _, witness := range [][]any{
		{"B011", "Embedded Ada", "", "", "NO", nil, ""},
		{"E021", "Character data types", "", "", "YES", nil, ""},
		{"E021", "Character string types", "04", "CHARACTER_LENGTH function", "YES", nil, "trims trailing spaces from CHARACTER values before counting"},
		{"F492", "Optional table constraint enforcement", "", "", "YES", nil, "except not-null constraints"},
		{"T175", "Generated columns", "", "", "NO", nil, "mostly supported"},
		{"T176", "Sequence generator support", "", "", "NO", nil, "supported except for NEXT VALUE FOR"},
		{"T321", "Basic SQL-invoked routines", "", "", "NO", nil, "partially supported"},
		{"T321", "Basic SQL-invoked routines", "06", "ROUTINES view", "YES", nil, ""},
		{"T616", "Null treatment option for LEAD and LAG functions", "", "", "NO", nil, ""},
		{"T824", "JSON_TABLE: specific PLAN clause", "", "", "NO", nil, ""},
		{"T838", "JSON_TABLE: PLAN DEFAULT clause", "", "", "NO", nil, ""},
		{"X222", "XML passing mechanism BY REF", "", "", "NO", nil, "parser accepts BY REF but ignores it; passing is always BY VALUE"},
	} {
		found := false
		for _, row := range rows {
			if reflect.DeepEqual(row, witness) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing independent release witness %#v", witness)
		}
	}
}

func TestReferenceInfoSQLFeaturesRows(t *testing.T) {
	for _, query := range []string{sfQuery, "SELECT * FROM " + sfTable} {
		t.Run(query, func(t *testing.T) {
			sfScenario(t, false, func(c *sfCase) {
				c.exec("SET search_path=public")
				held := c.hold(query)
				c.check(c.conn, query, c.release)
				c.run(held, c.projection(query, c.release))
				c.exec("CREATE TABLE public.sf_unrelated(id integer)")
				c.run(held, c.projection(query, c.release))
				c.exec("DROP TABLE public.sf_unrelated")
				c.run(held, c.projection(query, c.release))
			})
		})
	}
	t.Run("bound held reuse", func(t *testing.T) {
		sfScenario(t, false, func(c *sfCase) {
			q := sfQuery + " WHERE feature_id=$1"
			held := c.hold(q)
			for _, id := range []any{"E021", "T321", "absent-feature", nil, "E021"} {
				c.check(held, q, sfFeature(c.release, id), id)
				c.run(c.conn, c.projection(q, sfFeature(c.release, id), id))
			}
		})
	})
}

func TestReferenceInfoSQLFeaturesBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		predicate string
		args      []any
		keep      func([]any) bool
	}{
		{"absent", "feature_id='absent-feature'", nil, nil},
		{"false", "FALSE", nil, nil},
		{"null identifier", "feature_id IS NULL", nil, nil},
		{"literal null", "feature_id=NULL", nil, nil},
		{"bound null", "feature_id=$1", []any{nil}, nil},
		{"parent blanks", "sub_feature_id=''", nil, func(r []any) bool { return r[2] == "" }},
		{"comment blanks", "comments=''", nil, func(r []any) bool { return r[6] == "" }},
		{"verification null", "is_verified_by IS NULL", nil, func(r []any) bool { return true }},
		{"verification nonnull", "is_verified_by IS NOT NULL", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sfScenario(t, false, func(c *sfCase) {
				var want [][]any
				if tc.keep != nil {
					want = sfFilter(c.release, tc.keep)
				}
				c.check(c.conn, sfQuery+" WHERE "+tc.predicate, want, tc.args...)
			})
		})
	}
	t.Run("released flags and comments", func(t *testing.T) {
		sfScenario(t, false, func(c *sfCase) {
			q := sfQuery + " WHERE feature_id=$1"
			held := c.hold(q)
			for _, id := range []string{"B011", "E021", "F492", "T175", "T176", "T321", "T616", "T824", "T838", "X222"} {
				c.check(held, q, sfFeature(c.release, id), id)
			}
		})
	})
}
