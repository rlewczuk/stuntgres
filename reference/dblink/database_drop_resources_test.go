package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDblinkConnectionDatabaseDrop(t *testing.T) {
	if !*fixture.Mode {
		t.Skip("genuine external PostgreSQL connections require REFERENCE=1")
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			names := []string{"dbc_drop_target", "dbc_drop_peer"}
			fixture.RoleReferenceCleanup(t, []string{"dbc_drop_reader"})
			fixture.DatabaseReferenceCleanup(t, &names)
			provider := &dropDblinkProvider{}
			p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
				if err := d.RegisterExternalSQLProvider(stuntgres.ExternalSQLProviderDefinition{Name: "drop_dblink", Version: fixture.ExternalSQLFixtureVersion, Provider: provider}); err != nil {
					return err
				}
				return d.BindDblinkExternalSQLProvider("drop_dblink")
			})
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { dropDblinkResources(t, p, b, provider, prepared) })
			}
		})
	}
}

func dropDblinkResources(t *testing.T, p *fixture.Pair, b fixture.Backend, provider *dropDblinkProvider, prepared bool) {
	const target, peer = "dbc_drop_target", "dbc_drop_peer"
	admin := b.DB
	run := func(c fixture.Executor, r *fixture.Recorder, s fixture.Step) {
		if s.State != "" {
			s.ErrorStage = "execute"
		}
		fixture.RunStep(t, p.Ctx, c, r, s)
	}
	for _, q := range []string{"CREATE ROLE dbc_drop_reader", "CREATE DATABASE " + target, "CREATE DATABASE " + peer} {
		run(admin, b.Recorder, fixture.Exec(q, 0))
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		for _, q := range []string{"DROP DATABASE IF EXISTS " + target + " WITH (FORCE)", "DROP DATABASE IF EXISTS " + peer + " WITH (FORCE)", "DROP ROLE dbc_drop_reader"} {
			if _, err := admin.ExecContext(ctx, q); err != nil {
				t.Error("resource cleanup", q, err)
			}
		}
	})
	db, raw, rec := fixture.DatabaseScenarioPool(t, p, b.PG, target, "")
	peerDB, peerRaw, peerRec := fixture.DatabaseScenarioPool(t, p, b.PG, peer, "")
	c, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	other, err := peerDB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	for _, item := range []struct {
		c *sql.Conn
		r *fixture.Recorder
	}{{c, rec}, {other, peerRec}} {
		run(item.c, item.r, fixture.Exec("CREATE SCHEMA dbc_ext", 0))
		run(item.c, item.r, fixture.Exec("CREATE EXTENSION dblink SCHEMA dbc_ext", 0))
	}
	s := &connectionScenario{t: t, p: p, b: b, c: c, db: db, provider: &provider.base, prepared: prepared}
	ps := &connectionScenario{t: t, p: p, b: b, c: other, db: peerDB, provider: &provider.base, prepared: prepared}
	s.b.Recorder, ps.b.Recorder = rec, peerRec
	_, named := s.open(new("owned"))
	_, unnamed := s.open(nil)
	_, peerPID := ps.open(new("peer_owned"))
	defer func() { other.Close(); peerDB.Close(); peerRaw.Close(); connectionGone(t, p.Ctx, peerPID) }()
	oldWires := make(map[int32]*fixture.ExternalSQLWire)
	if !b.PG {
		for _, pid := range []int32{named, unnamed, peerPID} {
			oldWires[pid] = s.wire(pid)
		}
	}
	var independentHealthy func()
	if !b.PG {
		independentHealthy = dropDblinkIndependent(t, p, provider)
	}
	ordinary, force := "DROP DATABASE "+target, "DROP DATABASE "+target+" WITH (FORCE)"
	var plain, forced fixture.Executor = admin, admin
	if prepared {
		one, err := admin.PrepareContext(p.Ctx, ordinary)
		if err != nil {
			t.Fatal(err)
		}
		defer one.Close()
		two, err := admin.PrepareContext(p.Ctx, force)
		if err != nil {
			t.Fatal(err)
		}
		defer two.Close()
		plain, forced = fixture.HeldStatement{Executor: admin, Statement: one, SQL: ordinary}, fixture.HeldStatement{Executor: admin, Statement: two, SQL: force}
	}
	alive := func() {
		s.list("owned")
		ps.list("peer_owned")
		for _, pid := range []int32{named, unnamed, peerPID} {
			var n int
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&n); err != nil || n != 1 {
				t.Fatal("refused DROP closed real backend", pid, n, err)
			}
			if !b.PG && oldWires[pid].Activity().Closes != 0 {
				t.Fatal("refused DROP closed registered handle", pid)
			}
		}
	}
	busy := fixture.ImplicitError(ordinary, fixture.Diagnostic{Code: "55006", Message: `database "dbc_drop_target" is being accessed by other users`, Detail: "There is 1 other session using the database."})
	run(plain, b.Recorder, busy)
	alive()
	// A failed owner check must not even request eligible physical teardown.
	actor, err := admin.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	defer actor.ExecContext(context.Background(), "RESET ROLE")
	run(actor, b.Recorder, fixture.Exec("SET ROLE dbc_drop_reader", 0))
	denied := fixture.ImplicitError(force, fixture.Diagnostic{Code: "42501", Message: "must be owner of database dbc_drop_target"})
	denied.Prepared = prepared
	run(actor, b.Recorder, denied)
	run(actor, b.Recorder, fixture.Exec("RESET ROLE", 0))
	alive()
	// A genuine detached prepared transaction blocks FORCE before any resource
	// cancellation; its physical origin is closed, not merely returned to a pool.
	run(c, rec, fixture.Exec("CREATE TABLE prepared_resource(v integer)", 0))
	prepDB, prepRaw, prepRec := fixture.DatabaseScenarioPool(t, p, b.PG, target, "")
	prepDB.SetMaxIdleConns(0)
	origin, err := prepDB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	pending := true
	defer func() {
		if pending {
			_, _ = c.ExecContext(context.Background(), "ROLLBACK PREPARED 'dbc_drop_gid'")
		}
	}()
	for _, q := range []string{"BEGIN", "INSERT INTO prepared_resource VALUES(9)", "PREPARE TRANSACTION 'dbc_drop_gid'"} {
		affected := int64(0)
		if q == "INSERT INTO prepared_resource VALUES(9)" {
			affected = 1
		}
		run(origin, prepRec, fixture.Exec(q, affected))
	}
	origin.Close()
	prepDB.Close()
	prepRaw.Close()
	fixture.DatabaseReferenceOwners(t, p, admin, b.Recorder, target, 1)
	run(forced, b.Recorder, fixture.ImplicitError(force, fixture.Diagnostic{Code: "55006", Message: `database "dbc_drop_target" is being used by prepared transactions`, Detail: "There is 1 prepared transaction using the database."}))
	alive()
	run(c, rec, fixture.Exec("ROLLBACK PREPARED 'dbc_drop_gid'", 0))
	pending = false
	if b.PG {
		run(forced, b.Recorder, fixture.Exec(force, 0))
	} else {
		peerBefore := oldWires[peerPID].Activity()
		dropDblinkDrain(t, p, b, c, other, provider, s.conninfo("dbc_drop_pending", false), named, force, forced, prepared, independentHealthy)
		if oldWires[peerPID].Activity() != peerBefore {
			t.Fatal("DROP touched peer's actual transport", oldWires[peerPID].Activity(), peerBefore)
		}
		independentHealthy()
	}
	for _, pid := range []int32{named, unnamed} {
		connectionGone(t, p.Ctx, pid)
		if !b.PG {
			if oldWires[pid].Activity().Closes != 1 {
				t.Fatal("DROP did not close each actual handle exactly once", pid, oldWires[pid].Activity())
			}
			if _, err := oldWires[pid].Execute(p.Ctx, "SELECT 1"); err == nil {
				t.Fatal("retired real handle still executes", pid)
			}
		}
	}
	if _, err := c.ExecContext(p.Ctx, "SELECT 1"); err == nil {
		t.Fatal("old physical target owner survived FORCE")
	}
	ps.list("peer_owned")
	ps.remote(new("peer_owned"), peerPID, "SET statement_timeout = 0")
	if !b.PG && oldWires[peerPID].Activity().Closes != 0 {
		t.Fatal("peer resource was closed")
	}
	run(admin, b.Recorder, fixture.Exec("CREATE DATABASE "+target, 0))
	fresh, freshRaw, freshRec := fixture.DatabaseScenarioPool(t, p, b.PG, target, "")
	for _, q := range []string{"CREATE SCHEMA dbc_ext", "CREATE EXTENSION dblink SCHEMA dbc_ext"} {
		run(fresh, freshRec, fixture.Exec(q, 0))
	}
	step := fixture.ArrayReferenceSelect("SELECT dbc_ext.dblink_get_connections() AS value", []string{"value"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{nil}}, b.PG)
	run(fresh, freshRec, step)
	if _, err := c.ExecContext(p.Ctx, "SELECT 1"); err == nil {
		t.Fatal("old physical owner rebound to recreated database")
	}
	if !b.PG {
		independentHealthy()
		for _, pid := range []int32{named, unnamed} {
			if _, err := oldWires[pid].Execute(p.Ctx, "SELECT 1"); err == nil {
				t.Fatal("old real handle resurrected after recreation", pid)
			}
		}
	}
	fresh.Close()
	freshRaw.Close()
	fixture.DatabaseReferenceOwners(t, p, admin, b.Recorder, target, 0)
	run(forced, b.Recorder, fixture.Exec(force, 0)) // SAME held DROP resolves only the new incarnation.
	c.Close()
	db.Close()
	raw.Close()
	if !b.PG {
		for _, pid := range []int32{named, unnamed} {
			if oldWires[pid].Activity().Closes != 1 {
				t.Fatal("recreation/old Close replayed retired cleanup", pid)
			}
		}
	}
}
