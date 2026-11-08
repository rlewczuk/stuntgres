package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLateralViews(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		lateralFixture(run)
		lateralRepro(run)
		for _, q := range []string{
			"CREATE VIEW lat_v AS SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL (SELECT t.v+10 AS m) x ON true",
			"CREATE VIEW lat_v2 AS SELECT t.id,f.family,f.usage FROM lat_t t CROSS JOIN LATERAL (VALUES('a',t.v)) f(family,usage)",
			"CREATE VIEW lat_v3 AS SELECT t.id,s.n FROM lat_t t,LATERAL (SELECT 42 AS n) s",
			"CREATE VIEW lat_max AS SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL (SELECT max(v) AS m FROM lat_t) x ON true",
			"CREATE VIEW lat_correlated_max AS SELECT t.id,x.m FROM lat_t t LEFT JOIN LATERAL (SELECT max(u.k) AS m FROM lat_u u WHERE u.k=t.id) x ON true",
			"CREATE VIEW lat_values_bound AS SELECT t.id,f.n FROM lat_t t,LATERAL ((VALUES(t.v),(t.v+1)) ORDER BY 1 NULLS LAST LIMIT 1) f(n)",
			"CREATE VIEW lat_values_plain AS SELECT t.id,f.n FROM lat_t t,LATERAL (VALUES(42)) f(n)",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ name, definition string }{
			{"lat_v", " SELECT t.id,\n    x.m\n   FROM lat_t t\n     LEFT JOIN LATERAL ( SELECT t.v + 10 AS m) x ON true;"},
			{"lat_v2", " SELECT t.id,\n    f.family,\n    f.usage\n   FROM lat_t t\n     CROSS JOIN LATERAL ( VALUES ('a'::text,t.v)) f(family, usage);"},
			{"lat_v3", lateralUncorrelatedDefinition},
			{"lat_values_plain", " SELECT t.id,\n    f.n\n   FROM lat_t t,\n    LATERAL ( VALUES (42)) f(n);"},
		} {
			subqueryBoth(run, fixture.Select("SELECT pg_get_viewdef($1::regclass,true)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{tc.definition}}, tc.name))
		}
		for _, name := range []string{"lat_v", "lat_v2", "lat_v3", "lat_values_bound", "lat_values_plain"} {
			fixture.ViewCopy(t, p, b, name, name+"_copy")
		}
		check := func() { lateralViewChecks(subqueryRun(t, p, b, c)) }
		check()
		run(fixture.Exec("ALTER TABLE lat_t RENAME COLUMN v TO renamed_v", 0))
		check()
		// Isolate the dependency witness so the complete Detail is deterministic.
		run(fixture.Exec("CREATE TABLE lat_dep(id INT,v INT,unused INT)", 0))
		run(fixture.Exec("INSERT INTO lat_dep VALUES(1,9,0)", 1))
		run(fixture.Exec("CREATE VIEW lat_dep_view AS SELECT x.n FROM lat_dep t,LATERAL (VALUES(t.v)) x(n)", 0))
		run(fixture.Exec("ALTER TABLE lat_dep RENAME COLUMN v TO renamed_value", 0))
		run(fixture.Exec("ALTER TABLE lat_dep DROP COLUMN unused", 0))
		want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column renamed_value of table lat_dep because other objects depend on it")
		want.Detail = "view lat_dep_view depends on column renamed_value of table lat_dep"
		want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("ALTER TABLE lat_dep DROP COLUMN renamed_value", want))
		subqueryBoth(run, fixture.Select("SELECT n FROM lat_dep_view", []string{"n"}, []string{"INT4"}, [][]any{{int64(9)}}))
		if b.PG {
			return
		}
		// Controls use the actual emitted stream, not reconstructed CREATE text.
		dump := fixture.ViewDump(t, p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.ViewRestore(t, p, dump)
		check()
		subqueryBoth(run, lateralDependencyStep())
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh-lateral-restore")
		if err != nil {
			t.Fatal(err)
		}
		control := sql.OpenDB(connector)
		defer control.Close()
		if err := stuntgres.Restore(p.Ctx, control, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		destination, recorder := fixture.RecordedNativePool(t, connector)
		lateralViewChecks(subqueryRun(t, p, fixture.Backend{Recorder: recorder}, destination))
		subqueryBoth(subqueryRun(t, p, fixture.Backend{Recorder: recorder}, destination), lateralDependencyStep())
		if p.Postgres != nil {
			// The fixture owns public; leave it empty for the ordinary PG backend.
			clear := func() {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}
			clear()
			defer clear()
			if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
				t.Fatal("PostgreSQL replay of actual native lateral dump", err)
			}
			pg := fixture.Backend{PG: true, Recorder: &fixture.Recorder{RawBackend: fixture.RawPostgres}}
			lateralViewChecks(subqueryRun(t, p, pg, p.Postgres))
			subqueryBoth(subqueryRun(t, p, pg, p.Postgres), lateralDependencyStep())
		}
	})
}

