package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// Positions below are independently derived from release parser locations, not
// observed wire records. Root must settle them against pinned 18.0 before red.
func TestReferenceQueryRecursiveCTEDiagnostics(t *testing.T) {
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE recursive_diag(n INT)", 0))
		run(fixture.Exec("CREATE TABLE recursive_collation(a TEXT COLLATE \"C\",b TEXT COLLATE \"POSIX\")", 0))
		const form = "recursive query \"r\" does not have the form non-recursive-term UNION [ALL] recursive-term"
		const castHint = "Cast the output of the non-recursive term to the correct type."
		for _, population := range []string{"empty", "populated"} {
			if population == "populated" {
				run(fixture.Exec("INSERT INTO recursive_diag VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO recursive_collation VALUES('a','b')", 1))
			}
			for _, tc := range []struct{ name, q, code, message, marker, hint, detail string }{
				{"form", "WITH RECURSIVE r(n) AS (SELECT n FROM r) SELECT * FROM r", "42P19", form, "r(n)", "", ""},
				{"form_intersect", "WITH RECURSIVE r(n) AS (SELECT 1 INTERSECT SELECT n FROM r) SELECT * FROM r", "42P19", form, "r(n)", "", ""},
				{"form_except", "WITH RECURSIVE r(n) AS (SELECT 1 EXCEPT SELECT n FROM r) SELECT * FROM r", "42P19", form, "r(n)", "", ""},
				{"anchor", "WITH RECURSIVE r(n) AS (SELECT n FROM r UNION ALL SELECT 1) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within its non-recursive term", "r UNION", "", ""},
				{"multiple", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT a.n FROM r a CROSS JOIN r b) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear more than once", "r b)", "", ""},
				{"multiple_union", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n FROM r UNION ALL SELECT n FROM r)) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear more than once", "r))", "", ""},
				{"scalar", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT (SELECT n FROM r)) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within a subquery", "r))", "", ""},
				{"exists", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 2 WHERE EXISTS(SELECT n FROM r)) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within a subquery", "r))", "", ""},
				{"in", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 2 WHERE 1 IN (SELECT n FROM r)) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within a subquery", "r))", "", ""},
				{"left_nullable", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT r.n FROM recursive_diag d LEFT JOIN r ON TRUE) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within an outer join", "r ON", "", ""},
				{"right_nullable", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT r.n FROM r RIGHT JOIN recursive_diag d ON TRUE) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within an outer join", "r RIGHT", "", ""},
				{"full_nullable", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT r.n FROM r FULL JOIN recursive_diag d ON TRUE) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within an outer join", "r FULL", "", ""},
				{"intersect_all", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n FROM r INTERSECT ALL SELECT 1)) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within INTERSECT", "r INTERSECT", "", ""},
				{"except_right", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT 1 EXCEPT SELECT n FROM r)) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within EXCEPT", "r))", "", ""},
				{"except_all_left", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n FROM r EXCEPT ALL SELECT 1)) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within EXCEPT", "r EXCEPT", "", ""},
				{"except_all_right", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT 1 EXCEPT ALL SELECT n FROM r)) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within EXCEPT", "r))", "", ""},
				{"modifying", "WITH RECURSIVE r AS (INSERT INTO recursive_diag SELECT n FROM r RETURNING n) SELECT * FROM r", "42P19", "recursive query \"r\" must not contain data-modifying statements", "r AS", "", ""},
				{"modifying_update", "WITH RECURSIVE r AS (UPDATE recursive_diag SET n=r.n FROM r RETURNING recursive_diag.n) SELECT * FROM r", "42P19", "recursive query \"r\" must not contain data-modifying statements", "r AS", "", ""},
				{"modifying_delete", "WITH RECURSIVE r AS (DELETE FROM recursive_diag USING r WHERE recursive_diag.n=r.n RETURNING recursive_diag.n) SELECT * FROM r", "42P19", "recursive query \"r\" must not contain data-modifying statements", "r AS", "", ""},
				{"modifying_merge", "WITH RECURSIVE r AS (MERGE INTO recursive_diag d USING r ON d.n=r.n WHEN MATCHED THEN DELETE RETURNING d.n) SELECT * FROM r", "42P19", "recursive query \"r\" must not contain data-modifying statements", "r AS", "", ""},
				{"mutual", "WITH RECURSIVE r AS (SELECT n FROM s),s AS (SELECT n FROM r) SELECT * FROM r", "0A000", "mutual recursion between WITH items is not implemented", "r AS", "", ""},
				{"order", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r ORDER BY n) SELECT * FROM r", "0A000", "ORDER BY in a recursive query is not implemented", "n) SELECT", "", ""},
				{"offset", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r OFFSET 1 LIMIT 2) SELECT * FROM r", "0A000", "OFFSET in a recursive query is not implemented", "1 LIMIT", "", ""},
				{"limit", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r LIMIT 2) SELECT * FROM r", "0A000", "LIMIT in a recursive query is not implemented", "2) SELECT", "", ""},
				{"locking", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r FOR UPDATE) SELECT * FROM r", "0A000", "FOR UPDATE/SHARE in a recursive query is not implemented", "", "", ""},
				{"aggregate", "WITH RECURSIVE r(n) AS (SELECT 1::bigint UNION ALL SELECT count(*) FROM r) SELECT * FROM r", "42P19", "aggregate functions are not allowed in a recursive query's recursive term", "count(*)", "", ""},
				{"widen", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n::bigint FROM r) SELECT * FROM r", "42804", "recursive query \"r\" column 1 has type integer in non-recursive term but type bigint overall", "1 UNION", castHint, ""},
				{"typmod", "WITH RECURSIVE r(v) AS (SELECT 'a'::varchar(3) UNION ALL SELECT v::varchar FROM r) SELECT * FROM r", "42804", "recursive query \"r\" column 1 has type character varying(3) in non-recursive term but type character varying overall", "'a'", castHint, ""},
				{"collation", "WITH RECURSIVE r(v) AS (SELECT a FROM recursive_collation UNION ALL SELECT b FROM recursive_collation CROSS JOIN r) SELECT * FROM r", "42P21", "recursive query \"r\" column 1 has collation \"C\" in non-recursive term but collation \"(null)\" overall", "a FROM", "Use the COLLATE clause to set the collation of the non-recursive term.", ""},
				{"unknown_text", "WITH RECURSIVE r(n) AS (SELECT '7' UNION ALL SELECT n+1 FROM r) SELECT * FROM r", "42883", "operator does not exist: text + integer", "+1", "No operator matches the given name and argument types. You might need to add explicit type casts.", ""},
				{"width", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n,2 FROM r) SELECT * FROM r", "42601", "each UNION query must have the same number of columns", "n,2", "", ""},
				{"incompatible", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n::text FROM r) SELECT * FROM r", "42804", "UNION types integer and text cannot be matched", "n::text", "", ""},
				{"invalid_literal", "WITH RECURSIVE r(n) AS (SELECT 'bad'::int UNION ALL SELECT n FROM r) SELECT * FROM r", "22P02", "invalid input syntax for type integer: \"bad\"", "'bad'", "", ""},
				{"json_distinct", "WITH RECURSIVE r(v) AS (SELECT '{}'::json UNION SELECT v FROM r) SELECT * FROM r", "42883", "could not identify an equality operator for type json", "'{}'", "", ""},
				{"aliases", "WITH RECURSIVE r(a,b) AS (SELECT 1 UNION ALL SELECT a FROM r) SELECT * FROM r", "42P10", "WITH query \"r\" has 1 columns available but 2 columns specified", "r(a,b)", "", ""},
				{"duplicate", "WITH RECURSIVE r AS (SELECT 1),r AS (SELECT 2) SELECT * FROM r", "42712", "WITH query name \"r\" specified more than once", "r AS (SELECT 2)", "", ""},
				{"ordinary_forward", "WITH a AS (SELECT n FROM b),b AS (SELECT 7 AS n) SELECT * FROM a", "42P01", "relation \"b\" does not exist", "b),", "Use WITH RECURSIVE, or re-order the WITH items to remove forward references.", "There is a WITH item named \"b\", but it cannot be referenced from this part of the query."},
				{"unused_bad", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n::bigint FROM r) SELECT 7", "42804", "recursive query \"r\" column 1 has type integer in non-recursive term but type bigint overall", "1 UNION", castHint, ""},
				{"context_before_count", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT a.n FROM r a WHERE EXISTS(SELECT n FROM r)) SELECT * FROM r", "42P19", "recursive reference to query \"r\" must not appear within a subquery", "r))", "", ""},
				{"suffix", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) SELECT n FROM r; @", "42601", "syntax error at or near \"@\"", "@", "", ""},
				{"ordinary_self", "WITH r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) SELECT * FROM r", "42P01", "relation \"r\" does not exist", "r) SELECT", "Use WITH RECURSIVE, or re-order the WITH items to remove forward references.", "There is a WITH item named \"r\", but it cannot be referenced from this part of the query."},
			} {
				t.Run(population+"/"+tc.name, func(t *testing.T) {
					for _, prefix := range []string{"", "/*é*/ "} {
						q := prefix + tc.q
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Hint, want.Detail = tc.hint, tc.detail
						if tc.marker != "" {
							index := strings.Index(q, tc.marker)
							if index < 0 {
								t.Fatal("missing position marker", tc.marker)
							}
							want.Position = fixture.Position(utf8.RuneCountInString(q[:index]) + 1)
						}
						subqueryAnalysisFailure(subqueryRun(t, p, b, c), q, want)
					}
				})
			}
		}
		t.Run("complete script atomicity", func(t *testing.T) {
			run := subqueryRun(t, p, b, c)
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + "INSERT INTO recursive_diag VALUES(9); " + recursiveCTEFinite + "; @"
				want := fixture.ControlsReferenceDiagnostic("42601", "syntax error at or near \"@\"")
				want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "@")]) + 1)
				run(fixture.ErrorDiagnostic(q, want))
				run(cteBag("SELECT n FROM recursive_diag", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1)))
			}
		})
	})
}
