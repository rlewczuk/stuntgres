package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseSelectionFeatureSmoke(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		c.run(fixture.Exec("CREATE ROLE dc_reader LOGIN PASSWORD 'role-core-fixture'", 0))
		for _, target := range []struct{ name, options string }{
			{"ds_smoke_a", ""}, {"ds_smoke_b", ""},
			{"ds_smoke_disabled", "ALLOW_CONNECTIONS false"},
			{"ds_smoke_zero", "CONNECTION LIMIT 0"},
			{"ds_smoke_one", "CONNECTION LIMIT 1"},
		} {
			c.create(target.name, target.options, false)
		}
		c.run(fixture.Exec("CREATE TABLE ds_smoke_value(v integer)", 0))
		c.run(fixture.Exec("INSERT INTO ds_smoke_value VALUES (7)", 1))
		query := "SELECT v FROM ds_smoke_value ORDER BY v"
		for _, target := range []struct {
			name  string
			value int64
		}{{"ds_smoke_a", 11}, {"ds_smoke_b", 22}} {
			db, _, rec := c.pool(target.name, "")
			fixture.RunStep(t, c.p.Ctx, db, rec, fixture.Exec("CREATE TABLE ds_smoke_value(v integer)", 0))
			fixture.RunStep(t, c.p.Ctx, db, rec, fixture.Exec("INSERT INTO ds_smoke_value VALUES ($1)", 1, target.value))
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, _, recorder := c.pool(target.name, "")
			fixture.RunStep(t, c.p.Ctx, reopened, recorder, fixture.Prepared(fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{target.value}})))
		}
		a, raw, rec := c.pool("ds_smoke_a", "")
		owner, err := a.Conn(c.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, c.p.Ctx, owner, rec, s) }
		run(fixture.Exec("INSERT INTO ds_smoke_value VALUES (12)", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE ds_smoke_h NO SCROLL CURSOR WITH HOLD FOR "+query, 0))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Select("FETCH 1 FROM ds_smoke_h", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))

		missing := databaseSelectionFatal("3D000", "database \"ds_smoke_missing\" does not exist")
		databaseSelectionProbe(c, "ds_smoke_missing", "dc_reader", &missing)
		disabled := databaseSelectionFatal("55000", "database \"ds_smoke_disabled\" is not currently accepting connections")
		databaseSelectionProbe(c, "ds_smoke_disabled", "dc_reader", &disabled)
		databaseSelectionProbe(c, "ds_smoke_disabled", "", &disabled)
		zero := databaseSelectionFatal("53300", "too many connections for database \"ds_smoke_zero\"")
		databaseSelectionProbe(c, "ds_smoke_zero", "dc_reader", &zero)
		databaseSelectionProbe(c, "ds_smoke_zero", "", nil)
		one, _, _ := c.pool("ds_smoke_one", "dc_reader")
		if err := one.PingContext(c.p.Ctx); err != nil {
			t.Fatal(err)
		}
		full := databaseSelectionFatal("53300", "too many connections for database \"ds_smoke_one\"")
		databaseSelectionProbe(c, "ds_smoke_one", "dc_reader", &full)
		if err := one.Close(); err != nil {
			t.Fatal(err)
		}
		databaseSelectionProbe(c, "ds_smoke_one", "dc_reader", nil)

		if !c.backend.PG {
			rows, err := raw.QueryContext(c.p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(c.p.Ctx, raw, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(c.p.Ctx, raw); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, c.p.Ctx, a, rec, fixture.Select("SELECT relname FROM pg_class WHERE relname='ds_smoke_value'", []string{"relname"}, []string{"NAME"}, nil))
			if err := stuntgres.Restore(c.p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			for _, want := range []int64{11, 12} {
				var got int64
				if !rows.Next() {
					t.Fatal("retained row missing", rows.Err())
				}
				if err := rows.Scan(&got); err != nil || got != want {
					t.Fatalf("retained row %d, want %d: %v", got, want, err)
				}
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("retained rows changed", rows.Err())
			}
		}
		run(fixture.Select("FETCH ALL FROM ds_smoke_h", []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}}))
		run(fixture.Select("FETCH ALL FROM ds_smoke_h", []string{"v"}, []string{"INT4"}, nil))
		run(fixture.Exec("CLOSE ds_smoke_h", 0))
		run(fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}}))
		b, _, br := c.pool("ds_smoke_b", "")
		fixture.RunStep(t, c.p.Ctx, b, br, fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
		c.run(fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		policy := "SELECT datname,datallowconn,datconnlimit FROM pg_database WHERE datname IN ('ds_smoke_disabled','ds_smoke_one','ds_smoke_zero') ORDER BY datname"
		want := fixture.Select(policy, []string{"datname", "datallowconn", "datconnlimit"}, []string{"NAME", "BOOL", "INT4"}, [][]any{{"ds_smoke_disabled", false, int64(-1)}, {"ds_smoke_one", true, int64(1)}, {"ds_smoke_zero", true, int64(0)}})
		c.run(want)
		fixture.RunStep(t, c.p.Ctx, a, rec, want)
	})
}
