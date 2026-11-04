package reference_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGReplicationOriginStatusControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"status_control_actor", "status_control_empty"})
	databases := []string{"status_capacity_peer"}
	fixture.DatabaseReferenceCleanup(t, &databases)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			names := []string{"status_control_absent", "status_control_zero", "status_control_high"}
			originStateCleanup(t, b.DB, names...)
			for _, name := range names {
				originStateCreate(t, p, b, b.DB, name)
			}
			originStatusAdvance(t, p, b, b.DB, names[1], "0/0")
			originStatusAdvance(t, p, b, b.DB, names[2], "FFFFFFFF/FFFFFFFF")
			q := originStatusSelect + " ORDER BY external_id"
			want := func() [][]any {
				return [][]any{{originStateID(t, p, b.DB, names[2]), names[2], "FFFFFFFF/FFFFFFFF", "0/0"}, {originStateID(t, p, b.DB, names[1]), names[1], "0/0", "0/0"}}
			}
			originStatusRows(t, p, b, b.DB, q, want())
			if b.PG {
				return
			}
			admin := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			for _, sql := range []string{
				"CREATE ROLE status_control_actor", "CREATE ROLE status_control_empty",
				"GRANT SELECT(external_id,remote_lsn) ON pg_catalog.pg_replication_origin_status TO status_control_actor",
				"GRANT EXECUTE ON FUNCTION pg_catalog.pg_show_replication_origin_status() TO status_control_actor",
				"GRANT SELECT ON pg_catalog.pg_replication_origin_status TO status_control_empty",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_show_replication_origin_status() FROM status_control_empty",
			} {
				originStateRun(t, p, b, admin, fixture.Exec(sql, 0))
			}
			originStatusCleanup(t, admin, "RESET SESSION AUTHORIZATION",
				"REVOKE SELECT(external_id,remote_lsn) ON pg_catalog.pg_replication_origin_status FROM status_control_actor",
				"REVOKE ALL ON pg_catalog.pg_replication_origin_status FROM status_control_empty",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_show_replication_origin_status() FROM status_control_actor")
			originStatusInitial(t, p, b, admin)
			checkColumnACL := func() {
				step := fixture.Select("SELECT attname::text,attacl::text FROM pg_attribute WHERE attrelid='pg_catalog.pg_replication_origin_status'::regclass AND attnum>0 ORDER BY attnum",
					[]string{"attname", "attacl"}, []string{"TEXT", "TEXT"},
					[][]any{{"local_id", nil}, {"external_id", "{status_control_actor=r/postgres}"}, {"remote_lsn", "{status_control_actor=r/postgres}"}, {"local_lsn", nil}})
				originStateRun(t, p, b, admin, step)
				originStateRun(t, p, b, admin, fixture.Prepared(step))
			}
			checkColumnACL()
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			old, err := b.DB.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			originStatusMetadata(t, old, false)
			columns, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			oldWant := want()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"SELECT 1;", "SELECT pg_catalog.pg_replication_origin_advance('status_control_high','bad'::pg_catalog.pg_lsn);"} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+suffix)); err == nil {
					t.Fatal("malformed replay installed")
				}
				originStatusRows(t, p, b, b.DB, q, oldWant)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			originStatusRows(t, p, b, b.DB, q, oldWant)
			// Reuse the existing real control input boundary, not sleeps or a
			// second replay mechanism. A newer progress write rejects publication.
			if err := subscriptionRestoreDuringRead(t, p, dump.Bytes(), func(context.CancelFunc) { originStatusAdvance(t, p, b, b.DB, names[2], "0/9") }); err == nil {
				t.Fatal("stale progress replacement installed")
			}
			originStatusRows(t, p, b, b.DB, q, [][]any{{oldWant[0][0], names[2], "0/9", "0/0"}, oldWant[1]})
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			originStatusRows(t, p, b, b.DB, q, nil)
			for range 2 {
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				originStatusRows(t, p, b, b.DB, q, want())
				fixture.OidReferenceRows(t, p.Ctx, b, held, fixture.Select(q, originStatusColumns, originStatusTypes, want()))
				originStatusInitial(t, p, b, admin)
				checkColumnACL()
				originStateRun(t, p, b, admin, fixture.Exec("SET SESSION AUTHORIZATION status_control_actor", 0))
				projection := fixture.Select("SELECT external_id,remote_lsn FROM pg_replication_origin_status ORDER BY external_id", []string{"external_id", "remote_lsn"}, []string{"TEXT", "PG_LSN"}, [][]any{{names[2], "FFFFFFFF/FFFFFFFF"}, {names[1], "0/0"}})
				originStateRun(t, p, b, admin, projection)
				originStateRun(t, p, b, admin, fixture.Prepared(projection))
				originStatusDenied(t, p, b, admin, originStatusSelect, "view pg_replication_origin_status")
				originStateRun(t, p, b, admin, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				originStateRun(t, p, b, admin, fixture.Exec("SET SESSION AUTHORIZATION status_control_empty", 0))
				originStatusDenied(t, p, b, admin, originStatusSelect, "function pg_show_replication_origin_status")
				originStateRun(t, p, b, admin, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
			// Retained eager values survive live advance, identity removal and
			// replacement; descriptors were obtained while the handle was open.
			for i := range 2 {
				if !old.Next() {
					t.Fatal(old.Err())
				}
				var id int64
				var name, remote, local string
				if err := old.Scan(&id, &name, &remote, &local); err != nil || id != oldWant[i][0] || name != oldWant[i][1] || remote != oldWant[i][2] || local != "0/0" {
					t.Fatal(id, name, remote, local, err)
				}
			}
			if old.Next() || old.Err() != nil {
				t.Fatal(old.Err())
			}
			old.Close()
			if columns[0].DatabaseTypeName() != "OID" || columns[1].Name() != "external_id" {
				t.Fatal("retained descriptor changed")
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("semantic origin/ACL replay changed dump")
			}
			originStatusCapacityControls(t, p, b, dump.Bytes())
			// Raw NULL restores ordinary SQL defaults, not the owner-only seed
			// or an explicit-empty ACL. Column grants remain independent.
			nullRepair := "UPDATE pg_catalog.pg_class SET relacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_replication_origin_status'::pg_catalog.regclass;\n" +
				"UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_show_replication_origin_status()'::pg_catalog.regprocedure;\n"
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+nullRepair)); err != nil {
				t.Fatal(err)
			}
			checkNullACL := func() {
				steps := []fixture.Step{
					originStateScalar("SELECT relacl::text AS v FROM pg_class WHERE oid='pg_catalog.pg_replication_origin_status'::regclass", "TEXT", nil),
					originStateScalar("SELECT proacl::text AS v FROM pg_proc WHERE oid=6014", "TEXT", nil),
					fixture.Select("SELECT has_table_privilege('status_control_actor','pg_catalog.pg_replication_origin_status','SELECT') AS can_select,has_function_privilege('status_control_actor','pg_catalog.pg_show_replication_origin_status()','EXECUTE') AS can_execute",
						[]string{"can_select", "can_execute"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}),
				}
				for _, step := range steps {
					originStateRun(t, p, b, admin, step)
					originStateRun(t, p, b, admin, fixture.Prepared(step))
				}
				checkColumnACL()
				originStatusInitial(t, p, b, admin)
				originStateRun(t, p, b, admin, fixture.Exec("SET SESSION AUTHORIZATION status_control_actor", 0))
				projection := fixture.Select("SELECT external_id,remote_lsn FROM pg_replication_origin_status ORDER BY external_id",
					[]string{"external_id", "remote_lsn"}, []string{"TEXT", "PG_LSN"},
					[][]any{{names[2], "FFFFFFFF/FFFFFFFF"}, {names[1], "0/0"}})
				originStateRun(t, p, b, admin, projection)
				originStateRun(t, p, b, admin, fixture.Prepared(projection))
				originStatusDenied(t, p, b, admin, originStatusSelect, "view pg_replication_origin_status")
				originStateRun(t, p, b, admin, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				originStateRun(t, p, b, admin, fixture.Exec("SET SESSION AUTHORIZATION status_control_empty", 0))
				originStatusDenied(t, p, b, admin, originStatusSelect, "view pg_replication_origin_status")
				originStateRun(t, p, b, admin, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				originStatusRows(t, p, b, b.DB, q, want())
			}
			checkNullACL()
			var nullACL bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &nullACL); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(nullACL.Bytes())); err != nil {
				t.Fatal(err)
			}
			checkNullACL()
			var nullRedump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &nullRedump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(nullACL.Bytes(), nullRedump.Bytes()) {
				t.Fatal("raw-NULL relation/routine ACL replay changed dump")
			}
			// Restore the granted baseline before the separate empty-ACL case.
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			// Explicit-empty raw ACLs are replay state, not reconstructed defaults.
			for _, query := range []string{
				"REVOKE SELECT(external_id,remote_lsn) ON pg_catalog.pg_replication_origin_status FROM status_control_actor",
				"REVOKE ALL ON pg_catalog.pg_replication_origin_status FROM status_control_empty,postgres",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_show_replication_origin_status() FROM status_control_actor,postgres",
			} {
				originStateRun(t, p, b, admin, fixture.Exec(query, 0))
			}
			var emptyACL bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &emptyACL); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emptyACL.Bytes())); err != nil {
				t.Fatal(err)
			}
			originStateRun(t, p, b, admin, originStateScalar("SELECT relacl::text AS v FROM pg_class WHERE oid='pg_catalog.pg_replication_origin_status'::regclass", "TEXT", "{}"))
			originStateRun(t, p, b, admin, originStateScalar("SELECT proacl::text AS v FROM pg_proc WHERE oid=6014", "TEXT", "{}"))
			originStatusInitial(t, p, b, admin)
			originStatusRows(t, p, b, b.DB, q, want())
		})
	}
}

func TestReferencePGReplicationOriginStatusFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"status_smoke_actor"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			originStateCleanup(t, b.DB, "status_smoke", "status_smoke_absent")
			actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			originStatusCleanup(t, actor, "ROLLBACK", "RESET ROLE")
			run := func(e fixture.Executor, s fixture.Step) { originStateRun(t, p, b, e, s) }
			run(admin, fixture.Exec("CREATE TABLE status_smoke_sentinel(n integer)", 0))
			run(admin, fixture.Exec("INSERT INTO status_smoke_sentinel VALUES(41)", 1))
			originStatusRows(t, p, b, admin, originStatusSelect, nil)
			id := originStateCreate(t, p, b, admin, "status_smoke")
			originStatusRows(t, p, b, admin, originStatusSelect, nil)
			originStatusAdvance(t, p, b, admin, "status_smoke", "0/2")
			stmt, err := actor.PrepareContext(p.Ctx, originStatusSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: originStatusSelect}
			want := [][]any{{id, "status_smoke", "0/2", "0/0"}}
			originStatusRows(t, p, b, admin, originStatusSelect, want)
			fixture.OidReferenceRows(t, p.Ctx, b, held, fixture.Select(originStatusSelect, originStatusColumns, originStatusTypes, want))
			run(actor, fixture.Exec("BEGIN", 0))
			originStatusAdvance(t, p, b, actor, "status_smoke", "0/3")
			run(actor, fixture.Exec("ROLLBACK", 0))
			originStatusRows(t, p, b, admin, originStatusSelect, [][]any{{id, "status_smoke", "0/3", "0/0"}})
			originStatusAdvance(t, p, b, admin, "status_smoke", "0/0")
			want = [][]any{{id, "status_smoke", "0/0", "0/0"}}
			originStatusRows(t, p, b, admin, originStatusSelect, want)
			originStateProgress(t, p, b, admin, "status_smoke", nil)
			run(admin, fixture.Exec("CREATE ROLE status_smoke_actor", 0))
			originStatusCleanup(t, admin, "REVOKE SELECT ON pg_replication_origin_status FROM status_smoke_actor",
				"REVOKE EXECUTE ON FUNCTION pg_show_replication_origin_status() FROM status_smoke_actor")
			run(admin, fixture.Exec("GRANT SELECT ON pg_replication_origin_status TO status_smoke_actor", 0))
			run(actor, fixture.Exec("SET ROLE status_smoke_actor", 0))
			originStatusDenied(t, p, b, actor, originStatusSelect, "function pg_show_replication_origin_status")
			originStatusDenied(t, p, b, held, originStatusSelect, "function pg_show_replication_origin_status")
			run(admin, fixture.Exec("GRANT EXECUTE ON FUNCTION pg_show_replication_origin_status() TO status_smoke_actor", 0))
			fixture.OidReferenceRows(t, p.Ctx, b, held, fixture.Select(originStatusSelect, originStatusColumns, originStatusTypes, want))
			run(admin, fixture.Exec("REVOKE SELECT ON pg_replication_origin_status FROM status_smoke_actor", 0))
			originStatusDenied(t, p, b, held, originStatusSelect, "view pg_replication_origin_status")
			run(actor, fixture.Exec("RESET ROLE", 0))
			if !b.PG {
				originStateCreate(t, p, b, admin, "status_smoke_absent")
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				retained, err := admin.QueryContext(p.Ctx, originStatusSelect)
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				originStatusMetadata(t, retained, false)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				originStatusRows(t, p, b, admin, originStatusSelect, nil)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				id = originStateID(t, p, admin, "status_smoke")
				originStatusRows(t, p, b, admin, originStatusSelect, [][]any{{id, "status_smoke", "0/0", "0/0"}})
				if !retained.Next() {
					t.Fatal(retained.Err())
				}
				var oldID int64
				var name, remote, local string
				if err := retained.Scan(&oldID, &name, &remote, &local); err != nil || oldID != want[0][0] || name != "status_smoke" || remote != "0/0" || local != "0/0" {
					t.Fatal(oldID, name, remote, local, err)
				}
				retained.Close()
			}
			run(admin, fixture.Exec("SELECT pg_replication_origin_drop('status_smoke')", 1))
			originStatusRows(t, p, b, admin, originStatusSelect, nil)
			run(admin, originStateScalar("SELECT n AS v FROM status_smoke_sentinel", "INT4", int64(41)))
		})
	}
}

