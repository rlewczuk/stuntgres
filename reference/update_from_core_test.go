package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateFromOneToOne(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				updateFromSeed(run)
				step := referenceExec("UPDATE t AS x SET v=s.n,prev=x.v FROM s WHERE x.id=s.k", 2)
				if prepared {
					step = referencePrepared(step)
				}
				run(step)
				run(updateFromTargets([][]any{{int64(1), int64(101), int64(10)}, {int64(2), int64(20), int64(0)}, {int64(3), int64(303), int64(30)}, {int64(4), int64(40), int64(0)}}))
				run(referenceExec("UPDATE t x SET v=s.n FROM s WHERE x.id=s.k", 2))
				for _, predicate := range []string{"FALSE", "NULL", "x.id=99"} {
					run(referenceExec("UPDATE t x SET v=s.n FROM s WHERE "+predicate, 0))
				}
				run(referenceExec("DELETE FROM s", 4))
				run(referenceExec("UPDATE t x SET v=0 FROM s", 0))
			})
		})
	}
}

func TestReferenceUpdateFromPrepared(t *testing.T) {
	updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		updateFromSeed(run)
		run(referenceExec("CREATE TABLE r(k INT,z INT)", 0))
		run(referenceExec("INSERT INTO r VALUES(1,9),(3,8)", 2))
		q := "UPDATE t x SET v=s.n+$1 FROM s JOIN r ON s.k=r.k AND r.z>$2 WHERE x.id=s.k AND x.id=$3 RETURNING x.id,x.v,s.n"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct {
			id   any
			rows [][]any
		}{{int64(1), [][]any{{int64(1), int64(102), int64(101)}}}, {nil, nil}, {int64(3), [][]any{{int64(3), int64(304), int64(303)}}}} {
			referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT4"}, tc.rows, int64(1), int64(0), tc.id))
		}
	})
}

func TestReferenceUpdateFromReturning(t *testing.T) {
	for _, output := range []struct {
		sql  string
		cols []string
		rows [][]any
	}{
		{"x.id,old.v,new.v,s.n", []string{"id", "v", "v", "n"}, [][]any{{int64(1), int64(10), int64(101), int64(101)}, {int64(3), int64(30), int64(303), int64(303)}}},
		{"*", []string{"id", "v", "prev", "k", "n"}, [][]any{{int64(1), int64(101), int64(10), int64(1), int64(101)}, {int64(3), int64(303), int64(30), int64(3), int64(303)}}},
	} {
		t.Run(output.sql, func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				updateFromSeed(run)
				types := make([]string, len(output.cols))
				for i := range types {
					types[i] = "INT4"
				}
				s := referenceSelect("UPDATE t x SET v=s.n,prev=x.v FROM s WHERE x.id=s.k RETURNING "+output.sql, output.cols, types, output.rows)
				s.bag = true
				run(s)
				run(referenceSelect("UPDATE t x SET v=s.n FROM s WHERE FALSE RETURNING "+output.sql, output.cols, types, nil))
			})
		})
	}
	updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
		run(referenceExec("CREATE TABLE s(k INT,n INT,txt TEXT,bytes BYTEA,short VARCHAR(4),amount NUMERIC(6,2))", 0))
		run(referenceExec("INSERT INTO s VALUES(1,101,'wide','\\x0102'::bytea,'abcd',12.34)", 1))
		run(referenceSelect("UPDATE t x SET v=old.n FROM s old WHERE x.id=old.k RETURNING x.*,old.*", []string{"id", "v", "k", "n", "txt", "bytes", "short", "amount"}, []string{"INT4", "INT4", "INT4", "INT4", "TEXT", "BYTEA", "VARCHAR", "NUMERIC"}, [][]any{{int64(1), int64(101), int64(1), int64(101), "wide", string([]byte{1, 2}), "abcd", "12.34"}}))
	})
}

