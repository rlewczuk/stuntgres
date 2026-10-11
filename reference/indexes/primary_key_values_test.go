package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePrimaryKeyValuesAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,payload TEXT,PRIMARY KEY(a,b))", 0))
				run(fixture.Exec("INSERT INTO t VALUES($1,$2,$3)", 1, int64(1), "x", "keep"))
				run(fixture.Exec("INSERT INTO t VALUES(1,'y','other'),(2,'x',NULL)", 2))
				unchanged := fixture.Select("SELECT * FROM t ORDER BY a,b", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "keep"}, {int64(1), "y", "other"}, {int64(2), "x", nil}})
				for _, s := range []fixture.Step{
					fixture.ErrorDiagnostic("INSERT INTO t VALUES($1,$2,$3)", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists."), int64(1), "x", "bad"),
					fixture.ErrorDiagnostic("INSERT INTO t VALUES($1,$2,$3)", fixture.PrimaryKeyReferenceNull("t", "a", "null, z, bad"), nil, "z", "bad"),
					fixture.ErrorDiagnostic("INSERT INTO t VALUES($1,$2,$3)", fixture.PrimaryKeyReferenceNull("t", "b", "3, null, bad"), int64(3), nil, "bad"),
					fixture.ErrorDiagnostic("INSERT INTO t(payload) VALUES('bad')", fixture.PrimaryKeyReferenceNull("t", "a", "null, null, bad")),
					fixture.ErrorDiagnostic("INSERT INTO t VALUES(3,'z','valid'),(1,'x','bad'),(4,'w','later')", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")),
					fixture.ErrorDiagnostic("INSERT INTO t SELECT a,b,payload FROM source ORDER BY a", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")),
					fixture.ErrorDiagnostic("UPDATE t SET b='x' WHERE a=1", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists.")),
					fixture.ErrorDiagnostic("UPDATE t SET a=NULL WHERE b='y'", fixture.PrimaryKeyReferenceNull("t", "a", "null, y, other")),
				} {
					if s.Query == "INSERT INTO t SELECT a,b,payload FROM source ORDER BY a" {
						run(fixture.Exec("CREATE TABLE source(a INT,b TEXT,payload TEXT)", 0))
						run(fixture.Exec("INSERT INTO source VALUES(0,'z','valid'),(1,'x','bad'),(4,'w','later')", 3))
					}
					s.ErrorStage = "execute"
					run(s)
					run(unchanged)
				}
				returning := fixture.ErrorDiagnostic("UPDATE t SET b='x' WHERE a=1 RETURNING a,b", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a, b)=(1, x) already exists."))
				returning.SelectRows, returning.ErrorStage = true, "execute"
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
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(k "+c.typ+" PRIMARY KEY)", 0))
				run(fixture.Exec("INSERT INTO t VALUES("+c.first+")", 1))
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("INSERT INTO t VALUES("+c.second+")", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (k)=("+c.detail+") already exists."))
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
				var value any = c.value
				if c.name == "float_zero" {
					value = float64(0)
				}
				run(fixture.Select("SELECT k FROM t", []string{"k"}, []string{c.dbtype}, [][]any{{value}}))
			})
		})
	}
}

func TestReferencePrimaryKeyDefaultProducers(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE defaults(k INT DEFAULT 7 PRIMARY KEY,v TEXT)", 0))
		run(fixture.Exec("INSERT INTO defaults(v) VALUES('seed')", 1))
		run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO defaults(v) VALUES($1)", fixture.UniqueReferenceDuplicate("defaults", "defaults_pkey", "Key (k)=(7) already exists."), "bad")))
		run(fixture.Select("SELECT * FROM defaults", []string{"k", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}))
		run(fixture.Exec("CREATE TABLE generated(a INT,k INT GENERATED ALWAYS AS(a+1) STORED PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO generated(a) VALUES(1)", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO generated(a) VALUES(2),(1)", fixture.UniqueReferenceDuplicate("generated", "generated_pkey", "Key (k)=(2) already exists.")))
		run(fixture.ErrorDiagnostic("INSERT INTO generated(a) VALUES(NULL)", fixture.PrimaryKeyReferenceNull("generated", "k", "null, null")))
		run(fixture.Select("SELECT * FROM generated", []string{"a", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		run(fixture.Exec("CREATE TABLE identity_t(k INT GENERATED ALWAYS AS IDENTITY PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO identity_t DEFAULT VALUES", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO identity_t OVERRIDING SYSTEM VALUE VALUES(1)", fixture.UniqueReferenceDuplicate("identity_t", "identity_t_pkey", "Key (k)=(1) already exists.")))
		run(fixture.Exec("INSERT INTO identity_t DEFAULT VALUES", 1))
		run(fixture.Select("SELECT k FROM identity_t ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Exec("CREATE TABLE merge_t(k INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO merge_t VALUES(1)", 1))
		run(fixture.ErrorDiagnostic("MERGE INTO merge_t t USING (VALUES(2),(1)) AS s(k) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.k)", fixture.UniqueReferenceDuplicate("merge_t", "merge_t_pkey", "Key (k)=(1) already exists.")))
		run(fixture.Select("SELECT k FROM merge_t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}

func TestReferencePrimaryKeyConflictCaller(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(a INT,b TEXT,CONSTRAINT pk PRIMARY KEY(a,b))", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'x')", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1,$2) ON CONFLICT ON CONSTRAINT pk DO NOTHING", 0, int64(1), "x")))
		run(fixture.Exec("INSERT INTO t VALUES(1,'x'),(2,'y') ON CONFLICT ON CONSTRAINT pk DO NOTHING", 1))
		run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES($1,$2) ON CONFLICT ON CONSTRAINT pk DO NOTHING", fixture.PrimaryKeyReferenceNull("t", "a", "null, x"), nil, "x")))
		run(fixture.Select("SELECT * FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "y"}}))
	})
}
