package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerModeEffects(t *testing.T) {
	for _, route := range []string{"direct", "held"} {
		t.Run(route, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				triggerModeSetup(t, p, b, c)
				userOperatorExec(t, p, b, c,
					"CREATE TABLE tm_audit(label text)",
					"CREATE FUNCTION tm_audit_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tm_audit VALUES(TG_ARGV[0]); RETURN NULL; END$$",
					"CREATE TRIGGER r AFTER INSERT ON tm_data FOR EACH ROW EXECUTE FUNCTION tm_audit_fn('row')",
					"CREATE TRIGGER s AFTER INSERT OR UPDATE ON tm_data FOR EACH STATEMENT EXECUTE FUNCTION tm_audit_fn('statement')")
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				q := "INSERT INTO tm_data VALUES($1,5) RETURNING id,v"
				insert := run
				if route == "held" {
					insert = triggerDDLHeld(t, p, b, c, q)
				}
				id := int64(0)
				for _, mode := range triggerModeCases {
					for _, name := range []string{"a", "r", "s"} {
						run(fixture.Exec("ALTER TABLE tm_data "+mode.action+" "+name, 0))
					}
					for _, role := range []string{"origin", "local", "replica"} {
						run(fixture.Exec("SET session_replication_role="+role, 0))
						id++
						v := int64(5)
						var audit [][]any
						if triggerModeActive(mode.code, role) {
							v = 105
							audit = [][]any{{"row"}, {"statement"}}
						}
						s := triggerRows(q, []any{id, v})
						s.Args = []any{id}
						insert(s)
						run(fixture.Select("SELECT label FROM tm_audit ORDER BY label", []string{"label"}, []string{"TEXT"}, audit))
						run(fixture.Exec("DELETE FROM tm_audit", int64(len(audit))))
						run(fixture.Exec("UPDATE tm_data SET v=v WHERE id=-1", 0))
						var emptyAudit [][]any
						if triggerModeActive(mode.code, role) {
							emptyAudit = [][]any{{"statement"}}
						}
						run(fixture.Select("SELECT label FROM tm_audit", []string{"label"}, []string{"TEXT"}, emptyAudit))
						run(fixture.Exec("DELETE FROM tm_audit", int64(len(emptyAudit))))
					}
				}
				run(fixture.Exec("SET session_replication_role=origin", 0))
			})
		})
	}
	t.Run("mode-before-when-and-function", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerModeSetup(t, p, b, c)
			userOperatorExec(t, p, b, c,
				"CREATE TRIGGER z BEFORE INSERT ON tm_data FOR EACH ROW WHEN(1/(NEW.id-99)>0) EXECUTE FUNCTION tm_add()",
				"ALTER TABLE tm_data DISABLE TRIGGER a", "ALTER TABLE tm_data DISABLE TRIGGER z")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(triggerRows("INSERT INTO tm_data VALUES(99,5) RETURNING id,v", []any{int64(99), int64(5)}))
			run(fixture.Exec("DELETE FROM tm_data", 1))
			run(fixture.Exec("ALTER TABLE tm_data ENABLE REPLICA TRIGGER z", 0))
			run(fixture.Prepared(fixture.Exec("INSERT INTO tm_data VALUES(99,5)", 1)))
			run(fixture.Exec("DELETE FROM tm_data", 1))
			run(fixture.Exec("ALTER TABLE tm_data ENABLE TRIGGER z", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO tm_data VALUES(100,5),(99,5)", fixture.ControlsReferenceDiagnostic("22012", "division by zero")))
			run(triggerModeCount("tm_data", 0))
			userOperatorExec(t, p, b, c,
				"DROP TRIGGER z ON tm_data",
				"CREATE FUNCTION tm_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=1/(NEW.id-99); RETURN NEW; END$$",
				"CREATE TRIGGER z BEFORE INSERT ON tm_data FOR EACH ROW EXECUTE FUNCTION tm_fail()",
				"ALTER TABLE tm_data DISABLE TRIGGER z")
			run(fixture.Exec("INSERT INTO tm_data VALUES(99,5)", 1))
			run(fixture.Exec("DELETE FROM tm_data", 1))
			run(fixture.Exec("ALTER TABLE tm_data ENABLE TRIGGER z", 0))
			want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "tm_fail()", "assignment")
			run(fixture.ErrorDiagnostic("INSERT INTO tm_data VALUES(99,5)", want))
			run(triggerModeCount("tm_data", 0))
		})
	})
	t.Run("after-mode-before-when", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerModeSetup(t, p, b, c)
			userOperatorExec(t, p, b, c,
				"CREATE TABLE tm_audit(label text)",
				"CREATE FUNCTION tm_audit_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tm_audit VALUES('after'); RETURN NULL; END$$",
				"CREATE TRIGGER z AFTER INSERT ON tm_data FOR EACH ROW WHEN(1/(NEW.id-99)>0) EXECUTE FUNCTION tm_audit_fn()",
				"ALTER TABLE tm_data DISABLE TRIGGER z")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("INSERT INTO tm_data VALUES(99,5)", 1))
			run(fixture.Exec("DELETE FROM tm_data", 1))
			run(fixture.Exec("ALTER TABLE tm_data ENABLE REPLICA TRIGGER z", 0))
			run(fixture.Prepared(fixture.Exec("INSERT INTO tm_data VALUES(99,5)", 1)))
			run(fixture.Exec("DELETE FROM tm_data", 1))
			run(fixture.Exec("ALTER TABLE tm_data ENABLE TRIGGER z", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO tm_data VALUES(100,5),(99,5)", fixture.ControlsReferenceDiagnostic("22012", "division by zero")))
			run(triggerModeCount("tm_data", 0))
			run(triggerModeCount("tm_audit", 0))
		})
	})
}