func TestReferenceUpdateFromSelfJoin(t *testing.T) {
	updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(referenceExec("CREATE TABLE t(id INT,v INT,parent INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10,NULL),(2,20,1),(3,30,2)", 3))
		run(referenceExec("UPDATE t x SET v=src.v FROM t src WHERE x.parent=src.id", 2))
		run(referenceSelect("SELECT id,v,parent FROM t ORDER BY id", []string{"id", "v", "parent"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), nil}, {int64(2), int64(10), int64(1)}, {int64(3), int64(20), int64(2)}}))
		run(referenceExec("BEGIN", 0))
		run(referenceExec("INSERT INTO t VALUES(4,40,3)", 1))
		run(referenceExec("UPDATE t SET v=99 WHERE id=3", 1))
		run(referenceExec("UPDATE t x SET v=t.v FROM t WHERE x.parent=t.id", 3))
		run(referenceSelect("SELECT v FROM t WHERE id=4", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		run(referenceExec("ROLLBACK", 0))
	})
}

func TestReferenceUpdateFromQuerySources(t *testing.T) {
	for _, source := range []string{"s JOIN r ON s.k=r.k", "s JOIN r USING(k)", "s NATURAL JOIN r", "(s JOIN r USING(k))", "s,r"} {
		t.Run(source, func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				updateFromSeed(run)
				run(referenceExec("CREATE TABLE r(k INT,z INT)", 0))
				run(referenceExec("INSERT INTO r VALUES(1,9),(3,8),(NULL,7)", 3))
				run(referenceExec("UPDATE t x SET v=s.n+r.z FROM "+source+" WHERE x.id=s.k AND s.k=r.k", 2))
				run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(110)}, {int64(2), int64(20)}, {int64(3), int64(311)}, {int64(4), int64(40)}}))
				run(referenceExec("UPDATE t x SET v=s.n+r.z FROM s JOIN r USING(k) AS u WHERE x.id=u.k", 2))
				run(referenceExec("CREATE VIEW sv AS SELECT k,n FROM s", 0))
				run(referenceExec("UPDATE t x SET v=q.value FROM sv AS q(key,value) WHERE x.id=q.key", 2))
				run(referenceExec("UPDATE t x SET v=j.n+j.z FROM (s JOIN r USING(k)) AS j WHERE x.id=j.k", 2))
			})
		})
	}
}

func TestReferenceUpdateFromMultipleMatches(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,10),(1,10),(2,20)", 3))
				run(referenceExec("CREATE TABLE s(k INT,n INT)", 0))
				run(referenceExec("INSERT INTO s VALUES(1,101),(1,202),(1,101)", 3))
				q := "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k RETURNING x.id,old.v,new.v,s.n"
				var stmt *sql.Stmt
				var err error
				if prepared {
					stmt, err = c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(p.ctx)
				} else {
					rows, err = c.QueryContext(p.ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				cols, err := rows.Columns()
				if err != nil {
					t.Fatal(err)
				}
				if len(cols) != 4 || cols[0] != "id" || cols[1] != "v" || cols[2] != "v" || cols[3] != "n" {
					t.Fatal(cols)
				}
				count := 0
				for rows.Next() {
					var id, old, next, source int64
					if err := rows.Scan(&id, &old, &next, &source); err != nil {
						t.Fatal(err)
					}
					if id != 1 || old != 10 || next != source || (next != 101 && next != 202) {
						t.Fatalf("ineligible output %d,%d,%d,%d", id, old, next, source)
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if count != 2 {
					t.Fatalf("physical target count %d", count)
				}
				run(referenceSelect("SELECT v FROM t WHERE id=2", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}}))
				run(referenceExec("UPDATE t x SET v=x.v FROM s WHERE x.id=s.k", 2))
				run(referenceExec("CREATE SEQUENCE demand", 0))
				rows, err = c.QueryContext(p.ctx, "UPDATE t x SET v=s.n+nextval('demand')::int FROM s WHERE x.id=s.k RETURNING x.v-s.n,nextval('demand')")
				if err != nil {
					t.Fatal(err)
				}
				count = 0
				for rows.Next() {
					var assignment, output int64
					if err := rows.Scan(&assignment, &output); err != nil {
						t.Fatal(err)
					}
					if assignment <= 0 || output <= assignment {
						t.Fatal(assignment, output)
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if count != 2 {
					t.Fatal(count)
				}
				// Six admitted pairs demand SET; only two physical replacements demand
				// RETURNING. Do not pin traversal-specific returned draws or source winner.
				run(referenceSelect("SELECT last_value FROM demand", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(8)}}))
			})
		})
	}
}
