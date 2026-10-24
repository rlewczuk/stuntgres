package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalRulesPrepared(t *testing.T) {
	transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
		base := transactionalRulesRun(t, p, b, c, nil)
		transactionalRulesSetup(base)
		reader := "SELECT id,v FROM sink1 ORDER BY id"
		handles := transactionalRulesHandles(t, p, c, true, transactionalRulesInsert, reader)
		for q, h := range transactionalRulesHandles(t, p, c, held, transactionalRulesCreate, transactionalRulesReplace, transactionalRulesRename, transactionalRulesDrop) {
			handles[q] = h
		}
		base(fixture.Exec("PREPARE rule_insert(INT,INT) AS INSERT INTO src VALUES($1,$2)", 0))
		defer c.ExecContext(p.Ctx, "DEALLOCATE rule_insert")
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		run := transactionalRulesRun(t, p, b, e, handles)
		run(fixture.Exec(transactionalRulesInsert, 1, 1, -10))
		run(fixture.Exec(transactionalRulesCreate, 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 2, -20))
		run(fixture.Select(reader, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}}))
		run(fixture.Exec("SAVEPOINT fresh", 0))
		run(fixture.Exec(transactionalRulesReplace, 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 3, 30))
		run(fixture.Exec("EXECUTE rule_insert(4,40)", 1))
		run(transactionalRulesRows("sink2", []any{int64(3), int64(31)}, []any{int64(4), int64(41)}))
		run(fixture.Exec(transactionalRulesRename, 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 5, 50))
		run(fixture.Exec(transactionalRulesDrop, 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 6, 60))
		run(transactionalRulesRows("sink2", []any{int64(3), int64(31)}, []any{int64(4), int64(41)}, []any{int64(5), int64(51)}))
		run(fixture.Exec("ROLLBACK TO fresh", 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 7, -70))
		run(fixture.Exec("EXECUTE rule_insert(8,-80)", 1))
		run(fixture.Select(reader, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(7), int64(70)}, {int64(8), int64(80)}}))
		finish(true)
		// Peer publication invalidates the very same handles, not newly prepared substitutes.
		observe := transactionalRulesRun(t, p, b, peer, nil)
		observe(fixture.Exec(transactionalRulesReplace, 0))
		run = transactionalRulesRun(t, p, b, c, handles)
		run(fixture.Exec(transactionalRulesInsert, 1, 9, 90))
		run(transactionalRulesRows("sink2", []any{int64(9), int64(91)}))
		observe(fixture.Exec("DROP RULE r ON src", 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 10, 100))
		run(transactionalRulesRows("sink2", []any{int64(9), int64(91)}))
		e, finish = transactionalViewsBegin(t, p, b, c, mode)
		run = transactionalRulesRun(t, p, b, e, handles)
		run(fixture.Exec(transactionalRulesCreate, 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 11, -110))
		finish(false)
		run = transactionalRulesRun(t, p, b, c, handles)
		run(fixture.Exec(transactionalRulesInsert, 1, 12, -120))
		run(fixture.Select(reader, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(7), int64(70)}, {int64(8), int64(80)}}))
	})
	t.Run("signature-before-write", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
				run := transactionalRulesRun(t, p, b, c, nil)
				run(fixture.Exec("CREATE TABLE shape(id INT)", 0))
				run(fixture.Exec("INSERT INTO shape VALUES(1)", 1))
				run(fixture.Exec("CREATE VIEW v AS SELECT id FROM shape", 0))
				reader := "SELECT * FROM v"
				h := transactionalRulesHandles(t, p, c, true, reader)
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT id,true AS flag FROM shape", 0))
				run(fixture.Exec("CREATE RULE action AS ON INSERT TO shape DO ALSO SELECT id FROM v", 0))
				run = transactionalRulesRun(t, p, b, c, h)
				s := fixture.ImplicitError(reader, fixture.Diagnostic{Code: "0A000", Message: "cached plan must not change result type"})
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Select(reader, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		}
	})
}
