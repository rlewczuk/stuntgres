package engine

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestMergeSourceUpdatePending(t *testing.T) {
	t.Run("repeatableRead", func(t *testing.T) { mergeUpdateRepeatableReadPending(t, true) })
	for _, lane := range []string{"retainsClass", "noWiden", "twoActions", "fixedSource", "insertFault", "delete", "deleteNoInsert", "deleteDefault", "deleteNotNull", "deleteUnique"} {
		for _, commit := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "Rollback", true: "Commit"}[commit], func(t *testing.T) {
				e := New()
				holder := sessionStart(t, e, nil)
				worker := sessionStart(t, e, nil)
				peer := sessionStart(t, e, nil)
				ddl := "CREATE TABLE t(id INT,v INT)"
				if lane == "deleteNotNull" {
					ddl = "CREATE TABLE t(id INT NOT NULL,v INT)"
				}
				if lane == "deleteUnique" {
					ddl = "CREATE TABLE t(id INT CONSTRAINT t_key UNIQUE,v INT)"
				}
				if lane == "deleteDefault" {
					ddl = "CREATE TABLE t(id INT DEFAULT 8,v INT DEFAULT 80)"
				}
				sessionSQL(t, peer, ddl)
				matched := lane == "twoActions" || lane == "fixedSource" || lane == "insertFault"
				seed := "INSERT INTO t VALUES(2,10)"
				hold := "UPDATE t SET id=1,v=20"
				if matched {
					seed = "INSERT INTO t VALUES(1,10)"
					hold = "UPDATE t SET id=2"
				}
				if len(lane) >= 6 && lane[:6] == "delete" {
					hold = "DELETE FROM t"
				}
				sessionSQL(t, peer, seed)
				sessionSQL(t, peer, "CREATE TABLE source(id INT,x INT)")
				sessionSQL(t, peer, "INSERT INTO source VALUES(1,7)")
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				row := e.storage[ref.Address].versions[0].row
				tx := transactionBegin(t, holder)
				sessionSQL(t, holder, hold)
				q := "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=999 WHEN NOT MATCHED BY SOURCE AND t.v>=20 THEN UPDATE SET v=100 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=200"
				if matched {
					q = "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=1 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.x)"
				}
				if lane == "insertFault" {
					q = "MERGE INTO t USING source s ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=1 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100 WHEN NOT MATCHED THEN INSERT VALUES(s.id,1/(s.x-7))"
				}
				if len(lane) >= 6 && lane[:6] == "delete" {
					q = "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+100"
					if lane == "deleteDefault" {
						q += " WHEN NOT MATCHED THEN INSERT DEFAULT VALUES"
					} else if lane == "deleteUnique" {
						q += " WHEN NOT MATCHED THEN INSERT VALUES(COALESCE(s.id,1),s.x)"
					} else if lane != "deleteNoInsert" {
						q += " WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.x)"
					}
				}
				done := mergeUpdatePendingExecute(t, worker, q)
				mergeUpdatePendingRow(t, e, ref, row, worker, holder.transactions.ticket, 0)
				transactionRows(t, peer, "SELECT v FROM t", 10)
				if lane == "fixedSource" {
					sessionSQL(t, peer, "UPDATE source SET x=8")
				}
				if lane == "noWiden" {
					sessionSQL(t, peer, "INSERT INTO t VALUES(3,30)")
				}
				transactionEnd(t, holder, tx, commit)
				count := int64(1)
				var diagnostic *sqlstate.Error
				if matched && commit {
					count = 2
				}
				if len(lane) >= 6 && lane[:6] == "delete" && lane != "deleteNoInsert" {
					count = 2
				}
				if lane == "deleteNoInsert" && commit {
					count = 0
				}
				if lane == "insertFault" && commit {
					count = 0
					diagnostic = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
				}
				if lane == "deleteNotNull" && commit {
					count = 0
					diagnostic = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "id" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, null).", Schema: "public", Table: "t", Column: "id"}
				}
				if lane == "deleteUnique" && commit {
					count = 0
					diagnostic = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "t_key"`, Detail: "Key (id)=(1) already exists.", Schema: "public", Table: "t", Constraint: "t_key"}
				}
				mergeUpdatePendingOutcome(t, done, count, diagnostic)
				switch {
				case diagnostic != nil:
					if lane == "insertFault" {
						transactionRows(t, peer, "SELECT v FROM t", 10)
					} else {
						transactionRows(t, peer, "SELECT v FROM t")
					}
				case matched && commit:
					transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 7, 110)
				case matched:
					transactionRows(t, peer, "SELECT v FROM t", 1)
				case lane == "deleteDefault":
					if commit {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 80, 80)
					} else {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 110, 80)
					}
				case len(lane) >= 6 && lane[:6] == "delete":
					if commit {
						if lane == "deleteNoInsert" {
							transactionRows(t, peer, "SELECT v FROM t")
						} else {
							transactionRows(t, peer, "SELECT COALESCE(v,-1) FROM t ORDER BY id", 7, -1)
							transactionRows(t, peer, "SELECT count(*) FROM t WHERE id IS NULL AND v IS NULL", 1)
						}
					} else {
						if lane == "deleteNoInsert" {
							transactionRows(t, peer, "SELECT v FROM t", 110)
						} else {
							transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 7, 110)
						}
					}
				default:
					v := int64(200)
					if commit {
						v = 100
					}
					if lane == "noWiden" {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", v, 30)
					} else {
						transactionRows(t, peer, "SELECT v FROM t", v)
					}
				}
				lockEmpty(t, e)
				sessionSQL(t, peer, "UPDATE t SET v=v+1")
			})
		}
	}
}

func mergeSourceRepeatableReadFollowup(t *testing.T, e *Engine, s *Session, oldBag bool) {
	t.Helper()
	q := "MERGE INTO t USING (SELECT 9 AS id WHERE FALSE) src ON t.id=src.id WHEN NOT MATCHED BY SOURCE AND t.id=1"
	sessionSQL(t, s, "SAVEPOINT followup")
	op := transactionOperation(t, s)
	result, err := e.Execute(op, statement(t, q+" THEN UPDATE SET v=t.v+1"), 0, nil)
	op.Done()
	if oldBag {
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "21000" || diagnostic.Message != "MERGE command cannot affect row a second time" || diagnostic.Hint != "Ensure that not more than one source row matches any one target row." || result.Count != 0 {
			t.Fatal(result, err)
		}
	} else if err != nil || result.Count != 1 {
		t.Fatal(result, err)
	}
	sessionSQL(t, s, "ROLLBACK TO followup")
	r := sessionSQL(t, s, q+" AND t.v=10 THEN UPDATE SET v=t.v+1")
	if r.Count != 0 {
		t.Fatal(r.Count)
	}
	r = sessionSQL(t, s, q+" AND t.v=100 THEN UPDATE SET v=t.v+1")
	if r.Count != 1 {
		t.Fatal(r.Count)
	}
	if oldBag {
		transactionRows(t, s, "SELECT v FROM t ORDER BY id,v", 10, 101, 21, 40)
	} else {
		transactionRows(t, s, "SELECT v FROM t ORDER BY id,v", 101, 21, 40)
	}
	sessionSQL(t, s, "ROLLBACK TO followup")
	sessionSQL(t, s, "RELEASE followup")
}
