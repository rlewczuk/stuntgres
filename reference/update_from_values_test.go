package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateFromValues(t *testing.T) {
	updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
		columns := []string{"id", "v", "v", "k", "n"}
		types := []string{"INT4", "INT4", "INT4", "INT8", "INT4"}
		q := "UPDATE t x SET v=q.n FROM (VALUES (1::bigint,101),(2::bigint,202),(NULL::bigint,707)) q(k,n) WHERE x.id=q.k RETURNING x.id,old.v,new.v,q.k,q.n"
		step := referenceSelect(q, columns, types, [][]any{{int64(1), int64(10), int64(101), int64(1), int64(101)}, {int64(2), int64(20), int64(202), int64(2), int64(202)}})
		step.bag = true
		run(step)
		run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}, {int64(2), int64(202)}, {int64(3), int64(30)}}))
		empty := "UPDATE t x SET v=q.n FROM (VALUES (1::bigint,101),(2::bigint,202)) q(k,n) WHERE FALSE RETURNING x.id,old.v,new.v,q.k,q.n"
		for _, prepared := range []bool{false, true} {
			step := referenceSelect(empty, columns, types, nil)
			if prepared {
				step = referencePrepared(step)
			}
			run(step)
		}
		q = "UPDATE t x SET v=q.n FROM (VALUES ($1::bigint,$2::int),(NULL::bigint,$3::int)) q(k,n) WHERE x.id=q.k AND $4::bool RETURNING x.id,old.v,new.v,q.k,q.n"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct {
			key      any
			value    int64
			admitted bool
			expected [][]any
		}{
			{int64(1), 111, true, [][]any{{int64(1), int64(101), int64(111), int64(1), int64(111)}}},
			{int64(2), 222, true, [][]any{{int64(2), int64(202), int64(222), int64(2), int64(222)}}},
			{nil, 333, true, nil},
			{int64(3), 333, false, nil},
			{int64(3), 333, true, [][]any{{int64(3), int64(30), int64(333), int64(3), int64(333)}}},
		} {
			step := referenceSelect(q, columns, types, tc.expected, tc.key, tc.value, int64(707), tc.admitted)
			referenceRunStep(t, p.ctx, held, b.recorder, step)
		}
		run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(111)}, {int64(2), int64(222)}, {int64(3), int64(333)}}))
		for _, prepared := range []bool{false, true} {
			step := referenceExec("UPDATE t x SET v=q.n FROM (VALUES (1,111),(2,222),(NULL,707)) q(k,n) WHERE x.id=q.k", 2)
			if prepared {
				step = referencePrepared(step)
			}
			run(step)
		}
	})
}

func TestReferenceUpdateFromValuesPhysicalSource(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,10),(2,20),(3,30)", 3))
				run(referenceExec("CREATE TABLE s(k INT,extra INT)", 0))
				run(referenceExec("INSERT INTO s VALUES(1,1),(2,2),(4,4),(NULL,7)", 4))
				q := "UPDATE t x SET v=q.n+s.extra FROM (VALUES (1::bigint,100),(2::bigint,200),(NULL::bigint,700)) q(k,n) JOIN s ON q.k=s.k WHERE x.id=q.k RETURNING x.id,old.v,new.v,q.k,q.n,s.extra"
				step := referenceSelect(q, []string{"id", "v", "v", "k", "n", "extra"}, []string{"INT4", "INT4", "INT4", "INT8", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(101), int64(1), int64(100), int64(1)}, {int64(2), int64(20), int64(202), int64(2), int64(200), int64(2)}})
				step.bag = true
				if prepared {
					step = referencePrepared(step)
				}
				run(step)
				run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}, {int64(2), int64(202)}, {int64(3), int64(30)}}))
			})
		})
	}
}

func TestReferenceUpdateFromValuesTargetDiagnostics(t *testing.T) {
	for _, prefix := range []string{"", "/*é*/ "} {
		for _, populated := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(prefix+map[bool]string{false: "empty", true: "populated"}[populated]+map[bool]string{false: "/exec", true: "/prepare"}[prepared], func(t *testing.T) {
					updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
						run := updateFromRun(t, p, b, c)
						run(referenceExec("CREATE TABLE t(id INT,v INT)", 0))
						var expected [][]any
						if populated {
							run(referenceExec("INSERT INTO t VALUES(1,10)", 1))
							expected = [][]any{{int64(1), int64(10)}}
						}
						q := prefix + "UPDATE t x SET v=q.n FROM (VALUES (x.id,7)) q(k,n)"
						want := controlsReferenceDiagnostic("42P01", `invalid reference to FROM-clause entry for table "x"`)
						want.Position = updateFromPosition(q, "x.id")
						want.Detail = `There is an entry for table "x", but it cannot be referenced from this part of the query.`
						var err error
						if prepared {
							var stmt *sql.Stmt
							stmt, err = c.PrepareContext(p.ctx, q)
							if stmt != nil {
								stmt.Close()
							}
						} else {
							_, err = c.ExecContext(p.ctx, q)
						}
						if err == nil {
							t.Fatal("VALUES source referenced forbidden UPDATE target")
						}
						referenceAssertDiagnostic(t, err, &want)
						run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, expected))
					})
				})
			}
		}
	}
}
