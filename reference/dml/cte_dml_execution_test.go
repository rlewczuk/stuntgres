package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCTEDMLCompletion(t *testing.T) {
	for _, kind := range []string{"insert", "update", "delete", "merge"} {
		for _, demand := range []string{"unused", "no_returning", "limit0", "false", "partial"} {
			t.Run(kind+"/"+demand, func(t *testing.T) {
				cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					cteDMLSeed(t, p, b)
					body := "UPDATE t SET v=v+1"
					returned := [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}}
					final := returned
					switch kind {
					case "insert":
						body = "INSERT INTO t VALUES(3,30),(4,40)"
						returned = [][]any{{int64(3), int64(30)}, {int64(4), int64(40)}}
						final = [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(3), int64(30)}, {int64(4), int64(40)}}
					case "delete":
						body = "DELETE FROM t"
						returned = [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
						final = [][]any{}
					case "merge":
						body = "MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=t.v+1"
					}
					if demand != "no_returning" {
						body += " RETURNING t.id,t.v"
					}
					q := "WITH d AS (" + body + ") "
					s := fixture.Select(q+"SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}})
					switch demand {
					case "limit0":
						s = fixture.Select(q+"SELECT * FROM d LIMIT 0", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{})
					case "false":
						s = fixture.Select(q+"SELECT * FROM d WHERE FALSE", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{})
					case "partial":
						s = fixture.Select(q+"SELECT * FROM d ORDER BY id LIMIT 1", []string{"id", "v"}, []string{"INT4", "INT4"}, returned[:1])
					}
					cteDMLRun(t, p, b, prepared, s)
					cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, final))
				})
			})
		}
	}
}

func TestReferenceCTEDMLSnapshot(t *testing.T) {
	cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		cteDMLSeed(t, p, b)
		q := "WITH u AS (UPDATE t SET v=v+1 RETURNING id,v), old_rows AS (SELECT id,v FROM t) SELECT u.id,u.v,o.v,t.v FROM u JOIN old_rows o ON u.id=o.id JOIN t ON t.id=u.id"
		cteDMLRun(t, p, b, prepared, cteDMLBag(q, []string{"id", "v", "v", "v"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(11), int64(10), int64(10)}, {int64(2), int64(21), int64(20), int64(20)}}))
		cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}}))
		for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE a(id INT)", 0), fixture.Exec("CREATE TABLE physical(id INT)", 0), fixture.Exec("CREATE TABLE returned(id INT)", 0), fixture.Exec("INSERT INTO a VALUES(9)", 1)} {
			cteDMLRun(t, p, b, false, s)
		}
		q = "WITH i AS (INSERT INTO a VALUES(1) RETURNING id), p AS (INSERT INTO physical SELECT id FROM a RETURNING id), r AS (INSERT INTO returned SELECT id FROM i RETURNING id) SELECT p.id,r.id FROM p CROSS JOIN r"
		cteDMLRun(t, p, b, prepared, fixture.Select(q, []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(1)}}))
		cteDMLRun(t, p, b, false, fixture.Select("SELECT id FROM physical", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		q = "WITH one AS (UPDATE t SET v=v+1 WHERE id=1 RETURNING id,v), two AS (UPDATE t SET v=v+2 WHERE id=2 RETURNING id,v) SELECT one.v,two.v FROM one CROSS JOIN two"
		cteDMLRun(t, p, b, prepared, fixture.Select(q, []string{"v", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(12), int64(23)}}))
		cteDMLRun(t, p, b, false, fixture.Exec("CREATE TABLE output_only(v INT)", 0))
		cteDMLRun(t, p, b, false, fixture.Exec("INSERT INTO output_only VALUES(70)", 1))
		q = "WITH changed AS (UPDATE output_only SET v=80 RETURNING v), u AS (UPDATE t SET v=v WHERE id=1 RETURNING (SELECT v FROM output_only) AS before) SELECT u.before,changed.v FROM u CROSS JOIN changed"
		cteDMLRun(t, p, b, prepared, fixture.Select(q, []string{"before", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(70), int64(80)}}))
		tx, err := b.DB.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.LockExec(t, p.Ctx, tx, "INSERT INTO a VALUES(8)", 1)
		q = "WITH i AS (INSERT INTO a VALUES(2) RETURNING id) SELECT id FROM a ORDER BY id"
		s := fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(8)}, {int64(9)}})
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestReferenceCTEDMLPrimary(t *testing.T) {
	for _, kind := range []string{"select", "insert", "update", "delete", "merge", "zero"} {
		for _, queryRows := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/exec", true: "/query"}[queryRows], func(t *testing.T) {
				cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
					cteDMLSeed(t, p, b)
					cteDMLRun(t, p, b, false, fixture.Exec("CREATE TABLE dst(id INT,v INT)", 0))
					prefix := "WITH d AS (DELETE FROM t RETURNING id,v) "
					q := prefix + "INSERT INTO dst SELECT * FROM d"
					count := int64(2)
					want := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
					if kind == "update" || kind == "delete" || kind == "merge" || kind == "zero" {
						cteDMLRun(t, p, b, false, fixture.Exec("INSERT INTO dst VALUES(1,100)", 1))
					}
					switch kind {
					case "select":
						q = prefix + "SELECT id,v FROM d"
						if !queryRows {
							count = 2
						}
					case "update":
						q = prefix + "UPDATE dst SET v=d.v FROM d WHERE dst.id=d.id"
						count = 1
						want = [][]any{{int64(1), int64(10)}}
					case "delete":
						q = prefix + "DELETE FROM dst USING d WHERE dst.id=d.id"
						count = 1
						want = [][]any{{int64(1), int64(100)}}
					case "merge":
						q = prefix + "MERGE INTO dst USING d ON dst.id=d.id WHEN MATCHED THEN UPDATE SET v=d.v WHEN NOT MATCHED THEN INSERT VALUES(d.id,d.v)"
						if b.PG {
							count = 0
						}
					case "zero":
						q = prefix + "UPDATE dst SET v=99 WHERE FALSE"
						count = 0
						want = [][]any{}
					}
					if kind != "select" {
						q += " RETURNING dst.id,dst.v"
					}
					s := fixture.Exec(q, count)
					if queryRows {
						s = cteDMLBag(q, []string{"id", "v"}, []string{"INT4", "INT4"}, want)
					}
					cteDMLRun(t, p, b, prepared, s)
					cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}))
					final := want
					if kind == "select" || kind == "delete" {
						final = [][]any{}
					}
					if kind == "zero" {
						final = [][]any{{int64(1), int64(100)}}
					}
					cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM dst", []string{"id", "v"}, []string{"INT4", "INT4"}, final))
				})
			})
		}
	}
}

