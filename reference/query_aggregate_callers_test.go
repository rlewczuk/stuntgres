//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryAggregateBehaviorAndCallers(t *testing.T) {
	p := newReferencePair(t)
	for _, step := range []referenceStep{referenceExec("CREATE TABLE t(k TEXT,v INT)", 0), referenceExec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,2)", 3), referenceExec("CREATE TABLE empty(k TEXT,v INT)", 0), referenceExec("CREATE TABLE out(k TEXT,n BIGINT)", 0)} {
		p.step(t, step)
	}
	for _, step := range []referenceStep{
		referenceSelect("SELECT k FROM t GROUP BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}, {nil}}),
		referenceSelect("SELECT count(*),count(*) FROM t", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}),
		referenceSelect("SELECT count(*) FROM t WHERE FALSE", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		referenceSelect("SELECT count(*) FROM t WHERE NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		referenceSelect("SELECT count(*) FROM t WHERE v=NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		referenceSelect("SELECT k,count(*) FROM t GROUP BY k,k", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
		referenceSelect("SELECT 7 FROM t GROUP BY k", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}),
		referenceSelect("SELECT count(*)+1 FROM empty", []string{"?column?"}, []string{"INT8"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT count(*) FROM empty HAVING count(*)=0", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		referenceSelect("SELECT count(*) FROM empty HAVING count(*)>0", []string{"count"}, []string{"INT8"}, nil),
		referenceSelect("SELECT k,count(*) FROM t GROUP BY k HAVING k IS NULL", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{nil, int64(1)}}),
		referenceSelect("SELECT k FROM t GROUP BY k HAVING FALSE", []string{"k"}, []string{"TEXT"}, nil),
		referenceSelect("SELECT k FROM t GROUP BY k HAVING NULL", []string{"k"}, []string{"TEXT"}, nil),
		referenceSelect("SELECT count(*) FROM t HAVING $1", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}, true),
		referenceSelect("SELECT count(*) FROM t HAVING $1", []string{"count"}, []string{"INT8"}, nil, nil),
		referenceSelect("SELECT v+1,count(*) FROM t GROUP BY v", []string{"?column?", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}, {nil, int64(1)}, {int64(3), int64(1)}}),
		referenceSelect("SELECT k,count(*) FROM t GROUP BY 1", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
		referenceSelect("SELECT count(*) FROM t GROUP BY +1", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}),
		referenceSelect("SELECT count(*) FROM empty GROUP BY +1.5", []string{"count"}, []string{"INT8"}, nil),
		referenceSelect("SELECT k,count(*) FROM t GROUP BY (1)", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}}),
	} {
		step.bag = true
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceExec("CREATE TABLE pairs(a TEXT,b TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO pairs VALUES('a',NULL),('a',NULL),(NULL,'a'),(NULL,'a'),(NULL,NULL),('ab','c'),('a','bc')", 7))
	pair := referenceSelect("SELECT a,b,count(*) FROM pairs GROUP BY a,b", []string{"a", "b", "count"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{"a", nil, int64(2)}, {nil, "a", int64(2)}, {nil, nil, int64(1)}, {"ab", "c", int64(1)}, {"a", "bc", int64(1)}})
	pair.bag = true
	p.step(t, pair)
	p.step(t, referencePrepared(pair))
	for _, prepared := range []bool{false, true} {
		step := referenceExec("INSERT INTO out SELECT k,count(*) FROM t GROUP BY k", 2)
		step.prepared = prepared
		p.step(t, step)
	}
	out := referenceSelect("SELECT k,n FROM out", []string{"k", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {nil, int64(1)}, {"a", int64(2)}, {nil, int64(1)}})
	out.bag = true
	p.step(t, out)
	p.step(t, referenceExec("CREATE TABLE numbers(n SMALLINT)", 0))
	for _, step := range []referenceStep{referenceExec("INSERT INTO numbers SELECT count(*) FROM empty", 1), referenceExec("INSERT INTO numbers SELECT count(*) FROM empty GROUP BY k", 0), referenceExec("INSERT INTO numbers SELECT count(*) FROM t HAVING count(*)>5", 0)} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	p.step(t, referenceSelect("SELECT n FROM numbers", []string{"n"}, []string{"INT2"}, [][]any{{int64(0)}, {int64(0)}}))
	p.step(t, referenceExec("CREATE TABLE zero(v INT)", 0))
	p.step(t, referenceExec("INSERT INTO zero VALUES(NULL),(NULL)", 2))
	p.step(t, referenceExec("ALTER TABLE zero DROP COLUMN v", 0))
	zero := referenceSelect("SELECT count(*) FROM zero", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}})
	p.step(t, zero)
	p.step(t, referencePrepared(zero))
}

// Public GRANT remains an explicit PostgreSQL-only boundary. Native column ACL
// manifests are covered through the existing engine authority in package units.
func TestReferenceQueryAggregatePostgresColumnACL(t *testing.T) {
	p := newReferencePair(t)
	run := func(step referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step) }
	run(referenceExec("CREATE ROLE aggregate_reader", 0))
	run(referenceExec("CREATE TABLE t(k TEXT,v INT)", 0))
	t.Cleanup(func() {
		referenceAdmin.ExecContext(p.ctx, "DROP OWNED BY aggregate_reader CASCADE; DROP ROLE aggregate_reader")
	})
	if _, err := p.postgres.ExecContext(p.ctx, "GRANT USAGE ON SCHEMA public TO aggregate_reader"); err != nil {
		t.Fatal(err)
	}
	conn, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, referenceExec("SET SESSION AUTHORIZATION aggregate_reader", 0))
	denied := referenceImplicitError("SELECT count(*) FROM t", referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
	denied.selectRows = true
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, denied)
	if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT SELECT(v) ON t TO aggregate_reader"); err != nil {
		t.Fatal(err)
	}
	success := referenceSelect("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, success)
	referenceRunStep(t, p.ctx, conn, p.postgresRecord, referencePrepared(success))
	for _, q := range []string{"SELECT count(*) FROM t GROUP BY k", "SELECT count(*) FROM t GROUP BY k HAVING k IS NULL", "SELECT count(*) FROM t GROUP BY k ORDER BY k"} {
		step := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table t"})
		step.selectRows = true
		referenceRunStep(t, p.ctx, conn, p.postgresRecord, step)
	}
}

func TestReferenceQueryAggregatePreparedFreshnessAndHeldRows(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(k TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES('a'),('a'),(NULL)", 3))
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		stmt, err := db.PrepareContext(p.ctx, "SELECT count(*) FROM t")
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stmt.Close()
			held, err := stmt.QueryContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if _, err := db.ExecContext(p.ctx, "INSERT INTO t VALUES('b')"); err != nil {
				t.Fatal(err)
			}
			check := func(rows *sql.Rows, want int64) {
				t.Helper()
				defer rows.Close()
				names, err := rows.Columns()
				if err != nil || len(names) != 1 || names[0] != "count" {
					t.Fatal(names, err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT8" {
					t.Fatal(types, err)
				}
				if !rows.Next() {
					t.Fatal("missing global group", rows.Err())
				}
				var count int64
				if err := rows.Scan(&count); err != nil || count != want {
					t.Fatal(count, want, err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra global group", rows.Err())
				}
			}
			check(held, 3)
			fresh, err := stmt.QueryContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			check(fresh, 4)
		}()
	}
}
