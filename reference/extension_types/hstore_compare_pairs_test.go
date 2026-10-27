package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreComparisonPairs(t *testing.T) {
	// REL_18_6 hstore_cmp compares payload bytes, total length, count, then
	// entry boundaries/NULL bits. These are source-derived, not native goldens.
	cases := []struct {
		name, left, right string
		sign              int
	}{
		{"empty_nonempty_zero_payload", "", `""=>NULL`, -1},
		{"payload_before_key_length", "b=>0", "aa=>0", 1},
		{"text_not_number", "a=>2", "a=>10", 1},
		{"payload_prefix", "a=>b", "a=>ba", -1},
		{"key_boundary", "a=>bc", "ab=>c", -1},
		{"count_before_boundary", "a=>bcd", "a=>b,c=>d", -1},
		{"null_bit", "a=>NULL", `a=>""`, 1},
		{"length_before_null", "a=>NULL", "a=>b", -1},
		{"quoted_null", "a=>NULL", `a=>"NULL"`, -1},
		{"interior_null", "a=>NULL,b=>c", `a=>"",b=>c`, 1},
		{"alternate_text", "b=>2,a=>1", `"a"=>"1", "b"=>"2"`, 0},
		{"identical_duplicate", "a=>1,a=>1", "a=>1", 0},
		{"unsigned_utf8", "a=>é", "a=>z", 1},
		{"equal_utf8_key_length", "é=>0", "zz=>0", 1},
		{"empty_equal", "", "", 0},
		{"empty_key_value", `""=>""`, `""=>""`, 0},
		{"span_transition", `""=>a,b=>""`, `a=>b`, 1},
		{"escaped_bytes", `a=>"x\"y"`, `a=>"x\\y"`, -1},
		{"long_prefix", "a=>" + strings.Repeat("x", 2048) + "a", "a=>" + strings.Repeat("x", 2048) + "b", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hstoreComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreInstall(t, p, b, c)
				query := "SELECT " + hstoreComparisonProjection("$1::hstore", "$2::hstore", false)
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				for _, pair := range []struct {
					left, right string
					sign        int
				}{{tc.left, tc.right, tc.sign}, {tc.right, tc.left, -tc.sign}, {tc.left, tc.left, 0}} {
					for _, functions := range []bool{false, true} {
						q := "SELECT " + hstoreComparisonProjection(hstoreLiteral(pair.left)+"::hstore", hstoreLiteral(pair.right)+"::hstore", functions)
						s := hstoreComparisonStep(q, hstoreComparisonRow(pair.sign))
						hstoreRun(t, p, b, c, s)
						hstoreRun(t, p, b, c, fixture.Prepared(s))
						q = "SELECT " + hstoreComparisonProjection("$1::hstore", "$2::hstore", functions)
						s = hstoreComparisonStep(q, hstoreComparisonRow(pair.sign), pair.left, pair.right)
						hstoreRun(t, p, b, c, s)
						hstoreRun(t, p, b, c, fixture.Prepared(s))
					}
					hstoreRun(t, p, b, held, hstoreComparisonStep(query, hstoreComparisonRow(pair.sign), pair.left, []byte(pair.right)))
				}
				for _, args := range [][]any{{nil, tc.left}, {tc.right, nil}, {nil, nil}} {
					want := []any{nil, nil, nil, nil, nil, nil, nil}
					hstoreRun(t, p, b, held, hstoreComparisonStep(query, want, args...))
					for _, functions := range []bool{false, true} {
						q := "SELECT " + hstoreComparisonProjection("$1::hstore", "$2::hstore", functions)
						s := hstoreComparisonStep(q, want, args...)
						hstoreRun(t, p, b, c, s)
						hstoreRun(t, p, b, c, fixture.Prepared(s))
					}
				}
				// Unknown literal/parameter coercion comes from the typed operand.
				hstoreRun(t, p, b, c, fixture.Select("SELECT $1='a=>1'::hstore AS eq,'a=>1'::hstore='a=>1' AS literal", []string{"eq", "literal"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, "a=>1"))
			})
		})
	}
}
