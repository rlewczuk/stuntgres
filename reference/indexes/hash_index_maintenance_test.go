package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHashIndexMaintenance(t *testing.T) {
	hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		hashIndexSeed(run)
		run(fixture.Exec("CREATE INDEX hn ON t USING hash(n)", 0))
		run(fixture.Exec("CREATE INDEX hs ON t USING hash(lower(s))", 0))
		q := "SELECT n,s FROM t WHERE n=$1"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(2,'b'),(NULL,NULL)", 2)))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, hashIndexBag(q, [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(2), "b"}}, int64(2)))
		run(hashIndexBag("SELECT n,s FROM t WHERE n IS NULL", [][]any{{nil, nil}, {nil, nil}}))
		run(fixture.Prepared(fixture.Exec("UPDATE t SET n=4 WHERE n=2", 3)))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, hashIndexBag(q, nil, int64(2)))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, hashIndexBag(q, [][]any{{int64(4), "b"}, {int64(4), "b"}, {int64(4), "b"}}, int64(4)))
		run(fixture.Prepared(fixture.Exec("DELETE FROM t WHERE n=4", 3)))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, hashIndexBag(q, nil, int64(4)))
		run(fixture.Exec("UPDATE t SET s='A' WHERE n=1", 1))
		run(hashIndexBag("SELECT n,s FROM t WHERE lower(s)='a'", [][]any{{int64(1), "A"}}))
		run(fixture.Prepared(fixture.Exec("INSERT INTO t SELECT n,s FROM t WHERE n=1", 1)))
		run(hashIndexBag("SELECT n,s FROM t WHERE lower(s)='a'", [][]any{{int64(1), "A"}, {int64(1), "A"}}))
		run(fixture.Select("SELECT n,s FROM t ORDER BY n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(1), "A"}, {int64(3), "c"}, {nil, nil}, {nil, nil}}))
		bad := "INSERT INTO t VALUES(9,'ok'),('bad','bad')"
		s := fixture.ImplicitError(bad, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: fixture.Position(strings.Index(bad, "'bad'") + 1)})
		run(s)
		run(fixture.Prepared(s))
		run(hashIndexBag("SELECT n,s FROM t WHERE n=9", nil))
		run(fixture.Exec("INSERT INTO t VALUES(9,'valid')", 1))
		run(hashIndexBag("SELECT n,s FROM t WHERE n=9", [][]any{{int64(9), "valid"}}))
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE generated(id INT GENERATED ALWAYS AS IDENTITY,n INT DEFAULT 2,s TEXT DEFAULT 'd')", 0),
			fixture.Exec("CREATE INDEX generated_hash ON generated USING hash(n)", 0),
			fixture.Exec("INSERT INTO generated DEFAULT VALUES", 1),
			fixture.Prepared(fixture.Exec("INSERT INTO generated(s) VALUES('x')", 1)),
			fixture.Exec("INSERT INTO generated(n,s) SELECT n,s FROM t WHERE n=1", 2),
			fixture.Select("SELECT id,s FROM generated WHERE n=2 ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "d"}, {int64(2), "x"}}),
			fixture.Select("SELECT id,s FROM generated WHERE n=1 ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "A"}, {int64(4), "A"}}),
		} {
			run(s)
		}
	})
	t.Run("constraint-batch-atomicity", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(n INT NOT NULL,s TEXT,CONSTRAINT hash_positive CHECK(n>0))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'old'),(2,'keep')", 2))
			run(fixture.Exec("CREATE INDEX hn ON t USING hash(n)", 0))
			for _, tc := range []struct {
				q    string
				want fixture.Diagnostic
			}{
				{"INSERT INTO t VALUES(3,'new'),(NULL,'bad')", fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "n"}},
				{"INSERT INTO t VALUES(3,'new'),(0,'bad')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "hash_positive"`, Detail: "Failing row contains (0, bad).", Schema: "public", Table: "t", Constraint: "hash_positive"}},
				{"UPDATE t SET n=0 WHERE n=2", fixture.Diagnostic{Code: "23514", Message: `new row for relation "t" violates check constraint "hash_positive"`, Detail: "Failing row contains (0, keep).", Schema: "public", Table: "t", Constraint: "hash_positive"}},
			} {
				s := fixture.ImplicitError(tc.q, tc.want)
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Prepared(s))
				run(fixture.Select("SELECT n,s FROM t ORDER BY n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "keep"}}))
				run(hashIndexBag("SELECT n,s FROM t WHERE n=3", nil))
			}
			run(fixture.Exec("INSERT INTO t VALUES(3,'valid')", 1))
			run(hashIndexBag("SELECT n,s FROM t WHERE n=3", [][]any{{int64(3), "valid"}}))
		})
	})
	t.Run("derived-key-atomicity", func(t *testing.T) {
		hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE t(n INT,s TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(2,'old'),(5,'keep')", 2))
			run(fixture.Exec("CREATE INDEX he ON t USING hash((10/n))", 0))
			for _, q := range []string{"INSERT INTO t VALUES(1,'new'),(0,'bad')", "UPDATE t SET n=n-2"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Prepared(s))
				run(fixture.Select("SELECT n,s FROM t ORDER BY n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "old"}, {int64(5), "keep"}}))
				run(hashIndexBag("SELECT n,s FROM t WHERE 10/n=5", [][]any{{int64(2), "old"}}))
			}
			hashIndexSemanticFailure(t, p, b, run, "CREATE INDEX volatile_hash ON t USING hash(random())", fixture.Diagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"})
			run(fixture.Exec("INSERT INTO t VALUES(1,'valid')", 1))
			run(hashIndexBag("SELECT n,s FROM t WHERE 10/n=10", [][]any{{int64(1), "valid"}}))
		})
	})
}