func lateralViewChecks(run func(fixture.Step)) {
	// Replay retains canonical executable aliases, not original source spelling.
	// Exact initial definitions are asserted above; the written marker survives
	// every native/fresh-driver and actual PostgreSQL replay.
	subqueryBoth(run, fixture.Select("SELECT strpos(pg_get_viewdef('lat_v3'::regclass,true),'LATERAL')>0 AS written_lateral", []string{"written_lateral"}, []string{"BOOL"}, [][]any{{true}}))
	for _, name := range []string{"lat_v", "lat_v_copy"} {
		subqueryBoth(run, fixture.Select("SELECT * FROM "+name+" ORDER BY id", []string{"id", "m"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(12)}, {int64(2), int64(13)}, {int64(3), nil}}))
	}
	for _, name := range []string{"lat_v2", "lat_v2_copy"} {
		subqueryBoth(run, fixture.Select("SELECT * FROM "+name+" ORDER BY id", []string{"id", "family", "usage"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "a", int64(2)}, {int64(2), "a", int64(3)}, {int64(3), "a", nil}}))
	}
	for _, name := range []string{"lat_v3", "lat_v3_copy", "lat_values_plain", "lat_values_plain_copy"} {
		subqueryBoth(run, fixture.Select("SELECT * FROM "+name+" ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(42)}, {int64(2), int64(42)}, {int64(3), int64(42)}}))
	}
	for _, name := range []string{"lat_values_bound", "lat_values_bound_copy"} {
		subqueryBoth(run, fixture.Select("SELECT * FROM "+name+" ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(3)}, {int64(3), nil}}))
	}
	subqueryBoth(run, fixture.Select("SELECT * FROM lat_max ORDER BY id", []string{"id", "m"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(3)}, {int64(3), int64(3)}}))
	subqueryBoth(run, fixture.Select("SELECT * FROM lat_correlated_max ORDER BY id", []string{"id", "m"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), nil}}))
}

func lateralDependencyStep() fixture.Step {
	return fixture.Select("SELECT n FROM lat_dep_view", []string{"n"}, []string{"INT4"}, [][]any{{int64(9)}})
}

func TestReferenceQueryLateralViewDemand(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		for _, q := range []string{
			"CREATE TABLE lat_demand_prefix(id INT)",
			"CREATE TABLE lat_demand_base(n INT,unused INT)",
			"CREATE VIEW lat_demand_inner AS SELECT n,unused FROM lat_demand_base",
			"CREATE VIEW lat_demand_outer AS SELECT q.n FROM lat_demand_prefix a,(lat_demand_inner b CROSS JOIN LATERAL (SELECT b.n) q)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO lat_demand_prefix VALUES(1),(2)", 2))
		run(fixture.Exec("INSERT INTO lat_demand_base VALUES(7,70),(9,90)", 2))
		want := [][]any{{int64(7)}, {int64(7)}, {int64(9)}, {int64(9)}}
		for _, q := range []string{
			"SELECT q.n FROM lat_demand_prefix a,(lat_demand_inner b CROSS JOIN LATERAL (SELECT b.n) q) ORDER BY q.n",
			"SELECT n FROM lat_demand_outer ORDER BY n",
		} {
			subqueryBoth(run, fixture.Select(q, []string{"n"}, []string{"INT4"}, want))
		}
	})
}
