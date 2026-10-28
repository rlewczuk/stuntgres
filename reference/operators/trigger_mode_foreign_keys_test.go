package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerModeForeignKeys(t *testing.T) {
	t.Run("event-slots-and-actions", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			names := triggerModeFKSetup(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			alter := func(table, action string, slot int) {
				run(fixture.Exec("ALTER TABLE "+table+" "+action+" TRIGGER "+triggerModeQuote(names[slot]), 0))
			}
			run(triggerModeFKError("INSERT INTO tm_child VALUES(1,99)", 99))
			run(fixture.Exec("ALTER TABLE tm_child DISABLE TRIGGER USER", 0))
			run(fixture.Prepared(triggerModeFKError("INSERT INTO tm_child VALUES(1,99)", 99)))
			run(fixture.Exec("ALTER TABLE tm_child DISABLE TRIGGER ALL", 0))
			run(fixture.Exec("INSERT INTO tm_child VALUES(1,99)", 1))
			run(fixture.Exec("ALTER TABLE tm_child ENABLE TRIGGER ALL", 0))
			run(triggerModeFKError("INSERT INTO tm_child VALUES(2,98)", 98))
			// Enabling does not retroactively validate the saved orphan.
			run(fixture.Select("SELECT id,pid FROM tm_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(99)}}))
			alter("tm_child", "DISABLE", 2)
			run(fixture.Exec("INSERT INTO tm_child VALUES(2,98)", 1))
			run(triggerModeFKError("UPDATE tm_child SET pid=97 WHERE id=2", 97))
			// An own inserted row updated in the same transaction is UPDATE, not INSERT.
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("INSERT INTO tm_child VALUES(3,96)", 1))
			run(triggerModeFKError("UPDATE tm_child SET pid=95 WHERE id=3", 95))
			run(fixture.Exec("ROLLBACK", 0))
			alter("tm_child", "ENABLE", 2)
			run(fixture.Exec("INSERT INTO tm_parent VALUES(10),(20),(30)", 3))
			run(fixture.Exec("INSERT INTO tm_child VALUES(10,10),(20,20),(30,30)", 3))
			alter("tm_parent", "DISABLE", 0)
			run(fixture.Exec("DELETE FROM tm_parent WHERE id=10", 1))
			run(fixture.Select("SELECT pid FROM tm_child WHERE id=10", []string{"pid"}, []string{"INT4"}, [][]any{{int64(10)}}))
			alter("tm_parent", "ENABLE", 0)
			run(fixture.Exec("DELETE FROM tm_parent WHERE id=20", 1))
			run(triggerModeCount("tm_child WHERE id=20", 0))
			alter("tm_parent", "DISABLE", 1)
			run(fixture.Exec("UPDATE tm_parent SET id=31 WHERE id=30", 1))
			run(fixture.Select("SELECT pid FROM tm_child WHERE id=30", []string{"pid"}, []string{"INT4"}, [][]any{{int64(30)}}))
			run(fixture.Exec("INSERT INTO tm_parent VALUES(40)", 1))
			run(fixture.Exec("INSERT INTO tm_child VALUES(40,40)", 1))
			alter("tm_parent", "ENABLE", 1)
			run(fixture.Exec("UPDATE tm_parent SET id=41 WHERE id=40", 1))
			run(fixture.Select("SELECT pid FROM tm_child WHERE id=40", []string{"pid"}, []string{"INT4"}, [][]any{{int64(41)}}))
		})
	})
	for _, mode := range triggerModeCases {
		t.Run("roles-"+mode.code, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				names := triggerModeFKSetup(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for slot, name := range names {
					table := "tm_parent"
					if slot >= 2 {
						table = "tm_child"
					}
					run(fixture.Exec("ALTER TABLE "+table+" "+mode.action+" "+triggerModeQuote(name), 0))
				}
				for n, role := range []string{"origin", "local", "replica"} {
					id := int64(n + 1)
					run(fixture.Exec("SET session_replication_role="+role, 0))
					invalid := "INSERT INTO tm_child VALUES(" + triggerModeNumber(id) + ",99)"
					if triggerModeActive(mode.code, role) {
						run(triggerModeFKError(invalid, 99))
					} else {
						run(fixture.Exec(invalid, 1))
					}
					key := triggerModeNumber(id + 10)
					run(fixture.Exec("INSERT INTO tm_parent VALUES("+key+")", 1))
					run(fixture.Exec("INSERT INTO tm_child VALUES("+key+","+key+")", 1))
					run(fixture.Exec("DELETE FROM tm_parent WHERE id="+key, 1))
					left := int64(1)
					if triggerModeActive(mode.code, role) {
						left = 0
					}
					run(triggerModeCount("tm_child WHERE id="+key, left))
				}
				run(fixture.Exec("SET session_replication_role=origin", 0))
			})
		})
	}
	t.Run("no-action-restrict-and-set-actions", func(t *testing.T) {
		for _, action := range []string{"NO ACTION", "RESTRICT", "SET NULL", "SET DEFAULT"} {
			t.Run(action, func(t *testing.T) {
				triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c,
						"CREATE TABLE tm_parent(id integer PRIMARY KEY)",
						"CREATE TABLE tm_child(id integer PRIMARY KEY,pid integer DEFAULT NULL,CONSTRAINT tm_fk FOREIGN KEY(pid) REFERENCES tm_parent(id) ON DELETE "+action+")")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tm_parent VALUES(1),(2)", 2))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tm_child VALUES(1,1),(2,2)", 2))
					names := triggerModeFKNames(t, p, c, "tm_fk")
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("ALTER TABLE tm_parent DISABLE TRIGGER "+triggerModeQuote(names[0]), 0))
					run(fixture.Exec("DELETE FROM tm_parent WHERE id=1", 1))
					run(fixture.Select("SELECT pid FROM tm_child WHERE id=1", []string{"pid"}, []string{"INT4"}, [][]any{{int64(1)}}))
					run(fixture.Exec("ALTER TABLE tm_parent ENABLE TRIGGER "+triggerModeQuote(names[0]), 0))
					if action == "NO ACTION" || action == "RESTRICT" {
						want := fixture.ForeignKeyParent("tm_parent", "tm_child", "tm_fk", "Key (id)=(2) is still referenced from table \"tm_child\".")
						if action == "RESTRICT" {
							want = fixture.ForeignKeyError("23001",
								`update or delete on table "tm_parent" violates RESTRICT setting of foreign key constraint "tm_fk" on table "tm_child"`,
								`Key (id)=(2) is referenced from table "tm_child".`, "tm_child", "tm_fk")
						}
						run(fixture.ErrorDiagnostic("DELETE FROM tm_parent WHERE id=2", want))
					} else {
						run(fixture.Exec("DELETE FROM tm_parent WHERE id=2", 1))
						run(fixture.Select("SELECT pid FROM tm_child WHERE id=2", []string{"pid"}, []string{"INT4"}, [][]any{{nil}}))
					}
				})
			})
		}
	})
	t.Run("self-fk-four-identities", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE tm_self(id integer PRIMARY KEY,pid integer REFERENCES tm_self(id))", "ALTER TABLE tm_self DISABLE TRIGGER ALL")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Select("SELECT count(*) AS n,count(DISTINCT oid) AS identities FROM pg_trigger WHERE tgrelid='tm_self'::regclass AND tgisinternal AND tgenabled='D'", []string{"n", "identities"}, []string{"INT8", "INT8"}, [][]any{{int64(4), int64(4)}}))
			run(fixture.Exec("INSERT INTO tm_self VALUES(1,99)", 1))
			run(fixture.Exec("ALTER TABLE tm_self ENABLE TRIGGER ALL", 0))
			run(fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgrelid='tm_self'::regclass AND tgenabled='O'", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
		})
	})
}
