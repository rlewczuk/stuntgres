package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func autoincRows(q string, rows ...[]any) fixture.Step {
	return fixture.Select(q, []string{"id", "label"}, []string{"INT4", "TEXT"}, rows)
}

func autoincScalar(q, name string, n int64) fixture.Step {
	return fixture.Select(q, []string{name}, []string{"INT8"}, [][]any{{n}})
}

// Routes distinguish fresh Prepare from actual retained statement execution.
func autoincRoute(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route, q string) func(fixture.Step) {
	t.Helper()
	var executor fixture.Executor = c
	if route == "held" {
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		t.Cleanup(func() { stmt.Close() })
		executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	}
	return func(s fixture.Step) {
		if route == "prepared" { s = fixture.Prepared(s) }
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
	}
}

func autoincSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE EXTENSION autoinc",
		"CREATE SEQUENCE s START 10 INCREMENT 10 MINVALUE 0 CACHE 1",
		"CREATE TABLE t(id integer,label text)",
		"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','s')")
}

func TestReferenceAutoincValues(t *testing.T) {
	for _, route := range []string{"direct", "prepared", "held"} {
		t.Run(route, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				autoincSeed(t, p, b, c)
				q := "INSERT INTO t(id,label) VALUES($1,$2)"
				run := autoincRoute(t, p, b, c, route, q)
				for _, row := range []struct{ id any; label string }{{nil,"a"},{int64(0),"b"},{int64(7),"c"},{int64(-7),"d"}} {
					run(fixture.Exec(q, 1, row.id, row.label))
				}
				run(fixture.Exec("INSERT INTO t(label) VALUES('e')", 1))
				run(autoincRows("SELECT id,label FROM t ORDER BY label", []any{int64(10),"a"}, []any{int64(20),"b"}, []any{int64(7),"c"}, []any{int64(-7),"d"}, []any{int64(30),"e"}))
				run(autoincRows("SELECT id,label FROM t WHERE false"))
				run(autoincScalar("SELECT currval('s')", "currval", 30))
				run(autoincScalar("SELECT lastval()", "lastval", 30))
				run(fixture.Exec("UPDATE t SET label='c' WHERE id=7", 1))
				run(autoincRows("UPDATE t SET id=0 WHERE label='c' RETURNING id,label", []any{int64(40),"c"}))
				run(autoincRows("UPDATE t SET id=NULL WHERE label='c' RETURNING id,label", []any{int64(50),"c"}))
				run(fixture.Exec("UPDATE t SET id=0 WHERE false", 0))
				run(autoincScalar("SELECT currval('s')", "currval", 50))
			})
		})
	}
}

func TestReferenceAutoincPairs(t *testing.T) {
	for _, initial := range []string{"NULL", "0", "7"} {
		t.Run("duplicate/"+initial, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE EXTENSION autoinc", "CREATE SEQUENCE s START 10", "CREATE SEQUENCE s2 START 100", "CREATE TABLE t(id integer)", "CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','s','id','s2')")
				run := autoincRoute(t, p, b, c, "direct", "")
				want := int64(100)
				if initial == "7" { want = 7 }
				run(fixture.Select("INSERT INTO t VALUES("+initial+") RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{want}}))
				if initial == "7" {
					run(fixture.Select("SELECT last_value,is_called FROM s", []string{"last_value","is_called"}, []string{"INT8","BOOL"}, [][]any{{int64(10),false}}))
					run(fixture.Select("SELECT last_value,is_called FROM s2", []string{"last_value","is_called"}, []string{"INT8","BOOL"}, [][]any{{int64(100),false}}))
				} else {
					run(autoincScalar("SELECT currval('s')", "currval", 10))
					run(autoincScalar("SELECT currval('s2')", "currval", 100))
					run(autoincScalar("SELECT lastval()", "lastval", 100))
				}
			})
		})
	}
	t.Run("distinct-and-chain", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE EXTENSION autoinc", "CREATE SEQUENCE s START 10", "CREATE SEQUENCE s2 START 100", "CREATE TABLE t(id integer,k integer)", "CREATE TRIGGER m BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','s','k','s2')")
			run := autoincRoute(t, p, b, c, "direct", "")
			rows := func(q string, a, z int64) fixture.Step { return fixture.Select(q, []string{"id","k"}, []string{"INT4","INT4"}, [][]any{{a,z}}) }
			run(rows("INSERT INTO t VALUES(NULL,0) RETURNING id,k",10,100))
			run(fixture.Prepared(rows("INSERT INTO t VALUES(7,NULL) RETURNING id,k",7,101)))
			userOperatorExec(t, p, b, c, "CREATE FUNCTION first_key() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.id:=77; RETURN NEW; END$$", "CREATE FUNCTION last_key() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.k:=NEW.id; RETURN NEW; END$$", "CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION first_key()", "CREATE TRIGGER z BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION last_key()")
			run(rows("INSERT INTO t VALUES(NULL,NULL) RETURNING id,k",77,77))
			run(autoincScalar("SELECT currval('s')", "currval", 10))
			run(autoincScalar("SELECT currval('s2')", "currval", 102))
		})
	})
}

func TestReferenceAutoincNarrowing(t *testing.T) {
	for _, tc := range []struct{ start, increment, stored, current int64 }{
		{2147483648,1,-2147483648,2147483648},
		{4294967296,1,1,4294967297},
		{0,10,10,10},
		{0,4294967296,0,4294967296},
	} {
		t.Run(fmt.Sprintf("%d-%d",tc.start,tc.increment), func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE EXTENSION autoinc", fmt.Sprintf("CREATE SEQUENCE s MINVALUE 0 START %d INCREMENT %d",tc.start,tc.increment), "CREATE TABLE t(id integer,label text)", "CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION autoinc('id','s')")
				run := autoincRoute(t,p,b,c,"direct","")
				run(autoincRows("INSERT INTO t VALUES(NULL,'a') RETURNING id,label", []any{tc.stored,"a"}))
				run(autoincScalar("SELECT currval('s')", "currval", tc.current))
				run(fixture.Exec("INSERT INTO t VALUES(-2147483648,'min'),(2147483647,'max')",2))
				run(autoincRows("SELECT id,label FROM t WHERE label<>'a' ORDER BY label", []any{int64(2147483647),"max"}, []any{int64(-2147483648),"min"}))
				run(autoincScalar("SELECT nextval('s')", "nextval", tc.current+tc.increment))
			})
		})
	}
}
