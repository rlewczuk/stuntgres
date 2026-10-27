package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRuleModeRouting(t *testing.T) {
	for _, mode := range ruleModeCases {
		for _, role := range []string{"origin", "local", "replica"} {
			t.Run(mode.code+"/"+role, func(t *testing.T) {
				for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
					t.Run(event, func(t *testing.T) {
						ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
							c := ruleModeConn(t, p, b)
							run := transactionalRulesRun(t, p, b, c, nil)
							run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0))
							run(fixture.Exec("CREATE TABLE sink1(id INT,v INT)", 0))
							if event != "INSERT" {
								run(fixture.Exec("INSERT INTO src VALUES(1,10)", 1))
							}
							ref := "NEW"
							q := "INSERT INTO src VALUES(1,10)"
							if event == "UPDATE" {
								q = "UPDATE src SET v=11 WHERE id=1"
							}
							if event == "DELETE" {
								ref = "OLD"
								q = "DELETE FROM src WHERE id=1"
							}
							run(fixture.Exec("CREATE RULE r AS ON "+event+" TO src DO INSTEAD INSERT INTO sink1 VALUES("+ref+".id,"+ref+".v)", 0))
							run(fixture.Exec("ALTER TABLE src "+mode.action+" r", 0))
							run(fixture.Exec("SET session_replication_role="+role, 0))
							run = transactionalRulesRun(t, p, b, c, transactionalRulesHandles(t, p, c, held, q))
							affected := int64(1)
							if event != "INSERT" && ruleModeActive(mode.code, role) {
								affected = 0
							}
							run(fixture.Exec(q, affected))
							var src, sink [][]any
							v := int64(10)
							if event == "UPDATE" {
								v = 11
							}
							if ruleModeActive(mode.code, role) {
								sink = [][]any{{int64(1), v}}
								if event != "INSERT" {
									src = [][]any{{int64(1), int64(10)}}
								}
							} else if event != "DELETE" {
								src = [][]any{{int64(1), v}}
							}
							run(ruleModeValueRows("src", src...))
							run(ruleModeValueRows("sink1", sink...))
						})
					})
				}
			})
		}
	}
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "inactive", true: "active"}[active], func(t *testing.T) {
			t.Run("conditional", func(t *testing.T) {
				ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
					c := ruleModeConn(t, p, b)
					run := transactionalRulesRun(t, p, b, c, nil)
					run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0))
					run(fixture.Exec("CREATE TABLE sink1(id INT,v INT)", 0))
					run(fixture.Exec("CREATE RULE r AS ON INSERT TO src WHERE NEW.v>0 DO INSTEAD INSERT INTO sink1 VALUES(NEW.id,NEW.v)", 0))
					if !active {
						run(fixture.Exec("ALTER TABLE src DISABLE RULE r", 0))
					}
					q := "INSERT INTO src VALUES(1,10),(2,-10),(3,NULL)"
					run = transactionalRulesRun(t, p, b, c, transactionalRulesHandles(t, p, c, held, q))
					n := int64(3)
					if active {
						n = 2
					}
					run(fixture.Exec(q, n))
					src := [][]any{{int64(2), int64(-10)}, {int64(3), nil}}
					var sink [][]any
					if active {
						sink = [][]any{{int64(1), int64(10)}}
					} else {
						src = append([][]any{{int64(1), int64(10)}}, src...)
					}
					run(ruleModeValueRows("src", src...))
					run(ruleModeValueRows("sink1", sink...))
				})
			})
			t.Run("returning", func(t *testing.T) {
				ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
					c := ruleModeConn(t, p, b)
					run := transactionalRulesRun(t, p, b, c, nil)
					run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0))
					run(fixture.Exec("CREATE TABLE sink1(id INT,v INT)", 0))
					run(fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO sink1 VALUES(NEW.id,NEW.v+1) RETURNING id,v", 0))
					if !active {
						run(fixture.Exec("ALTER TABLE src DISABLE RULE r", 0))
					}
					queries := []string{"INSERT INTO src VALUES($1,$2) RETURNING v AS out", "INSERT INTO src SELECT 2,20 WHERE false RETURNING v AS out"}
					run = transactionalRulesRun(t, p, b, c, transactionalRulesHandles(t, p, c, held, queries...))
					v := int64(10)
					if active {
						v++
					}
					run(fixture.Select(queries[0], []string{"out"}, []string{"INT4"}, [][]any{{v}}, int64(1), int64(10)))
					run(fixture.Select(queries[1], []string{"out"}, []string{"INT4"}, nil))
				})
			})
			t.Run("cte-view-recursion", func(t *testing.T) {
				ruleModeRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
					c := ruleModeConn(t, p, b)
					run := transactionalRulesRun(t, p, b, c, nil)
					run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0))
					run(fixture.Exec("CREATE TABLE sink1(id INT,v INT)", 0))
					run(fixture.Exec("CREATE VIEW lower_view AS SELECT id,v FROM src", 0))
					run(fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO sink1 VALUES(NEW.id,NEW.v) RETURNING id,v", 0))
					if !active {
						run(fixture.Exec("ALTER TABLE src DISABLE RULE r", 0))
					}
					q := "WITH changed AS (INSERT INTO src VALUES(1,10) RETURNING id,v) SELECT id,v FROM changed"
					ruleRun(t, p, b, held, ruleModeValueRowsQuery(q, []any{int64(1), int64(10)}))
					ruleRun(t, p, b, held, fixture.Exec("INSERT INTO lower_view VALUES(2,20)", 1))
					target, empty := "src", "sink1"
					if active {
						target, empty = empty, target
					}
					run(ruleModeValueRows(target, []any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
					run(ruleModeValueRows(empty))
					run(fixture.Exec("DROP RULE r ON src", 0))
					run(fixture.Exec("CREATE RULE recursive AS ON INSERT TO src DO ALSO INSERT INTO src VALUES(NEW.id+1,NEW.v)", 0))
					if !active {
						run(fixture.Exec("ALTER TABLE src DISABLE RULE recursive", 0))
					}
					if active {
						run(ruleError("INSERT INTO src VALUES(3,30)", "42P17", "infinite recursion detected in rules for relation \"src\""))
					} else {
						run(fixture.Exec("INSERT INTO src VALUES(3,30)", 1))
					}
					run(ruleModeCount("src", map[bool]int64{false: 3, true: 0}[active]))
				})
			})
		})
	}
}

func ruleModeValueRowsQuery(q string, rows ...[]any) fixture.Step {
	return fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, rows)
}
