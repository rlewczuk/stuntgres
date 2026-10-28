package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerModeAuthorization(t *testing.T) {
	t.Run("ownership-and-system", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"tm_owner", "tm_actor"})
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerModeSetup(t, p, b, c)
			names := triggerModeFKSetup(t, p, b, c)
			userOperatorExec(t, p, b, c,
				"CREATE TRIGGER z BEFORE INSERT ON tm_child FOR EACH ROW EXECUTE FUNCTION tm_add()",
				"CREATE ROLE tm_owner", "CREATE ROLE tm_actor",
				"GRANT USAGE ON SCHEMA public TO tm_owner,tm_actor",
				"GRANT TRIGGER ON tm_data TO tm_actor",
				"ALTER TABLE tm_child OWNER TO tm_owner", "SET SESSION AUTHORIZATION tm_actor")
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, prepared := range []bool{false, true} {
				for _, name := range []string{"a", "missing"} {
					run(triggerDDLError("ALTER TABLE tm_data DISABLE TRIGGER "+name, "42501", "must be owner of table tm_data", prepared))
				}
			}
			userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION tm_owner")
			run(fixture.Exec("ALTER TABLE tm_child DISABLE TRIGGER USER", 0))
			for _, q := range []string{
				"ALTER TABLE tm_child DISABLE TRIGGER " + triggerModeQuote(names[2]),
				"ALTER TABLE tm_child ENABLE TRIGGER ALL",
				"ALTER TABLE tm_child DISABLE TRIGGER ALL",
				"ALTER TABLE tm_child ENABLE TRIGGER z, DISABLE TRIGGER " + triggerModeQuote(names[2]),
			} {
				for _, prepared := range []bool{false, true} {
					// ALL's first catalog trigger is not portable; explicitly name both
					// check slots and require its actual identity, never a fixed OID.
					var err error
					if prepared {
						stmt, prepareErr := c.PrepareContext(p.Ctx, q)
						if prepareErr != nil {
							t.Fatal("utility must prepare before execution authorization", prepareErr)
						}
						_, err = stmt.ExecContext(p.Ctx)
						stmt.Close()
					} else {
						_, err = c.ExecContext(p.Ctx, q)
					}
					got := fixture.ExtractDiagnostic(t, err)
					want := fixture.ControlsReferenceDiagnostic("42501", `permission denied: "`+names[2]+`" is a system trigger`)
					other := fixture.ControlsReferenceDiagnostic("42501", `permission denied: "`+names[3]+`" is a system trigger`)
					if got != want && !(strings.HasSuffix(q, " TRIGGER ALL") && got == other) {
						t.Fatalf("system-trigger diagnostic: %#v", got)
					}
				}
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT tgenabled FROM pg_trigger WHERE tgrelid='tm_child'::regclass AND tgname='z'", []string{"tgenabled"}, []string{"CHAR"}, [][]any{{"D"}}))
			run(fixture.Exec("ALTER TABLE tm_child ENABLE TRIGGER USER", 0))
		})
	})
	t.Run("missing-and-kind", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerModeSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE VIEW tm_view AS SELECT * FROM tm_data", "CREATE MATERIALIZED VIEW tm_mat AS SELECT * FROM tm_data")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, prepared := range []bool{false, true} {
				for _, mode := range triggerModeCases {
					run(triggerDDLError("ALTER TABLE tm_data "+mode.action+" missing", "42704", `trigger "missing" for table "tm_data" does not exist`, prepared))
				}
				for _, action := range []string{"DISABLE TRIGGER", "ENABLE TRIGGER", "ENABLE REPLICA TRIGGER", "ENABLE ALWAYS TRIGGER", "DISABLE TRIGGER ALL", "ENABLE TRIGGER USER"} {
					for _, relation := range []struct{ name, detail string }{{"tm_view", "views"}, {"tm_mat", "materialized views"}} {
						q := "ALTER TABLE " + relation.name + " " + action
						if action != "DISABLE TRIGGER ALL" && action != "ENABLE TRIGGER USER" {
							q += " missing"
						}
						want := fixture.ControlsReferenceDiagnostic("42809", `ALTER action `+action+` cannot be performed on relation "`+relation.name+`"`)
						want.Detail = "This operation is not supported for " + relation.detail + "."
						s := fixture.ErrorDiagnostic(q, want)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						run(s)
					}
				}
			}
			run(triggerDDLError(`ALTER TABLE tm_data DISABLE TRIGGER "mis""sing"`, "42704", `trigger "mis"sing" for table "tm_data" does not exist`, false))
		})
	})
}

