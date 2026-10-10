package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceHashIndexMaintenance(t *testing.T) {
	hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		hashIndexSeed(run)
		run(referenceExec("CREATE INDEX hn ON t USING hash(n)", 0))
		run(referenceExec("CREATE INDEX hs ON t USING hash(lower(s))", 0))
		q := "SELECT n,s FROM t WHERE n=$1"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}
		run(referencePrepared(referenceExec("INSERT INTO t VALUES(2,'b'),(NULL,NULL)", 2)))
		referenceRunStep(t, p.ctx, held, b.recorder, hashIndexBag(q, [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(2), "b"}}, int64(2)))
		run(hashIndexBag("SELECT n,s FROM t WHERE n IS NULL", [][]any{{nil, nil}, {nil, nil}}))
		run(referencePrepared(referenceExec("UPDATE t SET n=4 WHERE n=2", 3)))
		referenceRunStep(t, p.ctx, held, b.recorder, hashIndexBag(q, nil, int64(2)))
		referenceRunStep(t, p.ctx, held, b.recorder, hashIndexBag(q, [][]any{{int64(4), "b"}, {int64(4), "b"}, {int64(4), "b"}}, int64(4)))
		run(referencePrepared(referenceExec("DELETE FROM t WHERE n=4", 3)))
		referenceRunStep(t, p.ctx, held, b.recorder, hashIndexBag(q, nil, int64(4)))
		run(referenceExec("UPDATE t SET s='A' WHERE n=1", 1))
		run(hashIndexBag("SELECT n,s FROM t WHERE lower(s)='a'", [][]any{{int64(1), "A"}}))
		run(referencePrepared(referenceExec("INSERT INTO t SELECT n,s FROM t WHERE n=1", 1)))
		run(hashIndexBag("SELECT n,s FROM t WHERE lower(s)='a'", [][]any{{int64(1), "A"}, {int64(1), "A"}}))
		run(referenceSelect("SELECT n,s FROM t ORDER BY n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(1), "A"}, {int64(3), "c"}, {nil, nil}, {nil, nil}}))
		bad := "INSERT INTO t VALUES(9,'ok'),('bad','bad')"
		s := referenceImplicitError(bad, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: referencePosition(strings.Index(bad, "'bad'") + 1)})
		run(s)
		run(referencePrepared(s))
		run(hashIndexBag("SELECT n,s FROM t WHERE n=9", nil))
		run(referenceExec("INSERT INTO t VALUES(9,'valid')", 1))
		run(hashIndexBag("SELECT n,s FROM t WHERE n=9", [][]any{{int64(9), "valid"}}))
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE generated(id INT GENERATED ALWAYS AS IDENTITY,n INT DEFAULT 2,s TEXT DEFAULT 'd')", 0),
			referenceExec("CREATE INDEX generated_hash ON generated USING hash(n)", 0),
			referenceExec("INSERT INTO generated DEFAULT VALUES", 1),
			referencePrepared(referenceExec("INSERT INTO generated(s) VALUES('x')", 1)),
			referenceExec("INSERT INTO generated(n,s) SELECT n,s FROM t WHERE n=1", 2),
			referenceSelect("SELECT id,s FROM generated WHERE n=2 ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "d"}, {int64(2), "x"}}),
			referenceSelect("SELECT id,s FROM generated WHERE n=1 ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "A"}, {int64(4), "A"}}),
		} {
			run(s)
		}
	})
	t.Run("constraint-batch-atomicity", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
			run(referenceExec("CREATE TABLE t(n INT NOT NULL,s TEXT,CONSTRAINT hash_positive CHECK(n>0))", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'old'),(2,'keep')", 2))
			run(referenceExec("CREATE INDEX hn ON t USING hash(n)", 0))
			for _, tc := range []struct {
				q    string
				want referenceDiagnostic
			}{
				{"INSERT INTO t VALUES(3,'new'),(NULL,'bad')", referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "n"}},
				{"INSERT INTO t VALUES(3,'new'),(0,'bad')", referenceDiagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "hash_positive"`, Detail: "Failing row contains (0, bad).", Schema: "public", Table: "t", Constraint: "hash_positive"}},
				{"UPDATE t SET n=0 WHERE n=2", referenceDiagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "hash_positive"`, Detail: "Failing row contains (0, keep).", Schema: "public", Table: "t", Constraint: "hash_positive"}},
			} {
				s := referenceImplicitError(tc.q, tc.want)
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
				run(referenceSelect("SELECT n,s FROM t ORDER BY n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "keep"}}))
				run(hashIndexBag("SELECT n,s FROM t WHERE n=3", nil))
			}
			run(referenceExec("INSERT INTO t VALUES(3,'valid')", 1))
			run(hashIndexBag("SELECT n,s FROM t WHERE n=3", [][]any{{int64(3), "valid"}}))
		})
	})
	t.Run("derived-key-atomicity", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
			run(referenceExec("CREATE TABLE t(n INT,s TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES(2,'old'),(5,'keep')", 2))
			run(referenceExec("CREATE INDEX he ON t USING hash((10/n))", 0))
			for _, q := range []string{"INSERT INTO t VALUES(1,'new'),(0,'bad')", "UPDATE t SET n=n-2"} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
				run(referenceSelect("SELECT n,s FROM t ORDER BY n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "old"}, {int64(5), "keep"}}))
				run(hashIndexBag("SELECT n,s FROM t WHERE 10/n=5", [][]any{{int64(2), "old"}}))
			}
			hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX volatile_hash ON t USING hash(random())", referenceDiagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"})
			run(referenceExec("INSERT INTO t VALUES(1,'valid')", 1))
			run(hashIndexBag("SELECT n,s FROM t WHERE 10/n=10", [][]any{{int64(1), "valid"}}))
		})
	})
}

