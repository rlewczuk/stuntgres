package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsValuesInheritance(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE parent_leaf() INHERITS(p)", "CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p ON DELETE CASCADE ON UPDATE CASCADE)", "CREATE TABLE child_leaf() INHERITS(c)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO parent_leaf VALUES(2)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO child_leaf VALUES(1),(999)", 2))
		run(fixture.ForeignKeyFail("INSERT INTO c VALUES(2)", fixture.ForeignKeyChild("c", "fk", `Key (pid)=(2) is not present in table "p".`), true))
		for _, prepared := range []bool{false, true} {
			for _, target := range []string{"ONLY p", "p"} {
				step := fixture.Exec("UPDATE "+target+" SET id=3 WHERE id=1", 1)
				step.Prepared = prepared
				run(step)
				run(foreignKeyIntRows("SELECT id FROM ONLY p", "id", 3))
				run(foreignKeyIntRows("SELECT pid FROM ONLY c", "pid", 3))
				run(foreignKeyIntRows("SELECT id FROM parent_leaf", "id", 2))
				run(foreignKeyIntRows("SELECT pid FROM child_leaf ORDER BY pid", "pid", 1, 999))
				step = fixture.Exec("UPDATE "+target+" SET id=1 WHERE id=3", 1)
				step.Prepared = prepared
				run(step)
				run(foreignKeyIntRows("SELECT id FROM ONLY p", "id", 1))
				run(foreignKeyIntRows("SELECT pid FROM ONLY c", "pid", 1))
			}
		}
		run(foreignKeyIntRows("SELECT id FROM parent_leaf", "id", 2))
		run(fixture.Exec("DROP TABLE parent_leaf", 0))
		run(fixture.Prepared(fixture.Exec("UPDATE p SET id=3 WHERE id=1", 1)))
		run(foreignKeyIntRows("SELECT pid FROM ONLY c", "pid", 3))
		run(foreignKeyIntRows("SELECT pid FROM child_leaf ORDER BY pid", "pid", 1, 999))
		run(fixture.Prepared(fixture.Exec("DELETE FROM p WHERE id=3", 1)))
		run(foreignKeyIntRows("SELECT pid FROM ONLY c", "pid"))
		run(foreignKeyIntRows("SELECT pid FROM child_leaf ORDER BY pid", "pid", 1, 999))
	})
}

func TestReferenceForeignActionsMetadataNullableChild(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT REFERENCES p ON DELETE SET NULL,note TEXT)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1,'kept'),(NULL,NULL)", 2))
		run(fixture.Exec("DELETE FROM p", 1))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT pid,note FROM c ORDER BY note NULLS LAST", []string{"pid", "note"}, []string{"INT4", "TEXT"}, [][]any{{nil, "kept"}, {nil, nil}})
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				cols, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for i, col := range cols {
					n, k := col.Nullable()
					if b.PG {
						if n || k {
							t.Fatal("pq nullable availability", i, n, k)
						}
					} else if !k || !n {
						t.Fatal("child nullable metadata", i, n, k)
					}
					length, known := col.Length()
					want := int64(0)
					if i == 1 {
						want = 9223372036854775807
					}
					if length != want || known != (i == 1) {
						t.Fatal("child length metadata", i, length, known, want)
					}
				}
			}
			s.CheckScan = func(t *testing.T, r *sql.Rows, i int) {
				var pid sql.NullInt64
				var note sql.NullString
				err := r.Scan(&pid, &note)
				want := sql.NullString{}
				if i == 0 {
					want = sql.NullString{String: "kept", Valid: true}
				}
				if err != nil || pid.Valid || note != want {
					t.Fatal(pid, note, want, err)
				}
			}
			run(s)
		}
	})
}

func TestReferenceForeignActionsDefinitionPrecedence(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT)", 0))
		q := "CREATE TABLE c(pid INT,CONSTRAINT fk FOREIGN KEY(pid) REFERENCES p(id) ON DELETE SET NULL(missing))"
		for _, prepared := range []bool{false, true} {
			run(fixture.ForeignKeyFail(q, fixture.ForeignKeyError("42703", `column "missing" referenced in foreign key constraint does not exist`, "", "", ""), prepared))
		}
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='c'", []string{"relname"}, []string{"NAME"}, nil))
	})
}

func TestReferenceForeignActionsLifecycleSelf(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE self(a INT PRIMARY KEY,b INT REFERENCES self(a) ON UPDATE CASCADE ON DELETE CASCADE)", 0))
		run(fixture.Exec("INSERT INTO self VALUES(0,0),(1,1)", 2))
		if !b.PG {
			foreignKeyRoundtrip(t, p)
		}
		run(fixture.Prepared(fixture.Exec("UPDATE self SET a=123 WHERE a=0", 1)))
		run(fixture.Exec("UPDATE self SET a=456 WHERE a=123", 1))
		run(fixture.Select("SELECT a,b FROM self ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(456), int64(456)}}))
		run(fixture.Exec("DELETE FROM self WHERE a=456", 1))
		run(foreignKeyIntRows("SELECT b FROM self", "b", 1))
	})
}

func TestReferenceForeignActionsFeatureSmokeSelections(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE pset(tenant INT,author INT,PRIMARY KEY(tenant,author))", 0))
		run(fixture.Exec("CREATE TABLE cset(tenant INT,author INT,CONSTRAINT subset_fk FOREIGN KEY(tenant,author) REFERENCES pset ON DELETE SET NULL(author,author))", 0))
		run(fixture.Exec("INSERT INTO pset VALUES(7,2)", 1))
		run(fixture.Exec("INSERT INTO cset VALUES(7,2)", 1))
		run(fixture.Exec("CREATE TABLE pd(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE cd(pid INT DEFAULT 1 CONSTRAINT default_fk REFERENCES pd ON DELETE SET DEFAULT)", 0))
		run(fixture.Exec("INSERT INTO pd VALUES(0),(1),(2)", 3))
		run(fixture.Exec("INSERT INTO cd VALUES(2)", 1))
		q := "DELETE FROM pd WHERE id=$1"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec("ALTER TABLE cd ALTER pid SET DEFAULT 0", 0))
		if !b.PG {
			foreignKeyRoundtrip(t, p)
			stmt.Close()
			stmt, err = b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
		}
		run(fixture.Prepared(fixture.Exec("DELETE FROM pset", 1)))
		run(fixture.Select("SELECT tenant,author FROM cset", []string{"tenant", "author"}, []string{"INT4", "INT4"}, [][]any{{int64(7), nil}}))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1, 2))
		run(foreignKeyIntRows("SELECT pid FROM cd", "pid", 0))
		s := fixture.ForeignKeyFail(q, fixture.ForeignKeyParent("pd", "cd", "default_fk", `Key (id)=(0) is still referenced from table "cd".`), false)
		s.Args = []any{0}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, s)
		run(foreignKeyIntRows("SELECT id FROM pd ORDER BY id", "id", 0, 1))
		run(foreignKeyIntRows("SELECT pid FROM cd", "pid", 0))
	})
}
