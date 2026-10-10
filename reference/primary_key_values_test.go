package reference_test

import (
	"fmt"
	"testing"
)

func TestReferencePrimaryKeyValuesAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec("CREATE TABLE t(a INT,b TEXT,payload TEXT,PRIMARY KEY(a,b))", 0))
				run(referenceExec("INSERT INTO t VALUES($1,$2,$3)", 1, int64(1), "x", "keep"))
				run(referenceExec("INSERT INTO t VALUES(1,'y','other'),(2,'x',NULL)", 2))
				unchanged := referenceSelect("SELECT * FROM t ORDER BY a,b", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "keep"}, {int64(1), "y", "other"}, {int64(2), "x", nil}})
				for _, s := range []referenceStep{
					referenceErrorDiagnostic("INSERT INTO t VALUES($1,$2,$3)", uniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists."), int64(1), "x", "bad"),
					referenceErrorDiagnostic("INSERT INTO t VALUES($1,$2,$3)", primaryKeyReferenceNull("t", "a", "null, z, bad"), nil, "z", "bad"),
					referenceErrorDiagnostic("INSERT INTO t VALUES($1,$2,$3)", primaryKeyReferenceNull("t", "b", "3, null, bad"), int64(3), nil, "bad"),
					referenceErrorDiagnostic("INSERT INTO t(payload) VALUES('bad')", primaryKeyReferenceNull("t", "a", "null, null, bad")),
					referenceErrorDiagnostic("INSERT INTO t VALUES(3,'z','valid'),(1,'x','bad'),(4,'w','later')", uniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")),
					referenceErrorDiagnostic("INSERT INTO t SELECT a,b,payload FROM source ORDER BY a", uniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")),
					referenceErrorDiagnostic("UPDATE t SET b='x' WHERE a=1", uniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")),
					referenceErrorDiagnostic("UPDATE t SET a=NULL WHERE b='y'", primaryKeyReferenceNull("t", "a", "null, y, other")),
				} {
					if s.query == "INSERT INTO t SELECT a,b,payload FROM source ORDER BY a" {
						run(referenceExec("CREATE TABLE source(a INT,b TEXT,payload TEXT)", 0))
						run(referenceExec("INSERT INTO source VALUES(0,'z','valid'),(1,'x','bad'),(4,'w','later')", 3))
					}
					s.errorStage = "execute"
					run(s)
					run(unchanged)
				}
				returning := referenceErrorDiagnostic("UPDATE t SET b='x' WHERE a=1 RETURNING a,b", uniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists."))
				returning.selectRows, returning.errorStage = true, "execute"
				run(returning)
				run(unchanged)
			})
		})
	}
}

func TestReferencePrimaryKeyTypedValues(t *testing.T) {
	for _, c := range []struct{ name, typ, first, second, value, dbtype, detail string }{
		{"numeric", "NUMERIC", "1.0", "1.00", "1.0", "NUMERIC", "1.00"},
		{"bpchar", "CHAR(3)", "'x'", "'x '", "x  ", "BPCHAR", "x  "},
		{"float_zero", "DOUBLE PRECISION", "'0'", "'-0'", "", "FLOAT8", "-0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec("CREATE TABLE t(k "+c.typ+" PRIMARY KEY)", 0))
				run(referenceExec("INSERT INTO t VALUES("+c.first+")", 1))
				for _, prepared := range []bool{false, true} {
					s := referenceErrorDiagnostic("INSERT INTO t VALUES("+c.second+")", uniqueReferenceDuplicate("t", "t_pkey", "Key (k)=("+c.detail+") already exists."))
					s.prepared, s.errorStage = prepared, "execute"
					run(s)
				}
				var value any = c.value
				if c.name == "float_zero" {
					value = float64(0)
				}
				run(referenceSelect("SELECT k FROM t", []string{"k"}, []string{c.dbtype}, [][]any{{value}}))
			})
		})
	}
}

func TestReferencePrimaryKeyDefaultProducers(t *testing.T) {
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE defaults(k INT DEFAULT 7 PRIMARY KEY,v TEXT)", 0))
		run(referenceExec("INSERT INTO defaults(v) VALUES('seed')", 1))
		run(referencePrepared(referenceErrorDiagnostic("INSERT INTO defaults(v) VALUES($1)", uniqueReferenceDuplicate("defaults", "defaults_pkey", "Key (k)=(7) already exists."), "bad")))
		run(referenceSelect("SELECT * FROM defaults", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}))
		run(referenceExec("CREATE TABLE generated(a INT,k INT GENERATED ALWAYS AS(a+1) STORED PRIMARY KEY)", 0))
		run(referenceExec("INSERT INTO generated(a) VALUES(1)", 1))
		run(referenceErrorDiagnostic("INSERT INTO generated(a) VALUES(2),(1)", uniqueReferenceDuplicate("generated", "generated_pkey", "Key (k)=(2) already exists.")))
		run(referenceErrorDiagnostic("INSERT INTO generated(a) VALUES(NULL)", primaryKeyReferenceNull("generated", "k", "null, null")))
		run(referenceSelect("SELECT * FROM generated", []string{"a", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		run(referenceExec("CREATE TABLE identity_t(k INT GENERATED ALWAYS AS IDENTITY PRIMARY KEY)", 0))
		run(referenceExec("INSERT INTO identity_t DEFAULT VALUES", 1))
		run(referenceErrorDiagnostic("INSERT INTO identity_t OVERRIDING SYSTEM VALUE VALUES(1)", uniqueReferenceDuplicate("identity_t", "identity_t_pkey", "Key (k)=(1) already exists.")))
		run(referenceExec("INSERT INTO identity_t DEFAULT VALUES", 1))
		run(referenceSelect("SELECT k FROM identity_t ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(referenceExec("CREATE TABLE merge_t(k INT PRIMARY KEY)", 0))
		run(referenceExec("INSERT INTO merge_t VALUES(1)", 1))
		run(referenceErrorDiagnostic("MERGE INTO merge_t t USING (VALUES(2),(1)) AS s(k) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.k)", uniqueReferenceDuplicate("merge_t", "merge_t_pkey", "Key (k)=(1) already exists.")))
		run(referenceSelect("SELECT k FROM merge_t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}

func TestReferencePrimaryKeyConflictCaller(t *testing.T) {
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(a INT,b TEXT,CONSTRAINT pk PRIMARY KEY(a,b))", 0))
		run(referenceExec("INSERT INTO t VALUES(1,'x')", 1))
		run(referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2) ON CONFLICT ON CONSTRAINT pk DO NOTHING", 0, int64(1), "x")))
		run(referenceExec("INSERT INTO t VALUES(1,'x'),(2,'y') ON CONFLICT ON CONSTRAINT pk DO NOTHING", 1))
		run(referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES($1,$2) ON CONFLICT ON CONSTRAINT pk DO NOTHING", primaryKeyReferenceNull("t", "a", "null, x"), nil, "x")))
		run(referenceSelect("SELECT * FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "y"}}))
	})
}
