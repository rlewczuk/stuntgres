package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCreateAggregatePersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ca_persist_owner"})
	fixture.SchemaReferenceCleanup(t, "ca_owner_scope")
	var emitted string
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Cluster roles are consumer environment, not part of database Dump SQL.
		createAggregateRun(t, p, b, c, fixture.Exec("CREATE ROLE ca_persist_owner", 0))
		if b.PG {
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			if emitted == "" {
				t.Fatal("missing actual native emitted SQL")
			}
			if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal(err)
			}
			createAggregateRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		} else {
			createAggregateSetup(t, p, b, c)
			createAggregateRun(t, p, b, c,
				fixture.Exec("CREATE VIEW ca_persist_view AS SELECT ca_sum(x) AS v FROM ca_input", 0),
				fixture.Exec("CREATE AGGREGATE ca_persist_collect(integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}')", 0),
				fixture.Exec("CREATE VIEW ca_persist_array_view AS SELECT ca_persist_collect(x ORDER BY id)::text AS v FROM ca_input", 0),
				fixture.Exec("CREATE TYPE ca_mood AS ENUM('first','next')", 0),
				fixture.Exec("CREATE FUNCTION ca_mood_step(ca_mood,ca_mood) RETURNS ca_mood LANGUAGE SQL AS $$SELECT coalesce($2,$1)$$", 0),
				fixture.Exec("CREATE AGGREGATE ca_mood_last(ca_mood) (SFUNC=ca_mood_step,STYPE=ca_mood,INITCOND='first')", 0),
				fixture.Exec("CREATE TABLE ca_mood_input(id integer,mood ca_mood)", 0),
				fixture.Exec("INSERT INTO ca_mood_input VALUES(1,'first'),(2,'next')", 2),
				fixture.Exec("CREATE AGGREGATE ca_mood_collect(ca_mood) (SFUNC=pg_catalog.array_append,STYPE=ca_mood[],INITCOND='{}')", 0),
				fixture.Exec("CREATE VIEW ca_persist_mood_array_view AS SELECT ca_mood_collect(mood ORDER BY id)::text AS v FROM ca_mood_input", 0),
				fixture.Exec("CREATE VIEW ca_persist_mood_view AS SELECT ca_mood_last(mood ORDER BY id)::text AS v FROM ca_mood_input", 0),
				fixture.Exec("CREATE SCHEMA ca_owner_scope AUTHORIZATION ca_persist_owner", 0),
				fixture.Exec("SET ROLE ca_persist_owner", 0),
				fixture.Exec("CREATE FUNCTION ca_owner_scope.owned_step(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$", 0),
				fixture.Exec("CREATE AGGREGATE ca_owner_scope.owned_sum(integer) (SFUNC=ca_owner_scope.owned_step,STYPE=integer,INITCOND='0')", 0),
				fixture.Exec("RESET ROLE", 0))
		}
		// Filter only the owning procedure before catalog joins. check runs at
		// every lifecycle boundary; full pg_proc joins otherwise repeat nested
		// catalog products. Keep all matching owners and actual OID edges so
		// duplicate names, wrong support/state and wrong ownership still fail.
		check := func() {
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM ca_persist_view", int64(6)))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_sum(x) AS v FROM ca_input WHERE FALSE", int64(0)))
			createAggregateRun(t, p, b, c, fixture.Select("SELECT a.agginitval,a.aggtransspace,f.proname FROM (SELECT oid FROM pg_proc WHERE proname='ca_sum') p JOIN pg_aggregate a ON p.oid=a.aggfnoid JOIN pg_proc f ON f.oid=a.aggtransfn", []string{"agginitval", "aggtransspace", "proname"}, []string{"TEXT", "INT4", "NAME"}, [][]any{{"000", int64(32), "ca_add"}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT v FROM ca_persist_array_view", []string{"v"}, []string{"TEXT"}, [][]any{{"{1,2,3}"}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT ca_persist_collect(x)::text AS v FROM ca_input WHERE FALSE", []string{"v"}, []string{"TEXT"}, [][]any{{"{}"}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT v FROM ca_persist_mood_view", []string{"v"}, []string{"TEXT"}, [][]any{{"next"}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT ca_mood_last(mood)::text AS v FROM ca_mood_input WHERE FALSE", []string{"v"}, []string{"TEXT"}, [][]any{{"first"}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT v FROM ca_persist_mood_array_view", []string{"v"}, []string{"TEXT"}, [][]any{{"{first,next}"}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT ca_mood_collect(mood)::text AS v FROM ca_mood_input WHERE FALSE", []string{"v"}, []string{"TEXT"}, [][]any{{"{}"}}))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_owner_scope.owned_sum(x) AS v FROM ca_input", int64(6)))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT p.proowner=r.oid AS owner FROM (SELECT proname,proowner FROM pg_proc WHERE proname IN('owned_sum','owned_step')) p JOIN pg_roles r ON r.rolname='ca_persist_owner' ORDER BY p.proname", []string{"owner"}, []string{"BOOL"}, [][]any{{true}, {true}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT a.aggtransfn=378::oid AS support,a.aggtranstype=1007::oid AS state_type,p.prorettype=1007::oid AS result_type FROM (SELECT oid,prorettype FROM pg_proc WHERE proname='ca_persist_collect') p JOIN pg_aggregate a ON a.aggfnoid=p.oid", []string{"support", "state_type", "result_type"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		}
		check()
		if b.PG {
			return
		} // Only the native-control phase lacks a PostgreSQL API.
		held, err := c.QueryContext(p.Ctx, "SELECT v FROM ca_persist_view")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = dump.String()
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+"\nCREATE AGGREGATE ca_bad(integer) (SFUNC=missing_ca_support,STYPE=int4);")); err == nil {
			t.Fatal("invalid restore published")
		}
		check()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err = stuntgres.Restore(ctx, p.Native, strings.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore=%v", err)
		}
		check()
		freshDriver := stuntgres.NewDriver()
		connector, err := freshDriver.OpenConnector("aggregate-create-fresh/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, fixture.Exec("CREATE ROLE ca_persist_owner", 0))
		if err = stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		rows, err := fresh.QueryContext(p.Ctx, "SELECT v FROM ca_persist_view")
		if err != nil {
			t.Fatal(err)
		}
		fixture.ScalarRows(t, false, rows, []string{"v"}, []string{"INT4"}, [][]any{{int64(6)}}, false)
		for _, tc := range []struct{ query, value string }{
			{"SELECT v FROM ca_persist_array_view", "{1,2,3}"},
			{"SELECT v FROM ca_persist_mood_view", "next"},
			{"SELECT v FROM ca_persist_mood_array_view", "{first,next}"},
		} {
			rows, err := fresh.QueryContext(p.Ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.ScalarRows(t, false, rows, []string{"v"}, []string{"TEXT"}, [][]any{{tc.value}}, false)
		}
		fixture.ScalarRows(t, false, held, []string{"v"}, []string{"INT4"}, [][]any{{int64(6)}}, false)
		for _, revoke := range []string{
			"REVOKE EXECUTE ON FUNCTION ca_sum(integer) FROM PUBLIC",
			"REVOKE EXECUTE ON FUNCTION ca_add(integer,integer) FROM PUBLIC",
		} {
			createAggregateRun(t, p, b, c, fixture.Exec(revoke, 0))
			var rejected bytes.Buffer
			err = stuntgres.Dump(p.Ctx, p.Native, &rejected)
			if err == nil || err.Error() != "stuntgres: dump cannot serialize explicit routine ACL" || rejected.Len() != 0 {
				t.Fatalf("explicit-ACL dump=%q/%v", rejected.String(), err)
			}
			check()
			// Restore resets both explicit ACLs through the unchanged default-ACL dump.
			if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferenceCreateAggregateTemporaryRemap(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateSetup(t, p, b, c)
		createAggregateRun(t, p, b, c, fixture.Exec("CREATE VIEW ca_remap_persistent AS SELECT ca_sum(x) AS v FROM ca_input", 0))
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE TEMP TABLE ca_temp_source(v integer)", 0),
			fixture.Exec("INSERT INTO ca_temp_source VALUES(8)", 1),
			fixture.Exec("CREATE TEMP VIEW ca_temp_independent AS SELECT sum(v) AS v FROM ca_temp_source", 0))
		independent := fixture.Select("SELECT v FROM ca_temp_independent", []string{"v"}, []string{"INT8"}, [][]any{{int64(8)}})
		createAggregateBoth(t, p, b, c, independent)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		createAggregateForcedRemap(t, p, dump.String())
		replacement := strings.ReplaceAll(dump.String(), "ca_add", "ca_add_replacement")
		createAggregateRun(t, p, b, c, fixture.Exec("CREATE TEMP VIEW ca_temp_dependent AS SELECT v FROM ca_remap_persistent", 0))
		// A replacement cannot silently retarget a retained TEMP consumer to new
		// aggregate/support identities. The rejected replacement preserves both.
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(replacement)); err == nil || err.Error() != "stuntgres: catalog replacement with live temporary view persistent dependencies is unsupported" {
			t.Fatalf("replacement refusal=%v", err)
		}
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM ca_temp_dependent", int64(6)))
		createAggregateRun(t, p, b, c, fixture.Exec("DROP VIEW ca_temp_dependent", 0))
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			createAggregateBoth(t, p, b, c, independent)
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_sum(x) AS v FROM ca_input", int64(6)))
		}
	})
}

func TestReferenceCreateAggregateFeatureSmoke(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateSetup(t, p, b, c)
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_sum(x) AS v FROM ca_input", int64(6)))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_sum(x) AS v FROM ca_input WHERE FALSE", int64(0)))
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE FUNCTION ca_smoke_null(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,100)$$", 0),
			fixture.Exec("CREATE AGGREGATE ca_smoke_nonstrict(integer) (SFUNC=ca_smoke_null,STYPE=int4)", 0),
			fixture.Exec("CREATE AGGREGATE ca_smoke_strict(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0')", 0))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_smoke_nonstrict(x) AS v FROM(VALUES(NULL::int),(1)) s(x)", int64(101)))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ca_smoke_strict(x) AS v FROM(VALUES(NULL::int),(1),(2),(3)) s(x)", int64(6)))
	})
}
