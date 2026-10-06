//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceDeleteComposition(t *testing.T) {
	p := newReferencePair(t)
	schema := fmt.Sprintf("sequence_delete_%d",time.Now().UnixNano())
	for _, backend := range []struct { name string; db *sql.DB; record *referenceRecorder }{{"native",p.memory,p.memoryRecord},{"postgres",p.postgres,p.postgresRecord}} {
		t.Run(backend.name,func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			defer func() {
				_, _ = c.ExecContext(p.ctx,"ROLLBACK")
				if _, err := c.ExecContext(p.ctx,"DROP SCHEMA "+schema+" CASCADE"); err != nil { t.Error(err) }
			}()
			step := func(s referenceStep) { t.Helper(); referenceRunStep(t,p.ctx,c,backend.record,s) }
			step(referenceExec("CREATE SCHEMA "+schema,0))
			step(referenceExec("SET search_path TO "+schema,0))
			step(referenceExec("CREATE SEQUENCE s",0))
			step(referenceExec("CREATE TABLE t(v INT)",0))
			step(referenceExec("INSERT INTO t VALUES(1),(1),(2)",3))
			step(referenceSelect("SELECT nextval('s')",[]string{"nextval"},[]string{"INT8"},[][]any{{int64(1)}}))
			for _, s := range []referenceStep{
				referenceExec("BEGIN",0),referenceExec("ALTER SEQUENCE s RESTART 100",0),
				referencePrepared(referenceExec("DELETE FROM t WHERE v=1",2)),referenceExec("INSERT INTO t VALUES(3)",1),
				referenceSelect("SELECT v FROM t ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(2)},{int64(3)}}),referenceExec("ROLLBACK",0),
				referenceSelect("SELECT v FROM t ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(1)},{int64(1)},{int64(2)}}),
				referenceSelect("SELECT nextval('s')",[]string{"nextval"},[]string{"INT8"},[][]any{{int64(2)}}),
				referenceExec("BEGIN",0),referenceExec("ALTER SEQUENCE s RESTART 200",0),referenceExec("DELETE FROM t WHERE v=1",2),
				referenceExec("INSERT INTO t VALUES(4)",1),referenceExec("INSERT INTO t VALUES(5)",1),referenceExec("DELETE FROM t WHERE v=5",1),referenceExec("COMMIT",0),
				referenceSelect("SELECT v FROM t ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(2)},{int64(4)}}),
				referenceSelect("SELECT nextval('s')",[]string{"nextval"},[]string{"INT8"},[][]any{{int64(200)}}),
				referenceExec("BEGIN",0),referencePrepared(referenceExec("DELETE FROM t WHERE nextval('s')>0",2)),referenceExec("ROLLBACK",0),
				referenceSelect("SELECT v FROM t ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(2)},{int64(4)}}),
				referenceSelect("SELECT currval('s')",[]string{"currval"},[]string{"INT8"},[][]any{{int64(202)}}),
			} { step(s) }
			wrong := controlsReferenceDiagnostic("42809",`cannot change sequence "s"`)
			failure := referencePrepared(referenceErrorDiagnostic("DELETE FROM s WHERE nextval('s')>0",wrong))
			failure.errorStage="execute"
			step(failure)
			step(referenceSelect("SELECT currval('s')",[]string{"currval"},[]string{"INT8"},[][]any{{int64(202)}}))
			step(referenceExec("CREATE TABLE dst(v INT)",0))
			step(referencePrepared(referenceExec("INSERT INTO dst SELECT v FROM t ORDER BY v",2)))
			step(referenceSelect("SELECT v FROM dst ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(2)},{int64(4)}}))
		})
	}
}
