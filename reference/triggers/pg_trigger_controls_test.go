package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgTriggerControlsScenario(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			pgTriggerSmokeSetup(t, p, b)
			pgTriggerLive(t, p, b, b.DB, "pgt_smoke", pgTriggerSmokeRow("pgt_smoke_trigger"))
			fixture.PgDescriptionRows(t, p, b, fixture.Select("INSERT INTO pgt_smoke VALUES(1,5) RETURNING v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			for _, q := range []string{"CREATE TABLE pgt_control_attrs(a integer,b integer,c integer)", "CREATE FUNCTION pgt_control_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.a:=NEW.a*2; RETURN NEW; END$$", "CREATE TRIGGER attrs BEFORE UPDATE OF c,a ON pgt_control_attrs FOR EACH ROW WHEN(OLD.a>0) EXECUTE FUNCTION pgt_control_fn('x','','é')"} {
				pgTriggerExec(t, p, b, b.DB, q)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgt_control_attrs VALUES(1,2,3)", 1))
			pgTriggerLive(t, p, b, b.DB, "pgt_control_attrs", [][]any{{"attrs", int64(19), "O", int64(3), "780000c3a900", "3 1", true}})
			fixture.PgDescriptionRows(t, p, b, fixture.Select("UPDATE pgt_control_attrs SET c=c RETURNING a", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}))
			for _, q := range []string{"CREATE TABLE pgt_control_parent(id integer PRIMARY KEY)", "CREATE TABLE pgt_control_child(id integer CONSTRAINT pgt_control_fk REFERENCES pgt_control_parent ON DELETE CASCADE)"} {
				pgTriggerExec(t, p, b, b.DB, q)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgt_control_parent VALUES(1)", 1))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgt_control_child VALUES(1)", 1))
			pgTriggerFKControlCheck(t, p, b)
			if b.PG {
				pgTriggerFKControlDrop(t, p, b)
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), "CREATE TRIGGER") {
				t.Fatal("actual definition missing from dump")
			}
			stmt, err := b.DB.PrepareContext(p.Ctx, "SELECT tgname,tgnargs,tgqual IS NOT NULL AS has_qual FROM pg_trigger WHERE tgname='pgt_smoke_trigger'")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: "SELECT tgname,tgnargs,tgqual IS NOT NULL AS has_qual FROM pg_trigger WHERE tgname='pgt_smoke_trigger'"}
			old, err := b.DB.QueryContext(p.Ctx, "SELECT tgqual FROM pg_trigger WHERE tgname='pgt_smoke_trigger'")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE TRIGGER invalid BEFORE INSERT ON missing_target FOR EACH ROW EXECUTE FUNCTION pgt_smoke_fn();")); err == nil {
				t.Fatal("late invalid restore succeeded")
			}
			pgTriggerLive(t, p, b, b.DB, "pgt_smoke", pgTriggerSmokeRow("pgt_smoke_trigger"))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			s := fixture.Select(held.SQL, []string{"tgname", "tgnargs", "has_qual"}, []string{"NAME", "INT2", "BOOL"}, [][]any{})
			fixture.PgDescriptionExecutorRows(t, p, b, held, s)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			s.Rows = [][]any{{"pgt_smoke_trigger", int64(3), true}}
			fixture.PgDescriptionExecutorRows(t, p, b, held, s)
			pgTriggerLive(t, p, b, b.DB, "pgt_smoke", pgTriggerSmokeRow("pgt_smoke_trigger"))
			pgTriggerLive(t, p, b, b.DB, "pgt_control_attrs", [][]any{{"attrs", int64(19), "O", int64(3), "780000c3a900", "3 1", true}})
			fixture.PgDescriptionRows(t, p, b, fixture.Select("UPDATE pgt_control_attrs SET a=a RETURNING a", []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}}))
			if !old.Next() {
				t.Fatal("retained tree missing", old.Err())
			}
			var tree string
			if err := old.Scan(&tree); err != nil || tree == "" {
				t.Fatal("old tree lost", tree, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("old cursor changed", old.Err())
			}
			fixture.PgDescriptionRows(t, p, b, fixture.Select("INSERT INTO pgt_smoke VALUES(2,5) RETURNING v", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			pgTriggerBoth(t, p, b, fixture.Select("SELECT c.relname,p.proname FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgname='pgt_smoke_trigger'", []string{"relname", "proname"}, []string{"NAME", "NAME"}, [][]any{{"pgt_smoke", "pgt_smoke_fn"}}))
			pgTriggerFKControlCheck(t, p, b)
			pgTriggerFKControlDrop(t, p, b)
		})
	}
}

func TestReferencePGTriggerNativeControls(t *testing.T) { pgTriggerControlsScenario(t) }
