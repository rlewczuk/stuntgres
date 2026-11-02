package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkGetPkeyResolution(t *testing.T) {
	pkeyCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA pk_left", "CREATE SCHEMA pk_right", "CREATE SCHEMA pk_quoted",
			`CREATE TABLE pk_quoted."Mixed.Table ""name"("First Key" integer,"Second.Key" integer,PRIMARY KEY("Second.Key","First Key"))`,
			"CREATE TABLE pk_left.pkey_resolve(left_key integer PRIMARY KEY)",
			"CREATE TABLE pk_right.pkey_resolve(right_key integer PRIMARY KEY)",
			"SET search_path=pk_left,pk_right,public",
		} {
			run(fixture.Exec(q, 0))
		}
		held := pkeyHeld(t, p, c, pkeyCall)
		check := func(name string, arg string, want [][]any) {
			t.Run(name, func(t *testing.T) { pkeyRoutes(t, p, b, c, &held, pkeyRows(want, arg)) })
		}
		left, right := [][]any{{int64(1), "left_key"}}, [][]any{{int64(1), "right_key"}}
		check("quoted", `"pk_quoted"."Mixed.Table ""name"`, [][]any{{int64(1), "Second.Key"}, {int64(2), "First Key"}})
		check("unquoted_fold", "PKEY_RESOLVE", left)
		run(fixture.Exec("SET search_path=pk_right,pk_left,public", 0))
		check("held_caller_path", "pkey_resolve", right)
		check("qualified", "pk_left.pkey_resolve", left)
		run(fixture.Exec("CREATE TEMP TABLE pkey_resolve(temp_key integer PRIMARY KEY)", 0))
		check("temporary_shadow", "pkey_resolve", [][]any{{int64(1), "temp_key"}})
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, q := range []string{"SET search_path=pk_left,pk_right,public", "CREATE TEMP TABLE pkey_resolve(peer_key integer PRIMARY KEY)"} {
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, pkeyRows([][]any{{int64(1), "peer_key"}}, "pkey_resolve"))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(pkeyRows([][]any{{int64(1), "peer_key"}}, "pkey_resolve")))
		check("peer_temp_isolation", "pkey_resolve", [][]any{{int64(1), "temp_key"}})
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("DROP TABLE pg_temp.pkey_resolve", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("SET search_path=public", 0))
		run(fixture.Exec("DROP TABLE pg_temp.pkey_resolve", 0))
		check("temporary_retirement", "pkey_resolve", right)
		run(fixture.Exec("SET search_path=public", 0))
	})
}
