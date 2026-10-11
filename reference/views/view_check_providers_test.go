package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewCheckRuleTriggerBoundary(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0), fixture.Exec("CREATE VIEW inner_v AS SELECT a,b FROM t WHERE a<10 WITH LOCAL CHECK OPTION", 0), fixture.Exec("CREATE VIEW outer_v AS SELECT a,b FROM inner_v WHERE a>0 WITH CASCADED CHECK OPTION", 0))
		trigger := "CREATE TRIGGER route INSTEAD OF INSERT ON inner_v FOR EACH ROW EXECUTE FUNCTION route_insert()"
		rule := "CREATE RULE route AS ON INSERT TO inner_v DO INSTEAD INSERT INTO t VALUES(NEW.a,NEW.b)"
		if !b.PG {
			// Providers are not engine mechanisms. Pin existing rejection, never success.
			for _, q := range []string{trigger, rule} {
				want := fixture.Diagnostic{Code: "42601", Message: "syntax error at or near \"RULE\"", Position: "8"}
				if q == trigger {
					want.Code = "0A000"
					want.Message = "CREATE syntax is unsupported"
				}
				s := fixture.ImplicitError(q, want)
				s.Prepared = held
				s.ErrorStage = "execute"
				if held {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			run(viewCheckPairRows())
			return
		}
		run(fixture.Exec("CREATE FUNCTION route_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO t VALUES(NEW.a,NEW.b); RETURN NEW; END $$", 0), fixture.Exec(trigger, 0), fixture.Exec("INSERT INTO inner_v VALUES(20,1)", 1), fixture.Exec("INSERT INTO outer_v VALUES(30,1)", 1), viewCheckError("INSERT INTO outer_v VALUES(-1,1)", "outer_v", "-1, 1"), fixture.Exec("ALTER VIEW outer_v SET(check_option=local)", 0), fixture.Exec("INSERT INTO outer_v VALUES(40,1)", 1), fixture.Exec("DROP TRIGGER route ON inner_v", 0), fixture.Exec(rule, 0), fixture.Exec("INSERT INTO outer_v VALUES(-2,1)", 1), fixture.Exec("ALTER VIEW outer_v SET(check_option=cascaded)", 0), fixture.Exec("INSERT INTO outer_v VALUES(-3,1)", 1), viewCheckPairRows([]any{int64(20), int64(1)}, []any{int64(30), int64(1)}, []any{int64(40), int64(1)}, []any{int64(-2), int64(1)}, []any{int64(-3), int64(1)}))
		run(fixture.Exec("DROP RULE route ON inner_v", 0), fixture.Exec("CREATE RULE base_route AS ON INSERT TO t DO INSTEAD NOTHING", 0), fixture.Exec("INSERT INTO outer_v VALUES(-4,1)", 0), fixture.Exec("DROP RULE base_route ON t", 0))
		run(fixture.Exec("CREATE FUNCTION change_new() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.a := -10; RETURN NEW; END $$", 0), fixture.Exec("CREATE TRIGGER change BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION change_new()", 0), viewCheckError("INSERT INTO outer_v VALUES(1,1)", "outer_v", "-10, 1"), viewCheckPairRows([]any{int64(20), int64(1)}, []any{int64(30), int64(1)}, []any{int64(40), int64(1)}, []any{int64(-2), int64(1)}, []any{int64(-3), int64(1)}))
	})
}
