package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExternalSQLProviderDatabaseDrop(t *testing.T) {
	if !*fixture.Mode {
		t.Skip("physical external SQL provider comparison requires REFERENCE=1")
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			names := []string{"external_drop_target", "external_drop_peer"}
			fixture.RoleReferenceCleanup(t, []string{"external_drop_reader"})
			fixture.DatabaseReferenceCleanup(t, &names)
			fixture.SchemaReferenceCleanup(t, "external_sql_source")
			provider, handler := &externalSQLDropProvider{}, &externalSQLHandler{}
			p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
				if err := d.RegisterExternalSQLProvider(stuntgres.ExternalSQLProviderDefinition{Name: "external_sql_fixture", Version: fixture.ExternalSQLFixtureVersion, Provider: provider}); err != nil {
					return err
				}
				return d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "external_sql_fixture_handler"}, ExternalProvider: "external_sql_fixture", Wrapper: handler})
			})
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE SCHEMA external_sql_source", 0),
				fixture.Exec("CREATE TABLE external_sql_source.data(remote_id integer NOT NULL,remote_label text,remote_flag boolean,remote_bytes bytea)", 0),
				fixture.Exec("INSERT INTO external_sql_source.data VALUES(1,'x',true,decode('00ff','hex')),(2,NULL,false,NULL),(2,NULL,false,NULL),(3,'',NULL,decode('','hex'))", 4),
			} {
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
			}
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { externalSQLDropResources(t, p, b, provider, handler, prepared) })
			}
		})
	}
}

