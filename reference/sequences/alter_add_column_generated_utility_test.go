package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func addGeneratedUtilityNotice(t *testing.T) {
	addGeneratedRoutes(t, func(g *addGeneratedCase) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(g.t, g.p, g.b.PG, &notices)
		n := *g
		n.c = c
		n.b.Recorder = rec
		g = &n
		g.exec("CREATE TABLE g(a integer)", 0)
		g.exec("INSERT INTO g VALUES(2)", 1)
		q := "ALTER TABLE g ADD IF NOT EXISTS b integer GENERATED ALWAYS AS(a+1) STORED"
		alter := g.hold(q)
		if len(notices) != 0 {
			g.t.Fatal("Prepare emitted notice", notices)
		}
		g.unchanged([][]any{{int64(2)}})
		g.held(alter, fixture.Exec(q, 0))
		if len(notices) != 0 {
			g.t.Fatal("first execution skipped", notices)
		}
		g.held(alter, fixture.Exec(q, 0))
		g.notices(notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: `column "b" of relation "g" already exists, skipping`}})
		g.run(fixture.Select("SELECT * FROM g", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
	})
}

func addGeneratedRewriteEvents(t *testing.T) {
	addGeneratedRoutes(t, func(g *addGeneratedCase) {
		g.exec("CREATE TABLE g(a integer)", 0)
		g.exec("INSERT INTO g VALUES(2)", 1)
		g.exec("CREATE TABLE generated_audit(target oid,reason integer)", 0)
		g.exec("CREATE FUNCTION generated_rewrite_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO generated_audit VALUES(pg_event_trigger_table_rewrite_oid(),pg_event_trigger_table_rewrite_reason()); END$$", 0)
		g.exec("CREATE EVENT TRIGGER generated_rewrite ON table_rewrite EXECUTE FUNCTION generated_rewrite_fn()", 0)
		defer func() {
			if _, err := g.c.ExecContext(g.p.Ctx, "DROP EVENT TRIGGER generated_rewrite"); err != nil {
				g.t.Error(err)
			}
		}()
		for _, def := range []string{"v integer GENERATED ALWAYS AS(a/0) VIRTUAL", "d integer GENERATED ALWAYS AS(a/0)"} {
			g.exec("ALTER TABLE g ADD "+def, 0)
			g.run(fixture.Select("SELECT reason FROM generated_audit", []string{"reason"}, []string{"INT4"}, nil))
		}
		for _, def := range []string{"s integer GENERATED ALWAYS AS(7) STORED", "n integer GENERATED ALWAYS AS(NULL) STORED"} {
			g.exec("ALTER TABLE g ADD "+def, 0)
			g.run(fixture.Select("SELECT target='g'::regclass AS correct_target,reason FROM generated_audit", []string{"correct_target", "reason"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(2)}}))
			g.exec("DELETE FROM generated_audit", 1)
		}
		g.run(fixture.Select("SELECT a,s,n FROM g", []string{"a", "s", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(7), nil}}))
	})
}