func TestReferenceCTEDMLConsumers(t *testing.T) {
	for _, form := range []string{"ctas", "into", "no_data"} {
		t.Run(form, func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				cteDMLSeed(t, p, b)
				q := "CREATE TABLE copied AS WITH d AS (DELETE FROM t RETURNING id,v) SELECT * FROM d"
				if form == "into" {
					q = "WITH d AS (DELETE FROM t RETURNING id,v) SELECT * INTO copied FROM d"
				}
				count := int64(2)
				want := [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}
				source := [][]any{}
				if form == "no_data" {
					q += " WITH NO DATA"
					count = 0
					source = want
					want = [][]any{}
				}
				cteDMLRun(t, p, b, prepared, fixture.Exec(q, count))
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM copied", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, source))
			})
		})
	}
	for _, form := range []string{"ctas", "into"} {
		t.Run(form+"_atomic", func(t *testing.T) {
			cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				cteDMLSeed(t, p, b)
				q := "CREATE TABLE copied AS WITH d AS (DELETE FROM t RETURNING id,v) SELECT 1/(id-id) AS bad FROM d"
				if form == "into" {
					q = "WITH d AS (DELETE FROM t RETURNING id,v) SELECT 1/(id-id) AS bad INTO copied FROM d"
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.ErrorStage = "execute"
				cteDMLRun(t, p, b, prepared, s)
				cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
				cteDMLRun(t, p, b, false, fixture.Select("SELECT relname FROM pg_class WHERE relname='copied'", []string{"relname"}, []string{"NAME"}, [][]any{}))
			})
		})
	}
}

func TestReferenceCTEDMLFeatureSmoke(t *testing.T) {
	cteDMLRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		cteDMLSeed(t, p, b)
		cteDMLRun(t, p, b, false, fixture.Exec("CREATE TABLE dst(id INT,v INT)", 0))
		q := "WITH moved AS (DELETE FROM t RETURNING id,v), copied AS (INSERT INTO dst SELECT * FROM moved RETURNING id,v) SELECT a.id,a.v,b.v FROM copied a JOIN copied b ON a.id=b.id"
		cteDMLRun(t, p, b, prepared, cteDMLBag(q, []string{"id", "v", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(20), int64(20)}}))
		cteDMLRun(t, p, b, false, cteDMLBag("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}))
	})
}
