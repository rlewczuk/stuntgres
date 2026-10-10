package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSQLPreparedExpressions(t *testing.T) {
	for _, tc := range []struct { actual string; value any }{
		{"1+2", int64(3)}, {"'7'::int", int64(7)}, {"CASE WHEN TRUE THEN 7 ELSE 8 END", int64(7)},
		{"COALESCE(NULL,8)", int64(8)}, {"NULL::int", nil}, {"abs(-7)", int64(7)},
	} {
		t.Run(tc.actual, func(t *testing.T) {
			sqlPreparedScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				sqlPreparedStep(t, p, b, c, referenceExec("PREPARE p(int) AS SELECT $1", 0))
				for _, prepared := range []bool{false,true} {
					s := sqlPreparedScalar("EXECUTE p("+tc.actual+")", "?column?", "INT4", tc.value)
					s.prepared = prepared
					sqlPreparedStep(t, p, b, c, s)
				}
			})
		})
	}
	t.Run("boolean_scalar", func(t *testing.T) {
		sqlPreparedScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			sqlPreparedStep(t, p, b, c, referenceExec("PREPARE p(bool) AS SELECT $1", 0))
			sqlPreparedStep(t, p, b, c, referencePrepared(sqlPreparedScalar("EXECUTE p(1<2)", "?column?", "BOOL", true)))
		})
	})
}

func sqlPreparedZero(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	sqlPreparedStep(t, p, b, c, referenceExec("CREATE SEQUENCE prep_probe_seq START 41", 0))
	sqlPreparedStep(t, p, b, c, referenceExec("PREPARE z AS SELECT 1", 0))
	for _, actual := range []string{"2", "1/0", "nextval('prep_probe_seq')", "(SELECT 1)", "count(*)", "row_number() OVER()"} {
		for _, prepared := range []bool{false,true} {
			s := sqlPreparedScalar("EXECUTE z("+actual+")", "?column?", "INT4", int64(1))
			s.prepared = prepared
			sqlPreparedStep(t, p, b, c, s)
		}
	}
	sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT nextval('prep_probe_seq')", "nextval", "INT8", int64(41)))
}

func TestReferenceSQLPreparedZeroFormalExtras(t *testing.T) {
	// REL_18_0 ExecuteQuery bypasses EvaluateParams for zero formal parameters.
	// These are independent predictions, not rows learned from the oracle.
	for _, actual := range []string{"2", "1/0", "nextval('prep_probe_seq')", "(SELECT 1)", "count(*)", "row_number() OVER()"} {
		for _, prepared := range []bool{false,true} {
			route := "direct"
			if prepared { route = "handle" }
			t.Run(actual+"/"+route, func(t *testing.T) {
				sqlPreparedScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
					sqlPreparedStep(t, p, b, c, referenceExec("CREATE SEQUENCE prep_probe_seq START 41", 0))
					sqlPreparedStep(t, p, b, c, referenceExec("PREPARE z AS SELECT 1", 0))
					s := sqlPreparedScalar("EXECUTE z("+actual+")", "?column?", "INT4", int64(1))
					s.prepared = prepared
					sqlPreparedStep(t, p, b, c, s)
					sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT nextval('prep_probe_seq')", "nextval", "INT8", int64(41)))
				})
			})
		}
	}
}

func TestReferenceSQLPreparedActualEvaluation(t *testing.T) {
	sqlPreparedScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		sqlPreparedStep(t, p, b, c, referenceExec("CREATE SEQUENCE prep_draw START 51", 0))
		sqlPreparedStep(t, p, b, c, referenceExec("CREATE TABLE prep_draws(v bigint)", 0))
		sqlPreparedStep(t, p, b, c, referenceExec("PREPARE p(bigint) AS INSERT INTO prep_draws VALUES($1)", 0))
		q := "EXECUTE p(nextval('prep_draw'))"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil { t.Fatal("EXECUTE description", err) }
		defer stmt.Close()
		// A fresh session has no currval: describing EXECUTE must draw nothing.
		sqlPreparedStep(t, p, b, c, referenceErrorDiagnostic("SELECT currval('prep_draw')", sqlPreparedDiagnostic("55000", `currval of sequence "prep_draw" is not yet defined in this session`)))
		held := referenceHeldStatement{referenceExecutor:c, statement:stmt, query:q}
		for _, value := range []int64{51,52} {
			sqlPreparedStep(t, p, b, held, referenceExec(q, 1))
			sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT currval('prep_draw')", "currval", "INT8", value))
		}
		sqlPreparedStep(t, p, b, c, referenceSelect("SELECT v FROM prep_draws ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(51)},{int64(52)}}))
	})
}