func externalSQLDropResources(t *testing.T, p *fixture.Pair, b fixture.Backend, provider *externalSQLDropProvider, handler *externalSQLHandler, prepared bool) {
	const target, peer = "external_drop_target", "external_drop_peer"
	run := func(c fixture.Executor, r *fixture.Recorder, s fixture.Step) {
		if s.State != "" {
			s.ErrorStage = "execute"
		}
		fixture.RunStep(t, p.Ctx, c, r, s)
	}
	for _, q := range []string{"CREATE ROLE external_drop_reader", "CREATE DATABASE " + target, "CREATE DATABASE " + peer} {
		run(b.DB, b.Recorder, fixture.Exec(q, 0))
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		for _, q := range []string{"DROP DATABASE IF EXISTS " + target + " WITH (FORCE)", "DROP DATABASE IF EXISTS " + peer + " WITH (FORCE)", "DROP ROLE external_drop_reader"} {
			if _, err := b.DB.ExecContext(ctx, q); err != nil {
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
		c   *sql.Conn
		r   *fixture.Recorder
		app string
	}{{c, rec, "external_drop_target_wire"}, {other, peerRec, "external_drop_peer_wire"}} {
		externalSQLProviderInstallPhysical(t, b, func(s fixture.Step) { run(item.c, item.r, s) })
		op := "SET"
		if b.PG {
			op = "ADD"
		}
		run(item.c, item.r, fixture.Exec("ALTER SERVER external_sql_live OPTIONS ("+op+" application_name '"+item.app+"')", 0))
	}
	var extras []stuntgres.ExternalSQLConnection
	handler.mu.Lock()
	handler.action = func(ctx context.Context, r stuntgres.ForeignRequest, _ stuntgres.ExternalSQLConnection) error {
		extra, err := r.ExternalSQL.Open(ctx, stuntgres.ExternalSQLConnectRequest{})
		if err != nil {
			return err
		}
		extras = append(extras, extra)
		_, err = extra.Execute(ctx, "SELECT 29")
		return err
	}
	handler.mu.Unlock()
	defer func() { handler.mu.Lock(); handler.action = nil; handler.mu.Unlock() }()
	selectRows := func(c fixture.Executor, r *fixture.Recorder) {
		step := externalSQLProviderRows(b.PG, "SELECT id,label,flag,bytes FROM external_sql_data ORDER BY id", externalSQLProviderExpectedRows())
		step.Prepared = prepared
		run(c, r, step)
	}
	selectRows(c, rec)
	selectRows(other, peerRec)
	handler.mu.Lock()
	handler.action = nil
	handler.mu.Unlock()
	if !b.PG {
		// Transaction-owned primary connections close before scan return, but a
		// PostgreSQL backend can process its closed socket asynchronously.
		// Join those actual exits before observing only the retained extras.
		for _, w := range provider.base.Snapshot() {
			if w.Activity().Closes != 0 {
				externalSQLBackendGone(t, p.Ctx, w.Info().BackendPID)
			}
		}
	}
	// PG's real postgres_fdw cache and native's actual capability-opened extra
	// handles are different host implementations of physical session ownership.
	// Both are independently observed in the genuine remote activity inventory.
	remotePIDs := func(app string) []int32 {
		rows, err := fixture.Admin.QueryContext(p.Ctx, "SELECT pid FROM pg_stat_activity WHERE application_name=$1", app)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []int32
		for rows.Next() {
			var pid int32
			if err := rows.Scan(&pid); err != nil {
				t.Fatal(err)
			}
			out = append(out, pid)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 {
			t.Fatal("expected one genuine retained remote owner", app, out)
		}
		return out
	}
	targetPIDs, peerPIDs := remotePIDs("external_drop_target_wire"), remotePIDs("external_drop_peer_wire")
	defer func() {
		other.Close()
		peerDB.Close()
		peerRaw.Close()
		for _, pid := range peerPIDs {
			externalSQLBackendGone(t, p.Ctx, pid)
		}
	}()
	var targetWire, peerWire *fixture.ExternalSQLWire
	var oldCapability stuntgres.ExternalSQLSession
	if !b.PG {
		if len(extras) != 2 {
			t.Fatal("actual callback did not retain both owned handles", len(extras))
		}
		for _, w := range provider.base.Snapshot() {
			if w.Info().BackendPID == targetPIDs[0] {
				targetWire = w
			}
			if w.Info().BackendPID == peerPIDs[0] {
				peerWire = w
			}
		}
		if targetWire == nil || peerWire == nil {
			t.Fatal("retained resource missing genuine transport")
		}
		handler.mu.Lock()
		oldCapability = handler.sessions[len(handler.sessions)-2]
		handler.mu.Unlock()
		if _, err := extras[0].Execute(p.Ctx, "BEGIN; INSERT INTO external_sql_source.data VALUES(99,'uncommitted',true,NULL)"); err != nil {
			t.Fatal(err)
		}
	}
	var independentHealthy func()
	if !b.PG {
		independentHealthy = externalSQLDropIndependent(t, p, provider)
	}
	ordinary, force := "DROP DATABASE "+target, "DROP DATABASE "+target+" WITH (FORCE)"
	var plain, forced fixture.Executor = b.DB, b.DB
	if prepared {
		one, err := b.DB.PrepareContext(p.Ctx, ordinary)
		if err != nil {
			t.Fatal(err)
		}
		defer one.Close()
		two, err := b.DB.PrepareContext(p.Ctx, force)
		if err != nil {
			t.Fatal(err)
		}
		defer two.Close()
		plain, forced = fixture.HeldStatement{Executor: b.DB, Statement: one, SQL: ordinary}, fixture.HeldStatement{Executor: b.DB, Statement: two, SQL: force}
	}
	alive := func() {
		for _, pid := range append(append([]int32{}, targetPIDs...), peerPIDs...) {
			var n int
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&n); err != nil || n != 1 {
				t.Fatal("refused DROP closed actual resource", pid, n, err)
			}
		}
		if !b.PG {
			for _, w := range []*fixture.ExternalSQLWire{targetWire, peerWire} {
				if w.Activity().Closes != 0 {
					t.Fatal("refused DROP prematurely closed handle", w.Activity())
				}
				if _, err := w.Execute(p.Ctx, "SELECT 31"); err != nil {
					t.Fatal("refused DROP invalidated physical handle", err)
				}
			}
		}
		var v int
		if err := c.QueryRowContext(p.Ctx, "SELECT 7").Scan(&v); err != nil || v != 7 {
			t.Fatal("refused DROP ended target", v, err)
		}
	}
	run(plain, b.Recorder, fixture.ImplicitError(ordinary, fixture.Diagnostic{Code: "55006", Message: `database "external_drop_target" is being accessed by other users`, Detail: "There is 1 other session using the database."}))
	alive()
	actor, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	defer actor.ExecContext(context.Background(), "RESET ROLE")
	run(actor, b.Recorder, fixture.Exec("SET ROLE external_drop_reader", 0))
	denied := fixture.ImplicitError(force, fixture.Diagnostic{Code: "42501", Message: "must be owner of database external_drop_target"})
	denied.Prepared = prepared
	run(actor, b.Recorder, denied)
	run(actor, b.Recorder, fixture.Exec("RESET ROLE", 0))
	alive()
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
			_, _ = c.ExecContext(context.Background(), "ROLLBACK PREPARED 'external_drop_gid'")
		}
	}()
	for _, s := range []fixture.Step{fixture.Exec("BEGIN", 0), fixture.Exec("INSERT INTO prepared_resource VALUES(9)", 1), fixture.Exec("PREPARE TRANSACTION 'external_drop_gid'", 0)} {
		run(origin, prepRec, s)
	}
	origin.Close()
	prepDB.Close()
	prepRaw.Close()
	fixture.DatabaseReferenceOwners(t, p, b.DB, b.Recorder, target, 1)
	run(forced, b.Recorder, fixture.ImplicitError(force, fixture.Diagnostic{Code: "55006", Message: `database "external_drop_target" is being used by prepared transactions`, Detail: "There is 1 prepared transaction using the database."}))
	alive()
	run(c, rec, fixture.Exec("ROLLBACK PREPARED 'external_drop_gid'", 0))
	pending = false
	if b.PG {
		run(forced, b.Recorder, fixture.Exec(force, 0))
	} else {
		before := peerWire.Activity()
		externalSQLDropDrain(t, p, b, c, other, provider, targetWire, force, forced, prepared, independentHealthy)
		if peerWire.Activity() != before {
			t.Fatal("DROP touched peer's actual transport", peerWire.Activity(), before)
		}
		independentHealthy()
	}
	for _, pid := range targetPIDs {
		externalSQLBackendGone(t, p.Ctx, pid)
	}
	if _, err := c.ExecContext(p.Ctx, "SELECT 1"); err == nil {
		t.Fatal("old physical externalSQL owner survived FORCE")
	}
	if !b.PG {
		if targetWire.Activity().Closes != 1 {
			t.Fatal("actual retained handle not closed exactly once", targetWire.Activity())
		}
		if _, err := extras[0].Execute(p.Ctx, "SELECT 1"); err == nil {
			t.Fatal("old owned connection still usable")
		}
		if leaked, err := oldCapability.Open(p.Ctx, stuntgres.ExternalSQLConnectRequest{}); err == nil {
			if leaked != nil {
				leaked.Close(p.Ctx)
			}
			t.Fatal("old execution capability resurrected owner")
		}
		if peerWire.Activity().Closes != 0 {
			t.Fatal("peer resource was closed")
		}
		if _, err := extras[1].Execute(p.Ctx, "SELECT 37"); err != nil {
			t.Fatal("peer resource did not survive", err)
		}
	}
	selectRows(other, peerRec)
	var n int
	if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM external_sql_source.data WHERE remote_id=99").Scan(&n); err != nil || n != 0 {
		t.Fatal("real remote uncommitted work survived termination", n, err)
	}
	run(b.DB, b.Recorder, fixture.Exec("CREATE DATABASE "+target, 0))
	fresh, freshRaw, freshRec := fixture.DatabaseScenarioPool(t, p, b.PG, target, "")
	run(fresh, freshRec, fixture.Select("SELECT count(*) FROM pg_foreign_server WHERE srvname='external_sql_live'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	if _, err := c.ExecContext(p.Ctx, "SELECT 1"); err == nil {
		t.Fatal("old physical owner rebound to recreated database")
	}
	if !b.PG {
		if _, err := extras[0].Execute(p.Ctx, "SELECT 1"); err == nil {
			t.Fatal("old owned connection resurrected after recreation")
		}
		if leaked, err := oldCapability.Open(p.Ctx, stuntgres.ExternalSQLConnectRequest{}); err == nil {
			if leaked != nil {
				leaked.Close(p.Ctx)
			}
			t.Fatal("old capability gained replacement authority")
		}
		independentHealthy()
	}
	fresh.Close()
	freshRaw.Close()
	fixture.DatabaseReferenceOwners(t, p, b.DB, b.Recorder, target, 0)
	run(forced, b.Recorder, fixture.Exec(force, 0))
	c.Close()
	db.Close()
	raw.Close()
	if !b.PG && targetWire.Activity().Closes != 1 {
		t.Fatal("old owner cleanup replayed after recreation", targetWire.Activity())
	}
}
