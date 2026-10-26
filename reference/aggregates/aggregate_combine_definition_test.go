package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func aggregateCombineScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

const aggregateCombineInternalDDL = "CREATE AGGREGATE ac_internal(internal) (SFUNC=pg_catalog.string_agg_combine,STYPE=internal,COMBINEFUNC=pg_catalog.string_agg_combine,SERIALFUNC=pg_catalog.string_agg_serialize,DESERIALFUNC=pg_catalog.string_agg_deserialize,PARALLEL=SAFE)"

func TestReferenceAggregateCombineInternalDefinition(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c, fixture.Exec(aggregateCombineInternalDDL, 0))
		checkVector := aggregateCombineOIDVector(t, p, b, c)
		check := func() {
			checkVector()
			// OIDs of builtins are fixed; the owned aggregate OID is relational.
			q := "SELECT p.prokind::text AS kind,p.pronargs,p.proargtypes::text AS args,p.prorettype=2281::oid AS result_type,p.proisstrict,p.proparallel::text AS parallel,p.proowner=r.oid AS owner,p.proacl IS NULL AS default_acl,a.aggtransfn=6299::oid AS transition,a.aggtranstype=2281::oid AS state_type,a.aggcombinefn=6299::oid AS combine,a.aggserialfn=6300::oid AS serialize,a.aggdeserialfn=6301::oid AS deserialize,a.aggfinalfn=0::oid AS no_final,a.agginitval FROM (SELECT * FROM pg_proc WHERE proname='ac_internal') p JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_roles r ON r.rolname=current_user"
			createAggregateBoth(t, p, b, c, fixture.Select(q,
				[]string{"kind", "pronargs", "args", "result_type", "proisstrict", "parallel", "owner", "default_acl", "transition", "state_type", "combine", "serialize", "deserialize", "no_final", "agginitval"},
				[]string{"TEXT", "INT2", "TEXT", "BOOL", "BOOL", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT"},
				[][]any{{"a", int64(1), "2281", true, false, "s", true, true, true, true, true, true, true, true, nil}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT oid::bigint AS id,proargtypes::text AS args,prorettype::bigint AS result_type,proisstrict FROM pg_proc WHERE oid IN(6299,6300,6301) ORDER BY oid",
				[]string{"id", "args", "result_type", "proisstrict"}, []string{"INT8", "TEXT", "INT8", "BOOL"},
				[][]any{{int64(6299), "2281 2281", int64(2281), false}, {int64(6300), "2281", int64(17), true}, {int64(6301), "17 2281", int64(2281), true}}))
			// Pinned builtins have no ordinary pg_depend edge: PostgreSQL omits
			// dependencies on pinned objects. Require no bogus owned edges.
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM (SELECT oid FROM pg_proc WHERE proname='ac_internal') p JOIN pg_depend d ON d.objid=p.oid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_proc'::regclass AND d.refobjid IN(6299,6300,6301)", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		check()
		if b.PG {
			return
		}
		// Internal is declaration-only at the public SQL boundary: no invented
		// text or NULL pointer invocation is used to test its persistence.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			check()
		}
	})
}

func TestReferenceAggregateCombineSortSupport(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE AGGREGATE ac_max(integer) (SFUNC=pg_catalog.int4larger,STYPE=integer,COMBINEFUNC=pg_catalog.int4larger,PARALLEL=SAFE,SORTOP = >)", 0),
			fixture.Exec("CREATE AGGREGATE ac_plus_sort(integer) (SFUNC=pg_catalog.int4larger,STYPE=integer,COMBINEFUNC=pg_catalog.int4larger,SORTOP = +)", 0),
			fixture.Exec("CREATE TABLE ac_sort_input(x integer)", 0),
			fixture.Exec("INSERT INTO ac_sort_input VALUES(1),(NULL),(4),(2)", 4))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT pg_catalog.int4larger($1::integer,$2::integer) AS v", int64(4), 1, 4))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT pg_catalog.int4larger($1::integer,$2::integer) AS v", nil, nil, 4))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ac_max(x) AS v FROM ac_sort_input", int64(4)))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ac_max(x) AS v FROM ac_sort_input WHERE FALSE", nil))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ac_max(x) AS v FROM ac_sort_input WHERE x IS NULL", nil))
		// SORTOP need not return Boolean or belong to a B-tree family. Only
		// inspect the declaration of '+'; do not promise min/max optimization.
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT p.proname,o.oprname,o.oprleft=23::oid AS left_type,o.oprright=23::oid AS right_type,o.oprresult::bigint AS result_type FROM (SELECT oid,proname FROM pg_proc WHERE proname IN('ac_max','ac_plus_sort')) p JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_operator o ON o.oid=a.aggsortop ORDER BY p.proname",
			[]string{"proname", "oprname", "left_type", "right_type", "result_type"}, []string{"NAME", "NAME", "BOOL", "BOOL", "INT8"},
			[][]any{{"ac_max", ">", true, true, int64(16)}, {"ac_plus_sort", "+", true, true, int64(23)}}))
	})
}
