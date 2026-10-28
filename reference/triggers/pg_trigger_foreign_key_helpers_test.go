package reference_test

import (
	"bytes"
	"fmt"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgTriggerFKIDs(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name string) []int64 {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, "SELECT oid::bigint,tgname,tgfoid::bigint FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conname=$1::name) ORDER BY oid", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := make([]int64, 0, 4)
	for rows.Next() {
		var id, function int64
		var name string
		if err := rows.Scan(&id, &name, &function); err != nil {
			t.Fatal(err)
		}
		kind := "a"
		if function == 1644 || function == 1645 {
			kind = "c"
		}
		if id <= 0 || name != fmt.Sprintf("RI_ConstraintTrigger_%s_%d", kind, id) || len(ids) > 0 && id <= ids[len(ids)-1] {
			t.Fatal("actual persistent trigger identity", id, name, function)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil || len(ids) != 4 {
		t.Fatal("four distinct persisted identities", ids, err)
	}
	return ids
}

func pgTriggerFKControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgt_fk_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, q := range []string{"CREATE ROLE pgt_fk_reader", "CREATE TABLE pgt_fk_self(id integer PRIMARY KEY)", "ALTER TABLE pgt_fk_self ADD CONSTRAINT pgt_fk_self_fk FOREIGN KEY(id) REFERENCES pgt_fk_self(id) NOT VALID", "CREATE FUNCTION pgt_fk_user_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE TRIGGER pgt_fk_user BEFORE INSERT ON pgt_fk_self FOR EACH ROW EXECUTE FUNCTION pgt_fk_user_fn()"} {
				pgTriggerExec(t, p, b, b.DB, q)
			}
			ids := pgTriggerFKIDs(t, p, b, b.DB, "pgt_fk_self_fk")
			countQuery := "SELECT count(*) AS n,count(DISTINCT oid) AS identities FROM pg_trigger WHERE tgrelid='pgt_fk_self'::regclass"
			countStep := fixture.Select(countQuery, []string{"n", "identities"}, []string{"INT8", "INT8"}, [][]any{{int64(5), int64(5)}})
			pgTriggerBoth(t, p, b, countStep)
			pgTriggerBoth(t, p, b, fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='pgt_fk_self_fk') AND tgrelid=tgconstrrelid AND tgisinternal", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			pgTriggerBoth(t, p, b, fixture.Select("SELECT trigger_name FROM information_schema.triggers WHERE event_object_table='pgt_fk_self'", []string{"trigger_name"}, []string{"NAME"}, [][]any{{"pgt_fk_user"}}))
			// Resolve the actual identity as owner; catalog readability does not
			// grant the low role schema USAGE for regclass name resolution.
			relationID := fixture.PGDescriptionRelationID(t, p, b, "pgt_fk_self")
			readerCount := fixture.Select("SELECT count(*) AS n,count(DISTINCT oid) AS identities FROM pg_trigger WHERE tgrelid=$1::oid", []string{"n", "identities"}, []string{"INT8", "INT8"}, [][]any{{int64(5), int64(5)}}, relationID)
			owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
			pgTriggerExec(t, p, b, reader, "SET ROLE pgt_fk_reader")
			fixture.PgDescriptionExecutorRows(t, p, b, reader, readerCount)
			fixture.PgDescriptionExecutorRows(t, p, b, reader, fixture.Prepared(readerCount))
			fixture.PgDescriptionExecutorRows(t, p, b, reader, fixture.Select("SELECT trigger_name FROM information_schema.triggers WHERE event_object_table='pgt_fk_self'", []string{"trigger_name"}, []string{"NAME"}, [][]any{}))
			pgTriggerExec(t, p, b, reader, "RESET ROLE")
			pgTriggerExec(t, p, b, owner, "ALTER TABLE pgt_fk_self RENAME CONSTRAINT pgt_fk_self_fk TO pgt_fk_self_renamed")
			if renamed := pgTriggerFKIDs(t, p, b, owner, "pgt_fk_self_renamed"); !reflect.DeepEqual(ids, renamed) {
				t.Fatal("constraint rename replaced actual trigger identities", ids, renamed)
			}
			fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO pgt_fk_self VALUES(1)", 1))
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				heldSQL := "SELECT count(*) AS n FROM pg_trigger WHERE tgname='pgt_fk_user' OR tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='pgt_fk_self_renamed')"
				stmt, err := owner.PrepareContext(p.Ctx, heldSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: owner, Statement: stmt, SQL: heldSQL}
				old, err := owner.QueryContext(p.Ctx, "SELECT oid::bigint,tgname FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='pgt_fk_self_renamed') ORDER BY oid")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				step := fixture.Select(heldSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
				fixture.PgDescriptionExecutorRows(t, p, b, held, step)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				step.Rows = [][]any{{int64(5)}}
				fixture.PgDescriptionExecutorRows(t, p, b, held, step)
				pgTriggerFKIDs(t, p, b, owner, "pgt_fk_self_renamed")
				fixture.PgDescriptionExecutorRows(t, p, b, owner, countStep)
				for _, expected := range ids {
					if !old.Next() {
						t.Fatal("old FK capture lost row", old.Err())
					}
					var oid int64
					var name string
					if err := old.Scan(&oid, &name); err != nil || oid != expected || name == "" {
						t.Fatal("retained old FK identity", oid, expected, name, err)
					}
				}
				if old.Next() || old.Err() != nil {
					t.Fatal("old FK capture changed", old.Err())
				}
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("INSERT INTO pgt_fk_self VALUES(2)", 1))
			}
			pgTriggerExec(t, p, b, owner, "ALTER TABLE pgt_fk_self DROP CONSTRAINT pgt_fk_self_renamed")
			countStep.Rows = [][]any{{int64(1), int64(1)}}
			fixture.PgDescriptionExecutorRows(t, p, b, owner, countStep)
		})
	}
}

