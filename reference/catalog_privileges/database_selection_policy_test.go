package reference_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseSelectionPolicy(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		for i, target := range []struct{ name, options string }{
			{"ds_policy_disabled", "ALLOW_CONNECTIONS false"},
			{"ds_policy_one", "ALLOW_CONNECTIONS true CONNECTION LIMIT 1"},
			{"ds_policy_unlimited", "ALLOW_CONNECTIONS DEFAULT CONNECTION LIMIT DEFAULT"},
			{"ds_policy_zero", "CONNECTION LIMIT 0"},
		} {
			c.create(target.name, target.options, i%2 != 0)
		}
		query := "SELECT datname,datallowconn,datconnlimit FROM pg_database WHERE datname IN ('ds_policy_disabled','ds_policy_one','ds_policy_unlimited','ds_policy_zero') ORDER BY datname"
		want := fixture.Select(query, []string{"datname", "datallowconn", "datconnlimit"}, []string{"NAME", "BOOL", "INT4"}, [][]any{
			{"ds_policy_disabled", false, int64(-1)},
			{"ds_policy_one", true, int64(1)},
			{"ds_policy_unlimited", true, int64(-1)},
			{"ds_policy_zero", true, int64(0)},
		})
		c.run(want)
		c.run(fixture.Prepared(want))
		peer, raw, rec := c.pool("ds_policy_one", "")
		stmt, err := peer.PrepareContext(c.p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: query}
		fixture.RunStep(t, c.p.Ctx, held, rec, want)
		if !c.backend.PG {
			// Capture typed rows before publication and consume them afterwards.
			ctx := context.WithValue(c.p.Ctx, fixture.RecorderKey{}, rec)
			rows, err := peer.QueryContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			frozen := fixture.SequenceFrozenRows{Executor: peer, Rows: rows, Query: query}
			var dump bytes.Buffer
			if err := stuntgres.Dump(c.p.Ctx, raw, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(c.p.Ctx, raw); err != nil {
				t.Fatal(err)
			}
			c.run(want)
			if err := stuntgres.Restore(c.p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, c.p.Ctx, frozen, rec, want)
		}
		fixture.RunStep(t, c.p.Ctx, peer, rec, want)
		fixture.RunStep(t, c.p.Ctx, peer, rec, fixture.Prepared(want))
		fixture.RunStep(t, c.p.Ctx, held, rec, want)
		c.run(want)
	})
}