func TestReferenceTriggerModeSelectors(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerModeSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			`CREATE TRIGGER "all" BEFORE INSERT ON tm_data FOR EACH ROW EXECUTE FUNCTION tm_add()`,
			`CREATE TRIGGER "user" BEFORE INSERT ON tm_data FOR EACH ROW EXECUTE FUNCTION tm_add()`,
			"CREATE TABLE tm_empty(id integer)")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("ALTER TABLE IF EXISTS tm_data ENABLE TRIGGER a", 0))
		run(fixture.Exec(`ALTER TABLE tm_data DISABLE TRIGGER "all"`, 0))
		run(triggerModeCatalog("tm_data", []any{"a", "O"}, []any{"all", "D"}, []any{"user", "O"}))
		run(triggerRows("INSERT INTO tm_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(205)}))
		run(fixture.Prepared(fixture.Exec("ALTER TABLE tm_data DISABLE TRIGGER USER", 0)))
		run(triggerRows("INSERT INTO tm_data VALUES(2,5) RETURNING id,v", []any{int64(2), int64(5)}))
		run(fixture.Exec(`ALTER TABLE tm_data ENABLE ALWAYS TRIGGER "user"`, 0))
		run(fixture.Exec("ALTER TABLE tm_data ENABLE TRIGGER ALL", 0))
		run(triggerModeCatalog("tm_data", []any{"a", "O"}, []any{"all", "O"}, []any{"user", "O"}))
		for _, selector := range []string{"ALL", "USER"} {
			for _, action := range []string{"ENABLE", "DISABLE"} {
				run(fixture.Exec("ALTER TABLE tm_empty "+action+" TRIGGER "+selector, 0))
			}
		}
		for _, tc := range []struct{ q, token string }{
			{"ALTER TABLE tm_data ENABLE REPLICA TRIGGER ALL", "ALL"},
			{"ALTER TABLE tm_data ENABLE ALWAYS TRIGGER USER", "USER"},
			{"ALTER TABLE tm_data DISABLE TRIGGER public.a", "."},
			{"ALTER TABLE tm_data DISABLE TRIGGER a garbage", "garbage"},
		} {
			want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+tc.token+`"`)
			want.Position = fixture.Position(len(tc.q) - len(tc.token) + 1)
			if tc.token == "." {
				want.Position = fixture.Position(len("ALTER TABLE tm_data DISABLE TRIGGER public") + 1)
			}
			run(fixture.ErrorDiagnostic(tc.q, want))
		}
		run(triggerModeCatalog("tm_data", []any{"a", "O"}, []any{"all", "O"}, []any{"user", "O"}))
	})
}
