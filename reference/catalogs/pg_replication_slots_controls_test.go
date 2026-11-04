package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGReplicationSlotsControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"slots_control_actor", "slots_control_other", "slots_control_column"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { slotsRun(t, p, b, admin, s) }
			if b.PG {
				originStatusCleanup(t, admin,
					"UPDATE pg_catalog.pg_class SET relacl='{stuntgres=arwdDxtm/stuntgres,=r/stuntgres}'::aclitem[] WHERE oid='pg_catalog.pg_replication_slots'::regclass",
					"UPDATE pg_catalog.pg_proc SET proacl=NULL WHERE oid=3781")
			}
			originStatusCleanup(t, admin, "RESET SESSION AUTHORIZATION",
				"GRANT SELECT ON pg_catalog.pg_replication_slots TO PUBLIC",
				"GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() TO PUBLIC",
				"REVOKE SELECT ON pg_catalog.pg_replication_slots FROM slots_control_actor",
				"REVOKE SELECT(slot_name) ON pg_catalog.pg_replication_slots FROM slots_control_column",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() FROM slots_control_actor,slots_control_other,slots_control_column")
			for _, q := range []string{
				"CREATE ROLE slots_control_actor",
				"CREATE ROLE slots_control_other",
				"CREATE ROLE slots_control_column",
				"CREATE TABLE slots_control_sentinel(n integer)",
				"CREATE VIEW slots_control_view AS SELECT slot_name AS slot,restart_lsn AS retained FROM pg_catalog.pg_replication_slots",
				"REVOKE SELECT ON pg_catalog.pg_replication_slots FROM PUBLIC",
				"GRANT SELECT ON pg_catalog.pg_replication_slots TO slots_control_actor",
				"GRANT SELECT(slot_name) ON pg_catalog.pg_replication_slots TO slots_control_column",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() FROM PUBLIC",
				"GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() TO slots_control_other,slots_control_column",
			} { run(fixture.Exec(q, 0)) }
			run(fixture.Exec("INSERT INTO slots_control_sentinel VALUES(41)", 1))
			stmt, err := admin.PrepareContext(p.Ctx, slotsSelect)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: admin, Statement: stmt, SQL: slotsSelect}
			retained, err := admin.QueryContext(p.Ctx, slotsSelect)
			if err != nil { t.Fatal(err) }
			slotsMetadata(t, retained, b.PG)
			descriptors, err := retained.ColumnTypes()
			if err != nil { t.Fatal(err) }
			// pq streams: release its physical connection before subsequent SQL.
			if b.PG {
				if retained.Next() || retained.Err() != nil { t.Fatal(retained.Err()) }
				if err := retained.Close(); err != nil { t.Fatal(err) }
			} else { defer retained.Close() }
			owner := "postgres"
			if b.PG { owner = "stuntgres" }
			wantACL := "{" + owner + "=arwdDxtm/" + owner + ",slots_control_actor=r/" + owner + "}"
			var attributes [][]any
			for i, name := range slotsColumns {
				var acl any
				if i == 0 { acl = "{slots_control_column=r/" + owner + "}" }
				attributes = append(attributes, []any{name, acl})
			}
			check := func() {
				// Raw metadata plus separately executable outsiders distinguish
				// exact SELECT replay from reconstructed PUBLIC SELECT defaults.
				relacl := fixture.Select("SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_replication_slots'::regclass", []string{"relacl"}, []string{"TEXT"}, [][]any{{wantACL}})
				attacl := fixture.Select("SELECT attname::text,attacl::text FROM pg_attribute WHERE attrelid='pg_catalog.pg_replication_slots'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attacl"}, []string{"TEXT", "TEXT"}, attributes)
				run(relacl)
				run(fixture.Prepared(relacl))
				run(attacl)
				run(fixture.Prepared(attacl))
				run(fixture.Select("SELECT has_function_privilege('slots_control_other','pg_catalog.pg_get_replication_slots()','EXECUTE') AS executable", []string{"executable"}, []string{"BOOL"}, [][]any{{true}}))
				slotsEmpty(t, p, b, held, slotsSelect)
				run(fixture.Select("SELECT * FROM slots_control_view", []string{"slot", "retained"}, []string{"NAME", "PG_LSN"}, nil))
				run(fixture.Select("SELECT n FROM slots_control_sentinel", []string{"n"}, []string{"INT4"}, [][]any{{int64(41)}}))
				run(fixture.Exec("SET SESSION AUTHORIZATION slots_control_other", 0))
				slotsDenied(t, p, b, held, slotsSelect, "view pg_replication_slots")
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION slots_control_column", 0))
				projection := fixture.Select("SELECT slot_name FROM pg_catalog.pg_replication_slots", []string{"slot_name"}, []string{"NAME"}, nil)
				run(projection)
				run(fixture.Prepared(projection))
				slotsDenied(t, p, b, held, slotsSelect, "view pg_replication_slots")
				slotsDenied(t, p, b, admin, "SELECT slot_name FROM pg_replication_slots WHERE active", "view pg_replication_slots")
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION slots_control_actor", 0))
				slotsDenied(t, p, b, held, slotsSelect, "function pg_get_replication_slots")
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() TO slots_control_actor", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION slots_control_actor", 0))
				slotsEmpty(t, p, b, held, slotsSelect)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() FROM slots_control_actor", 0))
			}
			check()
			if !b.PG {
				s := fixture.ImplicitError("SELECT * FROM pg_create_physical_replication_slot('slots_control_absent')", fixture.Diagnostic{Code: "0A000", Message: "pg_create_physical_replication_slot physical replication slots and WAL reservation are unsupported in embedded mode"})
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
				for _, directive := range []string{"pg_create_physical_replication_slot(", "pg_create_logical_replication_slot(", "pg_replication_slot_advance("} {
					if strings.Contains(dump.String(), directive) { t.Fatal("dump invented slot state", directive) }
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
				slotsEmpty(t, p, b, held, slotsSelect)
				for range 2 {
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
					check()
				}
				if retained.Next() || retained.Err() != nil { t.Fatal("replacement invented retained slots", retained.Err()) }
				if err := retained.Close(); err != nil { t.Fatal(err) }
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil { t.Fatal(err) }
				if !bytes.Equal(dump.Bytes(), redump.Bytes()) { t.Fatal("slot view/ACL replay changed dump") }
			}
			for i, descriptor := range descriptors {
				if descriptor.Name() != slotsColumns[i] || descriptor.DatabaseTypeName() != slotsTypes[i] { t.Fatal("retained descriptor changed", i) }
			}
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_replication_slots() TO PUBLIC", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION slots_control_actor", 0))
			slotsEmpty(t, p, b, held, slotsSelect)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			if !b.PG { slotsRawACLControls(t, p, b, admin) }
		})
	}
}
