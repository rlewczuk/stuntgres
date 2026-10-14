package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRuleReview5ViewProviderRetirement(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(steps ...fixture.Step) { ruleRun(t, p, b, false, steps...) }
					run(fixture.Exec("CREATE DOMAIN rule_domain AS INT CHECK(VALUE>=0)", 0),
						fixture.Exec("CREATE TABLE base(id INT)", 0), fixture.Exec("INSERT INTO base VALUES(10)", 1),
						fixture.Exec("CREATE VIEW event_v AS SELECT id FROM base", 0), fixture.Exec("CREATE TABLE sink(id INT)", 0),
						fixture.Exec("CREATE TABLE unrelated(id INT)", 0), fixture.Exec("INSERT INTO unrelated VALUES(99)", 1),
						fixture.Exec("CREATE RULE r AS ON INSERT TO event_v DO INSTEAD INSERT INTO sink VALUES(NEW.id::rule_domain)", 0),
						fixture.Exec("CREATE RULE keep_r AS ON DELETE TO event_v DO ALSO NOTHING", 0))
					var notices []fixture.Diagnostic
					_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					const drop = "DROP DOMAIN rule_domain RESTRICT"
					const insert = "INSERT INTO event_v VALUES($1)"
					var dropExecutor, insertExecutor fixture.Executor = conn, conn
					if held {
						dropStmt, err := conn.PrepareContext(p.Ctx, drop)
						if err != nil {
							t.Fatal(err)
						}
						defer dropStmt.Close()
						insertStmt, err := conn.PrepareContext(p.Ctx, insert)
						if err != nil {
							t.Fatal(err)
						}
						defer insertStmt.Close()
						dropExecutor = fixture.HeldStatement{Executor: conn, Statement: dropStmt, SQL: drop}
						insertExecutor = fixture.HeldStatement{Executor: conn, Statement: insertStmt, SQL: insert}
					}
					deny := func() {
						s := fixture.ImplicitError(drop, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop type rule_domain because other objects depend on it", Detail: "rule r on view event_v depends on type rule_domain", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
						s.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, dropExecutor, recorder, s)
					}
					fire := func(id int) { fixture.RunStep(t, p.Ctx, insertExecutor, recorder, fixture.Exec(insert, 1, id)) }
					provider := func(present bool) {
						const q = "SELECT 1::rule_domain = 1 AS present"
						if present {
							run(fixture.Select(q, []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
						} else {
							s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42704", Message: "type \"rule_domain\" does not exist", Position: "11"})
							s.SelectRows = true
							run(s)
						}
					}
					state := func(retired bool, sink [][]any, base [][]any) {
						var rules [][]any
						if retired {
							rules = [][]any{{"_RETURN"}, {"keep_r"}}
						} else {
							rules = [][]any{{"_RETURN"}, {"keep_r"}, {"r"}}
						}
						run(fixture.Select("SELECT rulename FROM pg_rewrite WHERE ev_class='event_v'::regclass::oid ORDER BY rulename", []string{"rulename"}, []string{"NAME"}, rules),
							fixture.Select("SELECT id FROM sink ORDER BY id", []string{"id"}, []string{"INT4"}, sink),
							fixture.Select("SELECT id FROM base ORDER BY id", []string{"id"}, []string{"INT4"}, base),
							fixture.Select("SELECT id FROM event_v ORDER BY id", []string{"id"}, []string{"INT4"}, base),
							fixture.Select("SELECT id FROM unrelated", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
					}
					deny()
					provider(true)
					state(false, nil, [][]any{{int64(10)}})
					fire(1)
					state(false, [][]any{{int64(1)}}, [][]any{{int64(10)}})
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
					}
					// SAME held DROP and INSERT observe the retained objects after failed RESTRICT and native restoration.
					deny()
					provider(true)
					fire(2)
					state(false, [][]any{{int64(1)}, {int64(2)}}, [][]any{{int64(10)}})
					fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP DOMAIN rule_domain CASCADE", 0))
					fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to rule r on view event_v"}})
					provider(false)
					state(true, [][]any{{int64(1)}, {int64(2)}}, [][]any{{int64(10)}})
					// Removing only the DML rule exposes ordinary writable-view lowering, not a missing event view.
					fire(3)
					state(true, [][]any{{int64(1)}, {int64(2)}}, [][]any{{int64(3)}, {int64(10)}})
				})
			}
		})
	}
}
