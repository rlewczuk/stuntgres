package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRuleModeRestrictions(t *testing.T) {
	for _, event := range []string{"INSERT", "UPDATE"} {
		for _, nothing := range []bool{false, true} {
			for _, mode := range ruleModeCases {
				for _, role := range []string{"origin", "replica"} {
					t.Run(event+"/"+map[bool]string{false: "action", true: "nothing"}[nothing]+"/"+mode.code+"/"+role, func(t *testing.T) {
						ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
							c := ruleModeConn(t, p, b)
							run := transactionalRulesRun(t, p, b, c, nil)
							run(fixture.Exec("CREATE TABLE src(id INT PRIMARY KEY,v INT)", 0))
							run(fixture.Exec("CREATE TABLE sink1(id INT,v INT)", 0))
							action := "ALSO INSERT INTO sink1 VALUES(NEW.id,NEW.v)"
							if nothing {
								action = "INSTEAD NOTHING"
							}
							run(fixture.Exec("CREATE RULE r AS ON "+event+" TO src DO "+action, 0))
							run(fixture.Exec("ALTER TABLE src "+mode.action+" r", 0))
							run(fixture.Exec("SET session_replication_role="+role, 0))
							for _, conflict := range []string{"ON CONFLICT DO NOTHING", "ON CONFLICT(id) DO UPDATE SET v=EXCLUDED.v"} {
								q := "INSERT INTO src VALUES(1,10) " + conflict
								active := ruleModeActive(mode.code, role)
								// PostgreSQL records UPDATE-rule presence before filtering modes.
								rejected := event == "UPDATE" || active && !nothing
								if rejected {
									s := ruleError(q, "0A000", "INSERT with ON CONFLICT clause cannot be used with table that has INSERT or UPDATE rules")
									s.Prepared = held
									s.ErrorStage = "execute"
									if held {
										s.ErrorStage = "prepare"
									}
									fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
								} else {
									n := int64(1)
									if active && nothing {
										n = 0
									}
									transactionalViewsExec(t, p, b, c, held, fixture.Exec(q, n))
									run(fixture.Exec("DELETE FROM src", n))
								}
								run(ruleModeCount("src", 0))
								run(ruleModeCount("sink1", 0))
								run(fixture.Select("SELECT ev_enabled::TEXT FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'", []string{"ev_enabled"}, []string{"TEXT"}, [][]any{{mode.code}}))
							}
						})
					})
				}
			}
		}
	}
	for _, mode := range ruleModeCases {
		for _, role := range []string{"origin", "local", "replica"} {
			t.Run("merge/"+mode.code+"/"+role, func(t *testing.T) {
				ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
					c := ruleModeConn(t, p, b)
					run := transactionalRulesRun(t, p, b, c, nil)
					ruleModeSetup(run)
					run(fixture.Exec("INSERT INTO src VALUES(1,10)", 1))
					run(fixture.Exec("TRUNCATE sink1", 0))
					run(fixture.Exec("ALTER TABLE src "+mode.action+" r", 0))
					run(fixture.Exec("SET session_replication_role="+role, 0))
					q := "MERGE INTO src t USING (VALUES(1,11),(2,20)) s(id,v) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)"
					if ruleModeActive(mode.code, role) {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "cannot execute MERGE on relation \"src\"", Detail: "MERGE is not supported for relations with rules."})
						s.Prepared = held
						s.ErrorStage = "execute"
						if held {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						run(ruleModeValueRows("src", []any{int64(1), int64(10)}))
					} else {
						n := int64(2)
						if b.PG {
							n = 0
						}
						transactionalViewsExec(t, p, b, c, held, fixture.Exec(q, n))
						run(ruleModeValueRows("src", []any{int64(1), int64(11)}, []any{int64(2), int64(20)}))
					}
					run(ruleModeCount("sink1", 0))
					run(ruleModeCatalog(mode.code))
				})
			})
		}
	}
}
