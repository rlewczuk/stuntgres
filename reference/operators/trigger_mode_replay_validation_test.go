package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// This baseline has no orphan: each rejection must identify the sole violation
// introduced by that edit. The sealed-orphan waiver has its separate witness.
func TestReferenceTriggerModeReplayValidation(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		setup := []fixture.Step{
			fixture.Exec("CREATE TABLE tm_edit_parent(id integer PRIMARY KEY)", 0),
			fixture.Exec("CREATE TABLE tm_edit_other(id integer PRIMARY KEY)", 0),
			fixture.Exec("CREATE TABLE tm_edit_child(id integer PRIMARY KEY,pid integer,CONSTRAINT tm_edit_fk FOREIGN KEY(pid) REFERENCES tm_edit_parent(id))", 0),
			fixture.Exec("CREATE TABLE tm_edit_text(v text)", 0),
			fixture.Exec("CREATE TABLE tm_edit_audit(v text)", 0),
			fixture.Exec("CREATE FUNCTION tm_edit_log() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tm_edit_audit VALUES('fired'); RETURN NULL; END$$", 0),
			fixture.Exec("CREATE TRIGGER quiet AFTER INSERT ON tm_edit_child FOR EACH ROW EXECUTE FUNCTION tm_edit_log()", 0),
			fixture.Exec("ALTER TABLE tm_edit_child DISABLE TRIGGER quiet", 0),
			fixture.Exec("INSERT INTO tm_edit_parent VALUES(101),(102)", 2),
			fixture.Exec("INSERT INTO tm_edit_other VALUES(102)", 1),
			fixture.Exec("INSERT INTO tm_edit_child VALUES(1,101)", 1),
			fixture.Exec("INSERT INTO tm_edit_text VALUES('baseline')", 1),
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, step := range setup { run(step) }
		if b.PG {
			// Replay actual native output on the independent oracle, not a
			// hand-written approximation or a native-derived expected result.
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			for _, step := range setup { fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, step) }
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
		saved := dump.String()
		q := "INSERT INTO tm_edit_child VALUES($1,$2) RETURNING id,pid"
		held := triggerDDLHeld(t, p, b, c, q)
		check := func(rows [][]any, text [][]any) {
			run(fixture.Select("SELECT id,pid FROM tm_edit_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, rows))
			run(fixture.Select("SELECT id FROM tm_edit_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(101)}, {int64(102)}}))
			run(fixture.Select("SELECT v FROM tm_edit_text ORDER BY v", []string{"v"}, []string{"TEXT"}, text))
			run(triggerModeCount("tm_edit_audit", 0))
			run(fixture.Select("SELECT tgname,tgenabled FROM pg_trigger WHERE tgrelid='tm_edit_child'::regclass AND NOT tgisinternal ORDER BY tgname", []string{"tgname", "tgenabled"}, []string{"NAME", "CHAR"}, [][]any{{"quiet", "D"}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='tm_edit_fk') AND tgenabled='O'", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			run(fixture.Exec("BEGIN", 0))
			s := fixture.Select(q, []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(50), int64(101)}}, 50, 101)
			held(s)
			run(fixture.Exec("ROLLBACK", 0))
			s = fixture.ErrorDiagnostic(q, fixture.ForeignKeyChild("tm_edit_child", "tm_edit_fk", "Key (pid)=(197) is not present in table \"tm_edit_parent\"."))
			s.Args = []any{51, 197}; s.ErrorStage = "execute"; held(s)
		}
		baselineRows := [][]any{{int64(1), int64(101)}}
		baselineText := [][]any{{"baseline"}}
		check(baselineRows, baselineText)
		restore := func(script string) error {
			if !b.PG { return stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)) }
			// PostgreSQL has no native Restore API. Its ordinary SQL replay runs
			// in an isolated transaction so target/held assertions stay identical.
			if _, err := c.ExecContext(p.Ctx, "BEGIN; DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil { t.Fatal(err) }
			_, err := c.ExecContext(p.Ctx, script)
			if err != nil {
				if _, rollback := c.ExecContext(p.Ctx, "ROLLBACK"); rollback != nil { t.Fatal(rollback) }
				return err
			}
			_, err = c.ExecContext(p.Ctx, "COMMIT")
			return err
		}
		editKey := func(key string) string {
			t.Helper()
			start := strings.Index(saved, `INSERT INTO "public"."tm_edit_child"`)
			if start < 0 { t.Fatal("missing canonical child data") }
			end := strings.IndexByte(saved[start:], '\n')
			if end < 0 { t.Fatal("unterminated child data") }; end += start
			line := saved[start:end]
			if strings.Count(line, "101") != 1 { t.Fatal("missing unique saved child key", line) }
			return saved[:start]+strings.Replace(line, "101", key, 1)+saved[end:]
		}
		reference := strings.Index(saved, "REFERENCES ")
		if reference < 0 { t.Fatal("missing saved FK declaration") }
		target := strings.Index(saved[reference:], "tm_edit_parent")
		if target < 0 { t.Fatal("missing saved FK target") }; target += reference
		constraintEdit := saved[:target]+"tm_edit_other"+saved[target+len("tm_edit_parent"):]
		for _, tc := range []struct{ name, script, detail string }{
			{"pre-footer-data", editKey("199"), "Key (pid)=(199) is not present in table \"tm_edit_parent\"."},
			{"appended-invalid-insert", saved+"INSERT INTO public.tm_edit_child VALUES(2,198);\n", "Key (pid)=(198) is not present in table \"tm_edit_parent\"."},
			{"pre-footer-constraint", constraintEdit, "Key (pid)=(101) is not present in table \"tm_edit_other\"."},
		} {
			err := restore(tc.script)
			if err == nil { t.Fatalf("%s accepted invalid edited data", tc.name) }
			want := fixture.ForeignKeyChild("tm_edit_child", "tm_edit_fk", tc.detail)
			fixture.AssertDiagnostic(t, err, &want)
			check(baselineRows, baselineText)
		}
		const markerText = "line one\n-- stuntgres-fk-trigger-mode-v2 !inside-literal!\nline three"
		for _, tc := range []struct{ name, script string; rows, text [][]any }{
			{"valid-data-edit", editKey("102"), [][]any{{int64(1), int64(102)}}, baselineText},
			{"valid-appended-insert", saved+"INSERT INTO public.tm_edit_child VALUES(2,102);\n", [][]any{{int64(1), int64(101)}, {int64(2), int64(102)}}, baselineText},
			{"marker-looking-literal", saved+"INSERT INTO public.tm_edit_text VALUES('"+markerText+"');\n", baselineRows, [][]any{{"baseline"}, {markerText}}},
		} {
			if err := restore(tc.script); err != nil { t.Fatalf("%s: %v", tc.name, err) }
			check(tc.rows, tc.text)
		}
	})
}
