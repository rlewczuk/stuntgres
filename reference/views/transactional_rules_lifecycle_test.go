package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalRulesLifecycle(t *testing.T) {
	for _, family := range []string{"create", "replace", "rename", "drop"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit=%v", family, commit), func(t *testing.T) {
				transactionalRulesRoutes(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
					base := transactionalRulesRun(t, p, b, c, nil)
					observe := transactionalRulesRun(t, p, b, peer, nil)
					transactionalRulesSetup(base)
					q := transactionalRulesCreate
					before := []string{}
					after := []string{"r"}
					var oid int64
					if family != "create" {
						base(fixture.Exec(transactionalRulesCreate, 0))
						oid = transactionalRulesOID(t, p, c, "r")
						before = []string{"r"}
					}
					switch family {
					case "replace":
						q = transactionalRulesReplace
					case "rename":
						q = transactionalRulesRename
						after = []string{"renamed"}
					case "drop":
						base(fixture.Exec(transactionalRulesRename, 0))
						before = []string{"renamed"}
						after = nil
						q = transactionalRulesDrop
					}
					handles := transactionalRulesHandles(t, p, c, held, q, transactionalRulesInsert)
					e, finish := transactionalViewsBegin(t, p, b, c, mode)
					run := transactionalRulesRun(t, p, b, e, handles)
					run(fixture.Exec(q, 0))
					run(transactionalRulesNames(after...))
					if family != "drop" {
						name := "r"
						if family == "rename" {
							name = "renamed"
						}
						if oid == 0 {
							oid = transactionalRulesOID(t, p, e, name)
						} else {
							transactionalRulesSameOID(t, p, e, name, oid)
						}
					}
					run(fixture.Exec(transactionalRulesInsert, 1, 1, -10))
					run(transactionalRulesRows("src", []any{int64(1), int64(-10)}))
					var sink1, sink2 [][]any
					if family == "replace" {
						sink2 = [][]any{{int64(1), int64(-9)}}
					} else if family != "drop" {
						sink1 = [][]any{{int64(1), int64(10)}}
					}
					run(transactionalRulesRows("sink1", sink1...))
					run(transactionalRulesRows("sink2", sink2...))
					if mode != "autocommit" {
						observe(transactionalRulesNames(before...))
						if family != "create" {
							observe(transactionalRulesEdge("sink1", 1))
							observe(transactionalRulesEdge("sink2", 0))
						} else {
							observe(transactionalRulesEdge("sink1", 0))
						}
					}
					finish(commit)
					published := mode == "autocommit" || commit
					if published {
						observe(transactionalRulesNames(after...))
						observe(transactionalRulesRows("src", []any{int64(1), int64(-10)}))
						observe(transactionalRulesRows("sink1", sink1...))
						observe(transactionalRulesRows("sink2", sink2...))
					} else {
						observe(transactionalRulesNames(before...))
						observe(transactionalRulesRows("src"))
						observe(transactionalRulesRows("sink1"))
						observe(transactionalRulesRows("sink2"))
						if family != "create" {
							transactionalRulesSameOID(t, p, peer, before[0], oid)
						}
					}
					// Exact event-local identity: the same name on another event survives.
					observe(fixture.Select("SELECT rulename FROM pg_rewrite WHERE ev_class='other'::regclass", []string{"rulename"}, []string{"NAME"}, [][]any{{"r"}}))
					observe(fixture.Exec("INSERT INTO src VALUES(2,20)", 1))
					fires := family != "drop"
					target := "sink1"
					value := int64(20)
					if published && family == "replace" {
						target = "sink2"
						value = 21
					}
					if !published {
						fires = family != "create"
					}
					var rows [][]any
					if published {
						if target == "sink1" {
							rows = append(rows, sink1...)
						} else {
							rows = append(rows, sink2...)
						}
					}
					if fires {
						rows = append(rows, []any{int64(2), value})
					}
					observe(transactionalRulesRows(target, rows...))
				})
			})
		}
	}
	t.Run("rename-order", func(t *testing.T) {
		transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
			base := transactionalRulesRun(t, p, b, c, nil)
			for _, q := range []string{"CREATE TABLE src(id INT,v INT)", "CREATE SEQUENCE firing", "CREATE TABLE ordered(n BIGINT,label TEXT)", "CREATE RULE a AS ON INSERT TO src DO INSERT INTO ordered VALUES(nextval('firing'),'a')", "CREATE RULE z AS ON INSERT TO src DO INSERT INTO ordered VALUES(nextval('firing'),'z')"} {
				base(fixture.Exec(q, 0))
			}
			oid := transactionalRulesOID(t, p, c, "z")
			q := "ALTER RULE z ON src RENAME TO \"0_z\""
			handles := transactionalRulesHandles(t, p, c, held, q)
			e, finish := transactionalViewsBegin(t, p, b, c, mode)
			run := transactionalRulesRun(t, p, b, e, handles)
			run(fixture.Exec(q, 0))
			transactionalRulesSameOID(t, p, e, "0_z", oid)
			run(fixture.Exec("INSERT INTO src VALUES(1,1)", 1))
			run(fixture.Select("SELECT label FROM ordered ORDER BY n", []string{"label"}, []string{"TEXT"}, [][]any{{"z"}, {"a"}}))
			finish(false)
			base(fixture.Exec("INSERT INTO src VALUES(2,2)", 1))
			base(fixture.Select("SELECT n,label FROM ordered ORDER BY n", []string{"n", "label"}, []string{"INT8", "TEXT"}, [][]any{{int64(3), "a"}, {int64(4), "z"}}))
			transactionalRulesSameOID(t, p, c, "z", oid)
		})
	})
}
