package reference_test

import (
	"testing"
	"time"
)

func TestReferenceAlterAddColumnDefaults(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
		run(referenceExec("ALTER TABLE t ADD n INT DEFAULT (1+2*3) NOT NULL", 0))
		run(referenceExec("INSERT INTO t(id) VALUES(3)", 1))
		run(referencePrepared(referenceExec("INSERT INTO t VALUES(4,DEFAULT),(5,99)", 2)))
		run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}, {int64(3), int64(7)}, {int64(4), int64(7)}, {int64(5), int64(99)}}))
		run(referenceExec("ALTER TABLE t ALTER n SET DEFAULT 9", 0))
		run(referenceExec("INSERT INTO t(id) VALUES(6)", 1))
		run(referenceSelect("SELECT n FROM t WHERE id=1 OR id=6 ORDER BY id", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
		run(referenceExec("ALTER TABLE t ADD nothing INT DEFAULT NULL", 0))
		run(referenceSelect("SELECT adnum FROM pg_attrdef JOIN pg_class ON adrelid=pg_class.oid WHERE relname='t' ORDER BY adnum", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(2)}}))
		run(referenceExec("CREATE TABLE empty_t(id INT)", 0))
		run(referenceExec("ALTER TABLE empty_t ADD b INT NOT NULL", 0))
		referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, true, "SELECT * FROM empty_t", []string{"id", "b"}, []string{"INT4", "INT4"}, []bool{true, false}, 0)
		run(referenceImplicitError("INSERT INTO empty_t(id) VALUES(1)", referenceDiagnostic{Code: "23502", Message: `null value in column "b" of relation "empty_t" violates not-null constraint`, Detail: "Failing row contains (1, null).", Schema: "public", Table: "empty_t", Column: "b"}))
	})
}

func TestReferenceAlterAddColumnStable(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
		run(referenceExec("SET TIME ZONE 'UTC'", 0))
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
		run(referenceExec("ALTER TABLE t ADD z TIMESTAMPTZ DEFAULT (TIMESTAMP '2004-07-01 12:00' AT LOCAL)", 0))
		run(referenceExec("SET TIME ZONE 'America/New_York'", 0))
		run(referencePrepared(referenceExec("INSERT INTO t(id) VALUES(3)", 1)))
		noon := time.Date(2004, 7, 1, 12, 0, 0, 0, time.UTC)
		run(referenceSelect("SELECT id,z AT TIME ZONE 'UTC' AS z FROM t ORDER BY id", []string{"id", "z"}, []string{"INT4", "TIMESTAMP"}, [][]any{{int64(1), noon}, {int64(2), noon}, {int64(3), noon.Add(4 * time.Hour)}}))
	})
}

func TestReferenceAlterAddColumnVolatile(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE SEQUENCE s START 1 CACHE 1", 0))
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(2),(2)", 3))
		run(referenceExec("ALTER TABLE t ADD v BIGINT DEFAULT nextval('s')", 0))
		bag := referenceSelect("SELECT v FROM t", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
		bag.bag = true
		run(bag)
		run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
		run(referenceExec("INSERT INTO t(id) VALUES(3)", 1))
		run(referenceSelect("SELECT v FROM t WHERE id=3", []string{"v"}, []string{"INT8"}, [][]any{{int64(5)}}))
		run(referenceExec("CREATE SEQUENCE empty_s START 1 CACHE 1", 0))
		run(referenceExec("CREATE TABLE empty_t(id INT)", 0))
		run(referenceExec("ALTER TABLE empty_t ADD v BIGINT DEFAULT nextval('empty_s')", 0))
		run(referenceExec("INSERT INTO empty_t(id) VALUES(1)", 1))
		run(referenceSelect("SELECT v FROM empty_t", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(referenceExec("CREATE TABLE failing(id INT)", 0))
		run(referenceExec("INSERT INTO failing VALUES(1)", 1))
		run(referenceImplicitError("ALTER TABLE failing ADD v BIGINT DEFAULT nextval('empty_s') CONSTRAINT reject CHECK(v<0)", referenceDiagnostic{Code: "23514", Message: `check constraint "reject" of relation "failing" is violated by some row`, Schema: "public", Table: "failing", Constraint: "reject"}))
		run(referenceSelect("SELECT * FROM failing", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(referenceSelect("SELECT nextval('empty_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}
