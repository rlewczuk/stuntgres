package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryRecursiveCTEFeatureSmoke(t *testing.T) {
	recursiveCTEReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		s := cteBag(recursiveCTEFinite, []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3))
		s.CheckMetadata = recursiveCTEMetadata(b, s.Columns, s.Types)
		recursiveCTERoutes(t, p, b, c, s)
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION SELECT 10-n FROM r) SELECT n FROM r", recursiveCTEInts(1, 9)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION DISTINCT SELECT 10-n FROM r) SELECT n FROM r", recursiveCTEInts(1, 9)},
			{"WITH RECURSIVE r(n) AS (SELECT n FROM (VALUES(1),(2)) s(n) UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT n FROM r", recursiveCTEInts(1, 2, 2, 3, 3)},
			{"WITH RECURSIVE r(n) AS (SELECT NULL::int UNION SELECT n FROM r) SELECT n FROM r", [][]any{{nil}}},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r) SELECT n FROM r LIMIT 3", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE r(n,depth) AS (SELECT 1,0 UNION SELECT n,depth+1 FROM r WHERE depth<2) SELECT n FROM r", recursiveCTEInts(1, 1, 1)},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) SELECT n FROM r LIMIT 0", nil},
			{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM (SELECT n FROM r) s WHERE n<3) SELECT n FROM r", recursiveCTEInts(1, 2, 3)},
			{"WITH RECURSIVE a AS (SELECT n FROM b),b AS (SELECT 7 AS n) SELECT n FROM a", recursiveCTEInts(7)},
		} {
			subqueryBoth(run, cteBag(tc.q, []string{"n"}, []string{"INT4"}, tc.rows))
		}
		q := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<$1) SELECT n FROM r"
		held := fixture.ViewHold(t, p, b, q)
		for _, tc := range []struct {
			arg  any
			rows [][]any
		}{{int64(2), recursiveCTEInts(1, 2)}, {int64(4), recursiveCTEInts(1, 2, 3, 4)}, {nil, recursiveCTEInts(1)}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, cteBag(q, []string{"n"}, []string{"INT4"}, tc.rows, tc.arg))
		}
		multiple := fixture.ControlsReferenceDiagnostic("42P19", "recursive reference to query \"r\" must not appear more than once")
		multiple.Position = "75"
		subqueryAnalysisFailure(run, "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT a.n FROM r a CROSS JOIN r b) SELECT n FROM r", multiple)
		widen := fixture.ControlsReferenceDiagnostic("42804", "recursive query \"r\" column 1 has type integer in non-recursive term but type bigint overall")
		widen.Position = "32"
		widen.Hint = "Cast the output of the non-recursive term to the correct type."
		subqueryAnalysisFailure(run, "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n::bigint FROM r) SELECT n FROM r", widen)
		run(fixture.Exec("CREATE TABLE recursive_smoke_edges(src INT,dst INT)", 0))
		run(fixture.Exec("INSERT INTO recursive_smoke_edges VALUES(1,2),(2,3)", 2))
		run(fixture.Exec("CREATE VIEW recursive_smoke_view AS WITH RECURSIVE r(n) AS (SELECT 1 UNION SELECT dst FROM recursive_smoke_edges e JOIN r ON e.src=r.n) SELECT n FROM r", 0))
		check := func(want [][]any) {
			subqueryBoth(run, cteBag("SELECT n FROM recursive_smoke_view", []string{"n"}, []string{"INT4"}, want))
		}
		check(recursiveCTEInts(1, 2, 3))
		run(fixture.Exec("INSERT INTO recursive_smoke_edges VALUES(3,4)", 1))
		check(recursiveCTEInts(1, 2, 3, 4))
		run(fixture.Exec("CREATE TABLE recursive_smoke_saved(n INT)", 0))
		run(fixture.Exec("INSERT INTO recursive_smoke_saved "+recursiveCTEFinite, 3))
		run(fixture.Exec("CREATE TABLE recursive_smoke_sentinel(n INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO recursive_smoke_sentinel VALUES(99,99)", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO recursive_smoke_sentinel "+recursiveCTELate+"SELECT n,v FROM r", fixture.ControlsReferenceDiagnostic("22012", "division by zero")))
		run(cteBag("SELECT * FROM recursive_smoke_sentinel", []string{"n", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(recursiveCTEInts(1, 2, 3, 4))
			subqueryBoth(run, cteBag("SELECT n FROM recursive_smoke_saved", []string{"n"}, []string{"INT4"}, recursiveCTEInts(1, 2, 3)))
		}
	})
	// The standalone smoke and this selected scenario both include genuine
	// running recursion cancellation, with joined workers and owner recovery.
	t.Run("running cancellation", recursiveCTECancellation)
}