func TestReferenceHashIndexSnapshots(t *testing.T) {
	for _, level := range []sql.IsolationLevel{sql.LevelReadCommitted, sql.LevelRepeatableRead} {
		t.Run(level.String(), func(t *testing.T) {
			hashIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(id INT,n INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,2),(2,2),(3,1)", 3))
				run(fixture.Exec("CREATE INDEX hn ON t USING hash(n)", 0))
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				tx, err := a.BeginTx(p.Ctx, &sql.TxOptions{Isolation: level})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
				query := "SELECT id FROM t WHERE n=2 ORDER BY id"
				trun(fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE t SET n=4 WHERE id=1", 1))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM t WHERE id=2", 1))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO t VALUES(4,2),(5,2),(6,2)", 3))
				want := [][]any{{int64(4)}, {int64(5)}, {int64(6)}}
				if level == sql.LevelRepeatableRead {
					want = [][]any{{int64(1)}, {int64(2)}}
				}
				trun(fixture.Prepared(fixture.Select(query, []string{"id"}, []string{"INT4"}, want)))
				trun(fixture.Exec("INSERT INTO t VALUES(7,2)", 1))
				own := append(append([][]any(nil), want...), []any{int64(7)})
				trun(fixture.Select(query, []string{"id"}, []string{"INT4"}, own))
				trun(fixture.Exec("SAVEPOINT hash_point", 0))
				trun(fixture.Exec("UPDATE t SET n=2 WHERE id=3", 1))
				trun(fixture.Exec("DELETE FROM t WHERE id=7", 1))
				withThree := append([][]any{{int64(3)}}, want...)
				if level == sql.LevelRepeatableRead {
					withThree = [][]any{{int64(1)}, {int64(2)}, {int64(3)}}
				}
				trun(fixture.Select(query, []string{"id"}, []string{"INT4"}, withThree))
				trun(fixture.Exec("ROLLBACK TO hash_point", 0))
				trun(fixture.Select(query, []string{"id"}, []string{"INT4"}, own))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(5)}, {int64(6)}}))
				commit, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer commit.Rollback()
				fixture.RunStep(t, p.Ctx, commit, b.Recorder, fixture.Exec("INSERT INTO t VALUES(8,2)", 1))
				fixture.RunStep(t, p.Ctx, commit, b.Recorder, fixture.Exec("UPDATE t SET n=2 WHERE id=3", 1))
				fixture.RunStep(t, p.Ctx, commit, b.Recorder, fixture.Exec("DELETE FROM t WHERE id=4", 1))
				if err := commit.Commit(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(5)}, {int64(6)}, {int64(8)}}))
			})
		})
	}
}
