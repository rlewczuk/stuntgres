package core_test

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
)

func mergeSourceRepeatableReadFollowup(t *testing.T, e *core.Engine, s *session.Session) {
	t.Helper()
	q := "MERGE INTO t USING (SELECT 9 AS id WHERE FALSE) src ON t.id=src.id WHEN NOT MATCHED BY SOURCE AND t.id=1"
	enginetest.SessionSQL(t, s, "SAVEPOINT followup")
	op := enginetest.TransactionOperation(t, s)
	result, err := e.Execute(op, enginetest.Statement(t, q+" THEN UPDATE SET v=t.v+1"), 0, nil)
	op.Done()
	if err != nil || result.Count != 1 {
		t.Fatal(result, err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO followup")
	r := enginetest.SessionSQL(t, s, q+" AND t.v=10 THEN UPDATE SET v=t.v+1")
	if r.Count != 0 {
		t.Fatal(r.Count)
	}
	r = enginetest.SessionSQL(t, s, q+" AND t.v=100 THEN UPDATE SET v=t.v+1")
	if r.Count != 1 {
		t.Fatal(r.Count)
	}
	enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY id,v", 101, 21, 40)
	enginetest.SessionSQL(t, s, "ROLLBACK TO followup")
	enginetest.SessionSQL(t, s, "RELEASE followup")
}