func TestReferenceHashIndexSnapshots(t *testing.T) {
	for _, level := range []sql.IsolationLevel{sql.LevelReadCommitted, sql.LevelRepeatableRead} {
		t.Run(level.String(), func(t *testing.T) {
			hashIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE t(id INT,n INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,2),(2,2),(3,1)", 3))
				run(referenceExec("CREATE INDEX hn ON t USING hash(n)", 0))
				a, c := referenceLockConnections(t, p.ctx, b.db)
				tx, err := a.BeginTx(p.ctx, &sql.TxOptions{Isolation: level})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				trun := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
				query := "SELECT id FROM t WHERE n=2 ORDER BY id"
				trun(referenceSelect(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("UPDATE t SET n=4 WHERE id=1", 1))
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("DELETE FROM t WHERE id=2", 1))
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO t VALUES(4,2),(5,2),(6,2)", 3))
				want := [][]any{{int64(4)}, {int64(5)}, {int64(6)}}
				if level == sql.LevelRepeatableRead {
					want = [][]any{{int64(1)}, {int64(2)}}
				}
				trun(referencePrepared(referenceSelect(query, []string{"id"}, []string{"INT4"}, want)))
				trun(referenceExec("INSERT INTO t VALUES(7,2)", 1))
				own := append(append([][]any(nil), want...), []any{int64(7)})
				trun(referenceSelect(query, []string{"id"}, []string{"INT4"}, own))
				trun(referenceExec("SAVEPOINT hash_point", 0))
				trun(referenceExec("UPDATE t SET n=2 WHERE id=3", 1))
				trun(referenceExec("DELETE FROM t WHERE id=7", 1))
				withThree := append([][]any{{int64(3)}}, want...)
				if level == sql.LevelRepeatableRead {
					withThree = [][]any{{int64(1)}, {int64(2)}, {int64(3)}}
				}
				trun(referenceSelect(query, []string{"id"}, []string{"INT4"}, withThree))
				trun(referenceExec("ROLLBACK TO hash_point", 0))
				trun(referenceSelect(query, []string{"id"}, []string{"INT4"}, own))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, a, b.recorder, referenceSelect(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(5)}, {int64(6)}}))
				commit, err := a.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer commit.Rollback()
				referenceRunStep(t, p.ctx, commit, b.recorder, referenceExec("INSERT INTO t VALUES(8,2)", 1))
				referenceRunStep(t, p.ctx, commit, b.recorder, referenceExec("UPDATE t SET n=2 WHERE id=3", 1))
				referenceRunStep(t, p.ctx, commit, b.recorder, referenceExec("DELETE FROM t WHERE id=4", 1))
				if err := commit.Commit(); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, a, b.recorder, referenceSelect(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(5)}, {int64(6)}, {int64(8)}}))
			})
		})
	}
}