func originStatusCapacityControls(t *testing.T, p *fixture.Pair, b fixture.Backend, dump []byte) {
	t.Helper()
	// Keep nine genuine peer subscription slots beside one destination slot.
	// Importing the two-slot dump would require eleven, exceeding the real
	// fixture's ten-slot capacity. Rejection must preserve the complete view.
	originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_drop('status_control_zero')", 1))
	originStateRun(t, p, b, b.DB, fixture.Exec("CREATE DATABASE status_capacity_peer", 0))
	peer, _, recorder := fixture.DatabaseScenarioPool(t, p, false, "status_capacity_peer", "")
	pb := b
	pb.DB, pb.Recorder = peer, recorder
	var subscriptions []string
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		for _, name := range subscriptions {
			if _, err := peer.ExecContext(ctx, "DROP SUBSCRIPTION "+name); err != nil {
				t.Error(err)
			}
		}
	}()
	expected := [][]any{{originStateID(t, p, b.DB, "status_control_high"), "status_control_high", "FFFFFFFF/FFFFFFFF", "0/0"}}
	for i := range 9 {
		name := fmt.Sprintf("status_capacity_%d", i)
		originCreate(t, p, pb, peer, name, false)
		subscriptions = append(subscriptions, name)
		id, canonical := originIdentity(t, p, peer, name)
		originStatusAdvance(t, p, pb, peer, canonical, "0/0")
		expected = append(expected, []any{id, canonical, "0/0", "0/0"})
	}
	check := func() {
		t.Helper()
		for _, prepared := range []bool{false, true} {
			step := fixture.Select(originStatusSelect, originStatusColumns, originStatusTypes, expected)
			step.Prepared, step.Bag = prepared, true
			fixture.OidReferenceRows(t, p.Ctx, b, b.DB, step)
		}
	}
	check()
	err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump))
	want := fixture.ControlsReferenceDiagnostic("53400", `could not replace replication origin progress: replacement requires 11 active replication state slots, but "max_active_replication_origins" is 10`)
	want.Hint = `Increase "max_active_replication_origins" and try again.`
	fixture.AssertDiagnostic(t, err, &want)
	check()
	// Cleanup must finish before restoring the original two-slot state.
	for _, name := range subscriptions {
		originStateRun(t, p, pb, peer, fixture.Exec("DROP SUBSCRIPTION "+name, 0))
	}
	subscriptions = nil
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
}
