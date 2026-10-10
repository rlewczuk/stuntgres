package reference_test

import (
	"database/sql"
	"testing"
)

func sqlPreparedForm(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, form string) {
	sqlPreparedSetup(t, p, b, c)
	switch form {
	case "values":
		sqlPreparedStep(t, p, b, c, referenceExec("PREPARE pv(int) AS VALUES($1)", 0))
		for _, s := range []referenceStep{sqlPreparedScalar("EXECUTE pv(7)", "column1", "INT4", int64(7)), sqlPreparedScalar("EXECUTE pv(NULL)", "column1", "INT4", nil)} {
			s.prepared = true
			sqlPreparedStep(t, p, b, c, s)
		}
	case "insert_select":
		sqlPreparedStep(t, p, b, c, referenceExec("CREATE TABLE prep_source(v int,s text,f bool)", 0))
		sqlPreparedStep(t, p, b, c, referenceExec("INSERT INTO prep_source VALUES(1,'one',TRUE),(2,'two',FALSE)", 2))
		sqlPreparedStep(t, p, b, c, referenceExec("PREPARE pi(int) AS INSERT INTO prep_t SELECT v,s,f FROM prep_source WHERE v=$1", 0))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT count(*) FROM prep_t", "count", "INT8", int64(2)))
		sqlPreparedStep(t, p, b, c, referencePrepared(referenceExec("EXECUTE pi(2)", 1)))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT s FROM prep_t WHERE v=2", "s", "TEXT", "two"))
	case "update":
		sqlPreparedStep(t, p, b, c, referenceExec("PREPARE pu(text,int) AS UPDATE prep_t SET s=$1 WHERE v=$2 RETURNING v,s", 0))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT s FROM prep_t WHERE v=7", "s", "TEXT", "seven"))
		sqlPreparedStep(t, p, b, c, referencePrepared(referenceSelect("EXECUTE pu('changed',7)", []string{"v","s"}, []string{"INT4","TEXT"}, [][]any{{int64(7),"changed"}})))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT s FROM prep_t WHERE v=8", "s", "TEXT", "eight"))
		sqlPreparedStep(t, p, b, c, referenceExec("EXECUTE pu('again',7)", 1))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT s FROM prep_t WHERE v=7", "s", "TEXT", "again"))
	case "delete":
		sqlPreparedStep(t, p, b, c, referenceExec("PREPARE pd(int) AS DELETE FROM prep_t WHERE v=$1 RETURNING v", 0))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT count(*) FROM prep_t", "count", "INT8", int64(2)))
		sqlPreparedStep(t, p, b, c, referencePrepared(sqlPreparedScalar("EXECUTE pd(8)", "v", "INT4", int64(8))))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT v FROM prep_t", "v", "INT4", int64(7)))
		sqlPreparedStep(t, p, b, c, referenceExec("EXECUTE pd(7)", 1))
	case "merge":
		sqlPreparedStep(t, p, b, c, referenceExec("PREPARE pm(text) AS MERGE INTO prep_t USING (VALUES(9)) s(id) ON prep_t.v=s.id WHEN NOT MATCHED THEN INSERT(v,s,f) VALUES(s.id,$1,TRUE)", 0))
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT count(*) FROM prep_t", "count", "INT8", int64(2)))
		count := int64(1)
		if b.pg { count = 0 }
		sqlPreparedStep(t, p, b, c, referencePrepared(referenceExec("EXECUTE pm('merged')", count)))
		sqlPreparedStep(t, p, b, c, referenceSelect("SELECT v,s FROM prep_t WHERE v=9", []string{"v","s"}, []string{"INT4","TEXT"}, [][]any{{int64(9),"merged"}}))
	}
}

func TestReferenceSQLPreparedCurrentForms(t *testing.T) {
	for _, form := range []string{"values","insert_select","update","delete","merge"} {
		t.Run(form, func(t *testing.T) { sqlPreparedScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) { sqlPreparedForm(t, p, b, c, form) }) })
	}
}

func TestReferenceSQLPreparedWriteAtomicity(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlPreparedStep(t, p, b, c, referenceExec("CREATE TABLE prep_atomic(v int CONSTRAINT prep_atomic_key UNIQUE)", 0))
		sqlPreparedStep(t, p, b, c, referenceExec("INSERT INTO prep_atomic VALUES(7)", 1))
		definition := "PREPARE p(int) AS INSERT INTO prep_atomic VALUES($1),($1) RETURNING v"
		if !b.pg {
			// Keep the PostgreSQL RETURNING oracle without extending native INSERT.
			definition = "PREPARE p(int) AS INSERT INTO prep_atomic VALUES($1),($1)"
		}
		sqlPreparedStep(t, p, b, c, referenceExec(definition, 0))
		d := sqlPreparedDiagnostic("23505", `duplicate key value violates unique constraint "prep_atomic_key"`)
		d.Detail = "Key (v)=(8) already exists."
		d.Schema, d.Table, d.Constraint = "public", "prep_atomic", "prep_atomic_key"
		s := referenceErrorDiagnostic("EXECUTE p(8)", d)
		s.selectRows = true
		s.errorStage = "execute"
		sqlPreparedStep(t, p, b, c, s)
		sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT v FROM prep_atomic", "v", "INT4", int64(7)))
	})
}