func TestReferenceTriggerModeAtomicity(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerModeSetup(t, p, b, c)
		names := triggerModeFKSetup(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(triggerDDLError("ALTER TABLE tm_data DISABLE TRIGGER a, ENABLE TRIGGER missing", "42704", `trigger "missing" for table "tm_data" does not exist`, true))
		run(triggerModeCatalog("tm_data", []any{"a", "O"}))
		run(triggerRows("INSERT INTO tm_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(105)}))
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run(fixture.Exec("ALTER TABLE tm_data DISABLE TRIGGER a", 0))
		run(triggerModeCatalog("tm_data", []any{"a", "D"}))
		run(fixture.Exec("ROLLBACK", 0))
		run(triggerModeCatalog("tm_data", []any{"a", "O"}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT modes", 0))
		run(fixture.Exec("ALTER TABLE tm_parent DISABLE TRIGGER "+triggerModeQuote(names[0]), 0))
		run(fixture.Exec("ALTER TABLE tm_data ENABLE ALWAYS TRIGGER a", 0))
		run(fixture.Exec("ROLLBACK TO modes", 0))
		run(triggerModeCatalog("tm_data", []any{"a", "O"}))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER TABLE tm_data DISABLE TRIGGER a", 0))
		run(fixture.Exec("COMMIT", 0))
		run(triggerModeCatalog("tm_data", []any{"a", "D"}))
		// ADD executes before the MISC phase even though DISABLE is written first.
		run(fixture.Exec("CREATE TABLE tm_new(id integer,pid integer)", 0))
		run(fixture.Exec("ALTER TABLE tm_new DISABLE TRIGGER ALL, ADD CONSTRAINT tm_new_fk FOREIGN KEY(pid) REFERENCES tm_parent(id)", 0))
		run(fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgrelid='tm_new'::regclass AND tgenabled='D'", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(fixture.Exec("CREATE TABLE tm_bad(id integer,pid integer)", 0))
		run(fixture.Exec("INSERT INTO tm_bad VALUES(1,99)", 1))
		run(fixture.ErrorDiagnostic("ALTER TABLE tm_bad DISABLE TRIGGER ALL, ADD CONSTRAINT tm_bad_fk FOREIGN KEY(pid) REFERENCES tm_parent(id)", fixture.ForeignKeyChild("tm_bad", "tm_bad_fk", "Key (pid)=(99) is not present in table \"tm_parent\".")))
		run(triggerModeCount("pg_constraint WHERE conname='tm_bad_fk'", 0))
		triggerDDLPartitionSeed(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"ALTER TABLE tm_data ENABLE TRIGGER a",
			"CREATE FUNCTION tm_abort() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN ALTER TABLE tm_data DISABLE TRIGGER a; ALTER TABLE tm_parent DISABLE TRIGGER ALL; ALTER TABLE tr_parent DISABLE TRIGGER a; x:=1/0; RETURN x; END$$")
		want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "tm_abort()", "assignment")
		want.Context = "PL/pgSQL assignment \"x:=1/0\"\n" + want.Context
		run(fixture.ErrorDiagnostic("SELECT tm_abort()", want))
		run(triggerModeCatalog("tm_data", []any{"a", "O"}))
		run(fixture.Exec("INSERT INTO tm_parent VALUES(10)", 1))
		run(fixture.Exec("INSERT INTO tm_child VALUES(10,10)", 1))
		run(fixture.Exec("DELETE FROM tm_parent WHERE id=10", 1))
		run(triggerModeCount("tm_child", 0))
		run(triggerModeCatalog("tr_parent", []any{"a", "O"}))
		run(triggerModeCatalog("tr_mid", []any{"a", "O"}))
		run(triggerModeCatalog("tr_leaf", []any{"a", "O"}))
		run(triggerRows("INSERT INTO tr_parent VALUES(1,5) RETURNING id,v", []any{int64(1), int64(8)}))
	})
}
