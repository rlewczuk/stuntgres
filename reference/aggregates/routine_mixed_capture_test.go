package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineMixedCapture(t *testing.T) {
	aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		exec := func(q ...string) { aggregateDirectExec(t, p, b, c, q...) }
		exec("CREATE FUNCTION ad_mixed_callee(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION ad_mixed_capture() RETURNS regprocedure BEGIN ATOMIC SELECT CASE WHEN ad_mixed_callee(7)=7 THEN 'ad_mixed_callee(integer)'::regprocedure ELSE NULL::regprocedure END; END")
		query := "SELECT ad_mixed_capture()::text AS literal"
		held := aggregateDirectHeld(t, p, c, query)
		check := func(name string) {
			s := fixture.Select(query, []string{"literal"}, []string{"TEXT"}, [][]any{{name + "(integer)"}})
			createAggregateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT ad_mixed_capture()::oid='"+name+"(integer)'::regprocedure::oid AS original_identity", []string{"original_identity"}, []string{"BOOL"}, [][]any{{true}}))
			createAggregateBoth(t, p, b, c, aggregateDirectBag([]string{"ad_mixed_capture"}, [2]string{"ad_mixed_capture", name}))
		}
		check("ad_mixed_callee")
		// A rejected DROP must preserve both the callable and the stored body.
		createAggregateRun(t, p, b, c, aggregateDirectRestrict("DROP FUNCTION ad_mixed_callee(integer)", "ad_mixed_callee(integer)", "function ad_mixed_capture() depends on function ad_mixed_callee(integer)"))
		check("ad_mixed_callee")
		exec("ALTER FUNCTION ad_mixed_callee(integer) RENAME TO ad_mixed_original",
			"CREATE FUNCTION ad_mixed_callee(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 999$$")
		check("ad_mixed_original")
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT ad_mixed_callee(7) AS competitor", []string{"competitor"}, []string{"INT4"}, [][]any{{int64(999)}}))
		if b.PG {
			return
		}
		if err := held.Statement.Close(); err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		// Native replacement creates fresh OIDs: compare relationships, not numbers.
		held = aggregateDirectHeld(t, p, c, query)
		check("ad_mixed_original")
	})
}