func pgTriggerFKControlCheck(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	pgTriggerBoth(t, p, b, fixture.Select("SELECT c.relname,t.tgtype,p.proname,t.tgconstraint=f.oid AS constraint_link,t.tgconstrindid=f.conindid AS index_link FROM pg_trigger t JOIN pg_constraint f ON f.oid=t.tgconstraint JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_proc p ON p.oid=t.tgfoid WHERE f.conname='pgt_control_fk' AND t.tgisinternal ORDER BY c.relname,t.tgtype", []string{"relname", "tgtype", "proname", "constraint_link", "index_link"}, []string{"NAME", "INT2", "NAME", "BOOL", "BOOL"}, [][]any{{"pgt_control_child", int64(5), "RI_FKey_check_ins", true, true}, {"pgt_control_child", int64(17), "RI_FKey_check_upd", true, true}, {"pgt_control_parent", int64(9), "RI_FKey_cascade_del", true, true}, {"pgt_control_parent", int64(17), "RI_FKey_noaction_upd", true, true}}))
	pgTriggerBoth(t, p, b, fixture.Select("SELECT relname,relhastriggers FROM pg_class WHERE relname IN ('pgt_control_child','pgt_control_parent') ORDER BY relname", []string{"relname", "relhastriggers"}, []string{"NAME", "BOOL"}, [][]any{{"pgt_control_child", true}, {"pgt_control_parent", true}}))
}

func pgTriggerFKControlDrop(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM pgt_control_parent WHERE id=1", 1))
	pgTriggerBoth(t, p, b, fixture.Select("SELECT count(*) AS n FROM pgt_control_child", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	pgTriggerFKControlCheck(t, p, b)
	pgTriggerExec(t, p, b, b.DB, "ALTER TABLE pgt_control_child DROP CONSTRAINT pgt_control_fk")
	pgTriggerBoth(t, p, b, fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgrelid='pgt_control_child'::regclass OR tgrelid='pgt_control_parent'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	pgTriggerLive(t, p, b, b.DB, "pgt_smoke", pgTriggerSmokeRow("pgt_smoke_trigger"))
}
