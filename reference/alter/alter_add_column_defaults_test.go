package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAlterAddColumnDefaults(t *testing.T) {
	alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
		run(fixture.Exec("ALTER TABLE t ADD n INT DEFAULT (1+2*3) NOT NULL", 0))
		run(fixture.Exec("INSERT INTO t(id) VALUES(3)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(4,DEFAULT),(5,99)", 2)))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(7)}, {int64(3), int64(7)}, {int64(4), int64(7)}, {int64(5), int64(99)}}))
		run(fixture.Exec("ALTER TABLE t ALTER n SET DEFAULT 9", 0))
		run(fixture.Exec("INSERT INTO t(id) VALUES(6)", 1))
		run(fixture.Select("SELECT n FROM t WHERE id=1 OR id=6 ORDER BY id", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
		run(fixture.Exec("ALTER TABLE t ADD nothing INT DEFAULT NULL", 0))
		run(fixture.Select("SELECT adnum FROM pg_attrdef JOIN pg_class ON adrelid=pg_class.oid WHERE relname='t' ORDER BY adnum", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(2)}}))
		run(fixture.Exec("CREATE TABLE empty_t(id INT)", 0))
		run(fixture.Exec("ALTER TABLE empty_t ADD b INT NOT NULL", 0))
		fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, true, "SELECT * FROM empty_t", []string{"id", "b"}, []string{"INT4", "INT4"}, []bool{true, false}, 0)
		run(fixture.ImplicitError("INSERT INTO empty_t(id) VALUES(1)", fixture.Diagnostic{Code: "23502", Message: `null value in column "b" of relation "empty_t" violates not-null constraint`, Detail: "Failing row contains (1, null).", Schema: "public", Table: "empty_t", Column: "b"}))
	})
}

func TestReferenceAlterAddColumnStable(t *testing.T) {
	alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
		run(fixture.Exec("ALTER TABLE t ADD z TIMESTAMPTZ DEFAULT (TIMESTAMP '2004-07-01 12:00' AT LOCAL)", 0))
		run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO t(id) VALUES(3)", 1)))
		noon := time.Date(2004, 7, 1, 12, 0, 0, 0, time.UTC)
		run(fixture.Select("SELECT id,z AT TIME ZONE 'UTC' AS z FROM t ORDER BY id", []string{"id", "z"}, []string{"INT4", "TIMESTAMP"}, [][]any{{int64(1), noon}, {int64(2), noon}, {int64(3), noon.Add(4 * time.Hour)}}))
	})
}

func TestReferenceAlterAddColumnVolatile(t *testing.T) {
	alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE SEQUENCE s START 1 CACHE 1", 0))
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(2),(2)", 3))
		run(fixture.Exec("ALTER TABLE t ADD v BIGINT DEFAULT nextval('s')", 0))
		bag := fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
		bag.Bag = true
		run(bag)
		run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
		run(fixture.Exec("INSERT INTO t(id) VALUES(3)", 1))
		run(fixture.Select("SELECT v FROM t WHERE id=3", []string{"v"}, []string{"INT8"}, [][]any{{int64(5)}}))
		run(fixture.Exec("CREATE SEQUENCE empty_s START 1 CACHE 1", 0))
		run(fixture.Exec("CREATE TABLE empty_t(id INT)", 0))
		run(fixture.Exec("ALTER TABLE empty_t ADD v BIGINT DEFAULT nextval('empty_s')", 0))
		run(fixture.Exec("INSERT INTO empty_t(id) VALUES(1)", 1))
		run(fixture.Select("SELECT v FROM empty_t", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Exec("CREATE TABLE failing(id INT)", 0))
		run(fixture.Exec("INSERT INTO failing VALUES(1)", 1))
		run(fixture.ImplicitError("ALTER TABLE failing ADD v BIGINT DEFAULT nextval('empty_s') CONSTRAINT reject CHECK(v<0)", fixture.Diagnostic{Code: "23514", Message: `check constraint "reject" of relation "failing" is violated by some row`, Schema: "public", Table: "failing", Constraint: "reject"}))
		run(fixture.Select("SELECT * FROM failing", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT nextval('empty_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}
