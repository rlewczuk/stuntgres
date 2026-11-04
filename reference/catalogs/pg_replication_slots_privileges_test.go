package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func slotsDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query, object string) {
	t.Helper()
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + object})
	s.SelectRows, s.ErrorStage = true, "execute"
	slotsRun(t, p, b, e, s)
	slotsRun(t, p, b, e, fixture.Prepared(s))
}

func TestReferencePGReplicationSlotsPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"slots_actor", "slots_repl", "slots_group"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			exec := func(e fixture.Executor, q string) { slotsRun(t, p, b, e, fixture.Exec(q, 0)) }
			originStatusCleanup(t, actor, "RESET SESSION AUTHORIZATION")
			if b.PG {
				originStatusCleanup(t, admin,
					"UPDATE pg_catalog.pg_class SET relacl='{stuntgres=arwdDxtm/stuntgres,=r/stuntgres}'::aclitem[] WHERE oid='pg_catalog.pg_replication_slots'::regclass",
					"UPDATE pg_catalog.pg_proc SET proacl=NULL WHERE oid=3781")
			}
			originStatusCleanup(t, admin,
				"GRANT SELECT ON pg_catalog.pg_replication_slots TO PUBLIC",
				"GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() TO PUBLIC",
				"REVOKE SELECT(slot_name) ON pg_catalog.pg_replication_slots FROM slots_actor",
				"REVOKE ALL ON pg_catalog.pg_replication_slots FROM slots_actor,slots_repl,slots_group",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() FROM slots_actor,slots_repl,slots_group",
				"REVOKE slots_group,pg_monitor,pg_read_all_stats FROM slots_actor")
			for _, q := range []string{"CREATE ROLE slots_actor LOGIN", "CREATE ROLE slots_repl LOGIN REPLICATION", "CREATE ROLE slots_group", "CREATE TABLE slots_priv_sentinel(n integer)"} {
				exec(admin, q)
			}
			slotsRun(t, p, b, admin, fixture.Exec("INSERT INTO slots_priv_sentinel VALUES(41)", 1))
			slotsInitial(t, p, b, admin)
			q := slotsSelect + " WHERE slot_name IN ('slots_priv_permanent','slots_priv_temporary') ORDER BY slot_name"
			var want [][]any
			for _, tc := range []struct {
				name string
				temporary bool
				executor *sql.Conn
			}{{"slots_priv_permanent", false, admin}, {"slots_priv_temporary", true, owner}} {
				if b.PG && !tc.temporary {
					originStatusCleanup(t, admin, "SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name='slots_priv_permanent' AND NOT temporary")
				}
				if b.PG && tc.temporary {
					originStatusCleanup(t, owner, "SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name='slots_priv_temporary'")
				}
				query := "SELECT * FROM pg_create_physical_replication_slot($1::name,false,$2::boolean)"
				if b.PG {
					var name string
					var lsn sql.NullString
					if err := tc.executor.QueryRowContext(p.Ctx, query, tc.name, tc.temporary).Scan(&name, &lsn); err != nil || name != tc.name || lsn.Valid {
						t.Fatal(name, lsn, err)
					}
					var inactive any
					var validTime bool
					if err := admin.QueryRowContext(p.Ctx, "SELECT inactive_since,inactive_since IS NULL OR inactive_since<=clock_timestamp() FROM pg_replication_slots WHERE slot_name=$1", tc.name).Scan(&inactive, &validTime); err != nil || !validTime {
						t.Fatal(inactive, validTime, err)
					}
					var pid any
					if tc.temporary {
						var n int64
						if err := owner.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&n); err != nil {
							t.Fatal(err)
						}
						pid = n
					}
					want = append(want, []any{tc.name, nil, "physical", nil, nil, tc.temporary, tc.temporary, pid, nil, nil, nil, nil, nil, nil, false, nil, fixture.Normalize(inactive), nil, nil, false, false})
				} else {
					s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "0A000", Message: "pg_create_physical_replication_slot physical replication slots and WAL reservation are unsupported in embedded mode"}, tc.name, tc.temporary)
					s.SelectRows, s.ErrorStage = true, "execute"
					slotsRun(t, p, b, tc.executor, s)
				}
			}
			stmt, err := actor.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: q}
			limit, err := actor.PrepareContext(p.Ctx, slotsSelect + " LIMIT 0")
			if err != nil { t.Fatal(err) }
			defer limit.Close()
			heldLimit := fixture.HeldStatement{Executor: actor, Statement: limit, SQL: slotsSelect + " LIMIT 0"}
			eliminated, err := actor.PrepareContext(p.Ctx, slotsSelect + " WHERE FALSE")
			if err != nil { t.Fatal(err) }
			defer eliminated.Close()
			heldFalse := fixture.HeldStatement{Executor: actor, Statement: eliminated, SQL: slotsSelect + " WHERE FALSE"}
			read := func(e fixture.Executor) {
				for _, prepared := range []bool{false, true} {
					step := fixture.Select(q, slotsColumns, slotsTypes, want)
					step.Prepared = prepared
					fixture.OidReferenceRows(t, p.Ctx, b, e, step)
				}
			}
			for _, role := range []string{"slots_actor", "slots_repl"} {
				exec(actor, "SET SESSION AUTHORIZATION " + role)
				read(actor)
				read(held)
				exec(actor, "RESET SESSION AUTHORIZATION")
			}
			exec(actor, "SET SESSION AUTHORIZATION slots_actor")
			for _, role := range []string{"pg_monitor", "pg_read_all_stats"} {
				exec(admin, "GRANT " + role + " TO slots_actor")
				read(held)
				exec(admin, "REVOKE " + role + " FROM slots_actor")
			}
			s := fixture.ImplicitError("SELECT * FROM pg_create_physical_replication_slot('slots_priv_forbidden')", fixture.Diagnostic{Code: "42501", Message: "permission denied to use replication slots", Detail: "Only roles with the REPLICATION attribute may use replication slots."})
			s.SelectRows, s.ErrorStage = true, "execute"
			slotsRun(t, p, b, actor, s)
			exec(admin, "REVOKE SELECT ON pg_catalog.pg_replication_slots FROM PUBLIC")
			for _, query := range []string{q, slotsSelect + " LIMIT 0", slotsSelect + " WHERE FALSE"} {
				slotsDenied(t, p, b, actor, query, "view pg_replication_slots")
			}
			slotsDenied(t, p, b, held, q, "view pg_replication_slots")
			slotsDenied(t, p, b, heldLimit, slotsSelect + " LIMIT 0", "view pg_replication_slots")
			slotsDenied(t, p, b, heldFalse, slotsSelect + " WHERE FALSE", "view pg_replication_slots")
			exec(admin, "GRANT SELECT ON pg_catalog.pg_replication_slots TO slots_actor")
			read(held)
			exec(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() FROM PUBLIC")
			slotsDenied(t, p, b, actor, q, "function pg_get_replication_slots")
			slotsDenied(t, p, b, held, q, "function pg_get_replication_slots")
			slotsDenied(t, p, b, actor, slotsSelect + " LIMIT 0", "function pg_get_replication_slots")
			slotsDenied(t, p, b, heldLimit, slotsSelect + " LIMIT 0", "function pg_get_replication_slots")
			slotsEmpty(t, p, b, heldFalse, slotsSelect + " WHERE FALSE")
			exec(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() TO slots_actor")
			read(held)
			exec(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() FROM slots_actor")
			slotsDenied(t, p, b, held, q, "function pg_get_replication_slots")
			exec(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() TO slots_actor")
			exec(admin, "REVOKE SELECT ON pg_catalog.pg_replication_slots FROM slots_actor")
			exec(admin, "GRANT SELECT(slot_name) ON pg_catalog.pg_replication_slots TO slots_actor")
			var names [][]any
			if b.PG { names = [][]any{{"slots_priv_permanent"}, {"slots_priv_temporary"}} }
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT slot_name FROM pg_replication_slots WHERE slot_name IN ('slots_priv_permanent','slots_priv_temporary') ORDER BY slot_name", []string{"slot_name"}, []string{"NAME"}, names)
				step.Prepared = prepared
				slotsRun(t, p, b, actor, step)
			}
			for _, query := range []string{"SELECT slot_name FROM pg_replication_slots WHERE active", "SELECT slot_name FROM pg_replication_slots ORDER BY restart_lsn", "SELECT * FROM pg_replication_slots"} {
				slotsDenied(t, p, b, actor, query, "view pg_replication_slots")
			}
			exec(admin, "REVOKE SELECT(slot_name) ON pg_catalog.pg_replication_slots FROM slots_actor")
			exec(admin, "GRANT SELECT ON pg_catalog.pg_replication_slots TO slots_group")
			exec(admin, "GRANT slots_group TO slots_actor")
			read(held)
			exec(admin, "REVOKE slots_group FROM slots_actor")
			slotsDenied(t, p, b, held, q, "view pg_replication_slots")
			exec(admin, "GRANT slots_group TO slots_actor")
			read(held)
			exec(actor, "RESET SESSION AUTHORIZATION")
			exec(admin, "GRANT SELECT ON pg_catalog.pg_replication_slots TO PUBLIC")
			exec(admin, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() TO PUBLIC")
			exec(admin, "REVOKE slots_group FROM slots_actor")
			exec(admin, "REVOKE ALL ON pg_catalog.pg_replication_slots FROM slots_actor,slots_repl,slots_group")
			exec(admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() FROM slots_actor,slots_repl,slots_group")
			slotsRun(t, p, b, admin, fixture.Select("SELECT has_table_privilege('slots_actor','pg_catalog.pg_replication_slots','SELECT') AS can_select,has_function_privilege('slots_actor','pg_catalog.pg_get_replication_slots()','EXECUTE') AS can_execute", []string{"can_select", "can_execute"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
			ownerName := "postgres"
			if b.PG { ownerName = "stuntgres" }
			slotsRun(t, p, b, admin, fixture.Select("SELECT initprivs::text FROM pg_init_privs WHERE classoid=1259 AND objoid='pg_catalog.pg_replication_slots'::regclass AND objsubid=0 AND privtype='i'", []string{"initprivs"}, []string{"TEXT"}, [][]any{{"{" + ownerName + "=arwdDxtm/" + ownerName + ",=r/" + ownerName + "}"}}))
			slotsRun(t, p, b, admin, fixture.Select("SELECT initprivs::text FROM pg_init_privs WHERE classoid=1255 AND objoid=3781 AND objsubid=0 AND privtype='i'", []string{"initprivs"}, []string{"TEXT"}, nil))
			slotsRun(t, p, b, admin, fixture.Select("SELECT n FROM slots_priv_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(41)}}))
			read(admin)
		})
	}
}
