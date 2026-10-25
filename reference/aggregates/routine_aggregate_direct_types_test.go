package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAggregateDirectTypes(t *testing.T) {
	aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateDirectExec(t, p, b, c,
			"CREATE DOMAIN ad_state AS integer",
			"CREATE FUNCTION ad_state_step(ad_state,integer) RETURNS ad_state LANGUAGE SQL AS $$SELECT (coalesce($1,0)+coalesce($2,0))::ad_state$$",
			"CREATE FUNCTION ad_state_final(ad_state) RETURNS text LANGUAGE SQL AS $$SELECT $1::text$$",
			"CREATE FUNCTION ad_state_domain_step(ad_state,ad_state) RETURNS ad_state LANGUAGE SQL AS $$SELECT (coalesce($1,0)+coalesce($2,0))::ad_state$$",
			"CREATE AGGREGATE ad_state_agg(integer)(SFUNC=ad_state_step,STYPE=ad_state,INITCOND='0',FINALFUNC=ad_state_final)",
			"CREATE AGGREGATE ad_state_input(ad_state)(SFUNC=ad_state_domain_step,STYPE=ad_state,INITCOND='0',FINALFUNC=ad_state_final)",
			"CREATE AGGREGATE ad_state_result(integer)(SFUNC=ad_state_step,STYPE=ad_state,INITCOND='0')",
			"CREATE AGGREGATE ad_state_both(ad_state)(SFUNC=ad_state_domain_step,STYPE=ad_state,INITCOND='0')",
			"CREATE TABLE ad_state_data(id integer,v integer)")
		createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ad_state_data VALUES(1,1),(2,2),(3,NULL)", 3))

		// Only dependent declarations are selected. All referenced types and raw
		// physical rows remain visible: no DISTINCT, expected-type filter, or
		// support-type subtraction can conceal an extra edge or a duplicate.
		const typeQuery = `SELECT p.proname::text AS dependent,r.typname::text AS referenced,
 d.classid::bigint AS classid,d.objsubid,d.refclassid::bigint AS refclassid,d.refobjsubid,d.deptype::text AS deptype,
 d.objid=p.oid AS owner_identity,d.refobjid=r.oid AS reference_identity
 FROM (SELECT oid,proname FROM pg_proc WHERE proname IN
 ('ad_state_step','ad_state_final','ad_state_domain_step','ad_state_agg','ad_state_input','ad_state_result','ad_state_both')) p
 JOIN pg_depend d ON d.objid=p.oid JOIN pg_type r ON r.oid=d.refobjid
 WHERE d.classid=1255::oid AND d.refclassid=1247::oid AND d.deptype='n'`
		// STYPE alone creates no aggregate->domain row. Genuine input/result
		// edges survive even when that type is also STYPE, and repeated domain
		// occurrences within each support/aggregate declaration collapse once.
		typeRows := make([][]any, 0, 6)
		for _, name := range []string{"ad_state_step", "ad_state_final", "ad_state_domain_step", "ad_state_input", "ad_state_result", "ad_state_both"} {
			typeRows = append(typeRows, []any{name, "ad_state", int64(1255), int64(0), int64(1247), int64(0), "n", true, true})
		}
		typeBag := fixture.Select(typeQuery,
			[]string{"dependent", "referenced", "classid", "objsubid", "refclassid", "refobjsubid", "deptype", "owner_identity", "reference_identity"},
			[]string{"TEXT", "TEXT", "INT8", "INT4", "INT8", "INT4", "TEXT", "BOOL", "BOOL"}, typeRows)
		typeBag.Bag = true
		procedureBag := aggregateDirectBag([]string{"ad_state_step", "ad_state_final", "ad_state_domain_step", "ad_state_agg", "ad_state_input", "ad_state_result", "ad_state_both"},
			[2]string{"ad_state_agg", "ad_state_step"}, [2]string{"ad_state_agg", "ad_state_final"},
			[2]string{"ad_state_input", "ad_state_domain_step"}, [2]string{"ad_state_input", "ad_state_final"},
			[2]string{"ad_state_result", "ad_state_step"}, [2]string{"ad_state_both", "ad_state_domain_step"})

		const query = "SELECT ad_state_agg(v) AS state_only,ad_state_input(v::ad_state) AS input_type,ad_state_result(v)::text AS result_type,ad_state_both(v::ad_state)::text AS both_types FROM ad_state_data WHERE id<=$1 OR ($1=-1 AND id=3)"
		var held fixture.HeldStatement
		prepare := func() { held = aggregateDirectHeld(t, p, c, query) }
		prepare()
		check := func() {
			// Integer and text are pinned: neither occurs anywhere in the full
			// outgoing normal type bag, while support signature domain edges do.
			createAggregateBoth(t, p, b, c, typeBag)
			createAggregateBoth(t, p, b, c, procedureBag)
			for _, tc := range []struct {
				limit int64
				value string
			}{{3, "3"}, {-1, "0"}, {0, "0"}, {1, "1"}, {3, "3"}} {
				step := fixture.Select(query, []string{"state_only", "input_type", "result_type", "both_types"},
					[]string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{tc.value, tc.value, tc.value, tc.value}}, tc.limit)
				createAggregateBoth(t, p, b, c, step)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
			}
		}
		check()
		if b.PG {
			return
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
		// Replacement receives new handles; no claim that old prepared handles
		// survive Reset. Replay must retain the domain/support prerequisites
		// despite the intentionally absent state-only aggregate type edge.
		prepare()
		check()
	})
}
