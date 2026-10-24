package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalRulesSavepoints(t *testing.T) {
	transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
		base := transactionalRulesRun(t, p, b, c, nil)
		transactionalRulesSetup(base)
		base(fixture.Exec(transactionalRulesCreate, 0))
		base(fixture.Exec("INSERT INTO src VALUES(9,-90)", 1))
		oid := transactionalRulesOID(t, p, c, "r")
		create := "CREATE RULE added AS ON INSERT TO src DO INSERT INTO sink2 VALUES(NEW.id,abs(NEW.v))"
		handles := transactionalRulesHandles(t, p, c, held, transactionalRulesReplace, transactionalRulesRename, transactionalRulesDrop, create, transactionalRulesInsert)
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		run := transactionalRulesRun(t, p, b, e, handles)
		run(fixture.Exec("SAVEPOINT rules", 0))
		for i := 1; i <= 2; i++ {
			run(fixture.Exec(transactionalRulesReplace, 0))
			run(fixture.Exec(transactionalRulesRename, 0))
			run(fixture.Exec(transactionalRulesInsert, 1, i, -10))
			run(fixture.Exec(transactionalRulesDrop, 0))
			run(fixture.Exec(create, 0))
			run(fixture.Exec(transactionalRulesInsert, 1, i+2, -20))
			run(transactionalRulesNames("added"))
			run(fixture.Exec("ROLLBACK TO rules", 0))
			run(transactionalRulesNames("r"))
			transactionalRulesSameOID(t, p, e, "r", oid)
			run(transactionalRulesRows("src", []any{int64(9), int64(-90)}))
			run(transactionalRulesRows("sink1", []any{int64(9), int64(90)}))
			run(transactionalRulesRows("sink2"))
			run(transactionalRulesEdge("sink1", 1))
			run(transactionalRulesEdge("sink2", 0))
		}
		run(fixture.Exec(transactionalRulesReplace, 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 5, 50))
		run(fixture.Exec("RELEASE rules", 0))
		run(transactionalRulesRows("sink2", []any{int64(5), int64(51)}))
		finish(false)
		base(transactionalRulesNames("r"))
		transactionalRulesSameOID(t, p, c, "r", oid)
		base(transactionalRulesRows("src", []any{int64(9), int64(-90)}))
		base(transactionalRulesRows("sink2"))
		base(fixture.Exec(transactionalRulesInsert, 1, 6, -60))
		base(transactionalRulesRows("sink1", []any{int64(6), int64(60)}, []any{int64(9), int64(90)}))
	})
}

func TestReferenceTransactionalRulesAtomicity(t *testing.T) {
	transactionalRulesRoutes(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
		base := transactionalRulesRun(t, p, b, c, nil)
		transactionalRulesSetup(base)
		base(fixture.Exec(transactionalRulesCreate, 0))
		base(fixture.Exec("INSERT INTO src VALUES(9,-90)", 1))
		base(fixture.Exec("CREATE TABLE bad(id INT,v INT NOT NULL)", 0))
		broken := "CREATE OR REPLACE RULE r AS ON INSERT TO src DO ALSO (INSERT INTO sink2 VALUES(NEW.id,abs(NEW.v)); INSERT INTO bad VALUES(NEW.id,NULL))"
		invalid := "CREATE OR REPLACE RULE r AS ON INSERT TO src DO INSERT INTO sink2 VALUES(NEW.id,NEW.absent)"
		handles := transactionalRulesHandles(t, p, c, held, broken, invalid, transactionalRulesInsert)
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		run := transactionalRulesRun(t, p, b, e, handles)
		run(fixture.Exec("SAVEPOINT good", 0))
		run(fixture.Exec(broken, 0))
		failure := fixture.ImplicitError(transactionalRulesInsert, fixture.Diagnostic{Code: "23502", Message: "null value in column \"v\" of relation \"bad\" violates not-null constraint", Detail: "Failing row contains (1, null).", Schema: "public", Table: "bad", Column: "v"}, 1, -10)
		failure.ErrorStage = "execute"
		run(failure)
		run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		run(fixture.Exec("ROLLBACK TO good", 0))
		run(transactionalRulesRows("src", []any{int64(9), int64(-90)}))
		run(transactionalRulesRows("sink1", []any{int64(9), int64(90)}))
		run(transactionalRulesRows("sink2"))
		run(transactionalRulesRows("bad"))
		run(fixture.Exec(transactionalRulesInsert, 1, 2, -20))
		run(transactionalRulesRows("sink1", []any{int64(2), int64(20)}, []any{int64(9), int64(90)}))
		run(fixture.Exec("SAVEPOINT bind_failure", 0))
		s := fixture.ImplicitError(invalid, fixture.Diagnostic{Code: "42703", Message: "column new.absent does not exist", Position: "81"})
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Exec("ROLLBACK TO bind_failure", 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 3, -30))
		run(transactionalRulesNames("r"))
		run(transactionalRulesRows("sink1", []any{int64(2), int64(20)}, []any{int64(3), int64(30)}, []any{int64(9), int64(90)}))
		run(transactionalRulesRows("sink2"))
		run(fixture.ImplicitError("SELECT 1/0", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
		finish(false)
		base(transactionalRulesRows("src", []any{int64(9), int64(-90)}))
		base(transactionalRulesRows("sink1", []any{int64(9), int64(90)}))
		base(transactionalRulesRows("sink2"))
		e, finish = transactionalViewsBegin(t, p, b, c, mode)
		run = transactionalRulesRun(t, p, b, e, handles)
		run(fixture.Exec("CREATE RULE private_prefix AS ON INSERT TO src DO INSERT INTO sink2 VALUES(NEW.id,abs(NEW.v))", 0))
		run(fixture.Exec(transactionalRulesInsert, 1, 8, -80))
		run(fixture.ImplicitError("SELECT 1/0", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
		finish(false)
		base(transactionalRulesNames("r"))
		base(transactionalRulesRows("src", []any{int64(9), int64(-90)}))
		base(transactionalRulesRows("sink2"))
		// Ordinary autocommit statement atomicity is a separate owner, not a hidden BEGIN workaround.
		base(fixture.Exec(broken, 0))
		failure.Query = "INSERT INTO src VALUES(1,-10)"
		failure.Args = nil
		base(failure)
		base(transactionalRulesRows("src", []any{int64(9), int64(-90)}))
		base(transactionalRulesRows("sink2"))
		base(transactionalRulesRows("bad"))
		base(fixture.Exec("CREATE OR REPLACE RULE r AS ON INSERT TO src DO ALSO INSERT INTO sink1 VALUES(NEW.id,abs(NEW.v))", 0))
		base(fixture.Exec("INSERT INTO src VALUES(4,-40)", 1))
		base(transactionalRulesRows("sink1", []any{int64(4), int64(40)}, []any{int64(9), int64(90)}))
	})
}
