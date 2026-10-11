package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartialIndexUniqueBuildPolicies(t *testing.T) {
	for _, definition := range []string{"(k)", "(k DESC NULLS FIRST) INCLUDE(id)", "(k int4_ops,id)", "(k) NULLS NOT DISTINCT"} {
		t.Run(definition, func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE u(id INT,k INT,active BOOL)", 0))
				run(fixture.Exec("INSERT INTO u VALUES(1,7,FALSE),(1,7,NULL),(2,7,TRUE)", 3))
				run(fixture.Prepared(fixture.Exec("CREATE UNIQUE INDEX pu ON u"+definition+" WHERE active", 0)))
				detail := "Key (k)=(7) already exists."
				if definition == "(k int4_ops,id)" {
					detail = "Key (k, id)=(7, 2) already exists."
				}
				run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO u VALUES(2,7,TRUE)", fixture.UniqueReferenceDuplicate("u", "pu", detail))))
				run(fixture.Select("SELECT id,k,active FROM u ORDER BY id,active NULLS LAST", []string{"id", "k", "active"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(7), false}, {int64(1), int64(7), nil}, {int64(2), int64(7), true}}))
			})
		})
	}
}

func TestReferencePartialIndexUniqueNullPolicies(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE u(id INT,k INT,j INT,active BOOL)", 0))
		run(fixture.Exec("INSERT INTO u VALUES(1,NULL,1,TRUE),(2,NULL,1,TRUE),(3,NULL,1,FALSE),(4,NULL,1,NULL)", 4))
		run(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k,j) INCLUDE(id) WHERE active", 0))
		run(fixture.Exec("INSERT INTO u VALUES(5,NULL,1,TRUE)", 1))
		run(fixture.Exec("DROP INDEX pu", 0))
		build := uniqueIndexReferenceBuildDiagnostic("u", "pu", "Key (k, j)=(null, 1) is duplicated.")
		run(fixture.Prepared(fixture.ErrorDiagnostic("CREATE UNIQUE INDEX pu ON u(k,j) INCLUDE(id) NULLS NOT DISTINCT WHERE active", build)))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pu'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 4, 5))
		run(fixture.Exec("DELETE FROM u WHERE id=2 OR id=5", 2))
		run(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k,j) INCLUDE(id) NULLS NOT DISTINCT WHERE active", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO u VALUES(6,NULL,1,TRUE)", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k, j)=(null, 1) already exists.")))
		run(fixture.Exec("INSERT INTO u VALUES(6,NULL,2,TRUE),(7,NULL,1,FALSE),(8,NULL,1,NULL)", 3))
		run(fixture.PartialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 1, 6))
	})
}

func TestReferencePartialIndexUniqueConstantPredicates(t *testing.T) {
	for _, predicate := range []string{"TRUE", "FALSE", "NULL"} {
		t.Run(predicate, func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE u(id INT,k INT)", 0))
				run(fixture.Exec("INSERT INTO u VALUES(1,7),(2,7)", 2))
				q := "CREATE UNIQUE INDEX pu ON u(k) WHERE " + predicate
				if predicate == "TRUE" {
					run(fixture.ErrorDiagnostic(q, uniqueIndexReferenceBuildDiagnostic("u", "pu", "Key (k)=(7) is duplicated.")))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pu'", []string{"relname"}, []string{"NAME"}, nil))
					run(fixture.Exec("DELETE FROM u WHERE id=2", 1))
					run(fixture.Prepared(fixture.Exec(q, 0)))
					run(fixture.ErrorDiagnostic("INSERT INTO u VALUES(3,7)", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")))
					run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1))
				} else {
					run(fixture.Prepared(fixture.Exec(q, 0)))
					run(fixture.Exec("INSERT INTO u VALUES(3,7)", 1))
					run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3))
				}
			})
		})
	}
}

func TestReferencePartialIndexUniqueCandidates(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE u(id INT,k INT,active BOOL)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k) WHERE active", 0))
				apply := func(s fixture.Step) {
					if held {
						s = fixture.Prepared(s)
					}
					run(s)
				}
				apply(fixture.Exec("INSERT INTO u VALUES(1,7,FALSE),(2,7,NULL),(3,7,TRUE)", 3))
				apply(fixture.Exec("INSERT INTO u VALUES(4,7,FALSE),(5,7,NULL)", 2))
				for _, q := range []string{"INSERT INTO u VALUES(6,8,TRUE),(7,8,TRUE)", "INSERT INTO u VALUES(6,8,TRUE),(7,8,FALSE),(8,8,TRUE)"} {
					apply(fixture.ErrorDiagnostic(q, fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(8) already exists.")))
					run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 4, 5))
				}
				dup := fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")
				apply(fixture.ErrorDiagnostic("UPDATE u SET active=TRUE WHERE id=1 OR id=2", dup))
				s := fixture.ErrorDiagnostic("UPDATE u SET active=TRUE WHERE id=1 RETURNING id", dup)
				s.SelectRows = true
				apply(s)
				apply(fixture.Exec("UPDATE u SET k=k WHERE id=3", 1))
				apply(fixture.Exec("UPDATE u SET active=NULL WHERE id=3", 1))
				apply(fixture.Exec("UPDATE u SET active=TRUE WHERE id=1", 1))
				apply(fixture.Exec("UPDATE u SET active=FALSE WHERE id=1", 1))
				apply(fixture.Exec("UPDATE u SET active=TRUE WHERE id=2", 1))
				run(fixture.PartialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 2))
				run(fixture.PartialIndexIDs("SELECT id FROM u WHERE active IS NULL ORDER BY id", 3, 5))
				run(fixture.Exec("INSERT INTO u VALUES(6,8,TRUE)", 1))
				apply(fixture.ErrorDiagnostic("UPDATE u SET k=7 WHERE active", dup))
				run(fixture.Select("SELECT id,k FROM u WHERE active ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(7)}, {int64(6), int64(8)}}))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
				trun(fixture.Exec("INSERT INTO u VALUES(7,9,FALSE),(8,9,NULL)", 2))
				trun(fixture.Exec("INSERT INTO u VALUES(9,9,TRUE)", 1))
				trun(fixture.Exec("SAVEPOINT s", 0))
				trun(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO u VALUES(10,9,TRUE)", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(9) already exists."))))
				trun(fixture.Exec("ROLLBACK TO s", 0))
				trun(fixture.Exec("INSERT INTO u VALUES(10,10,TRUE)", 1))
				trun(fixture.Exec("ROLLBACK TO s", 0))
				trun(fixture.Exec("INSERT INTO u VALUES(11,10,TRUE)", 1))
				trun(fixture.Exec("DELETE FROM u WHERE id=9", 1))
				trun(fixture.Exec("INSERT INTO u VALUES(12,9,TRUE)", 1))
				trun(fixture.PartialIndexIDs("SELECT id FROM u WHERE active ORDER BY id", 2, 6, 11, 12))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				run(fixture.PartialIndexIDs("SELECT id FROM u ORDER BY id", 1, 2, 3, 4, 5, 6, 7, 8, 11, 12))
			})
		})
	}
}

func TestReferencePartialIndexUniqueFinalization(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE u(id INT GENERATED BY DEFAULT AS IDENTITY,k INT DEFAULT 7,active BOOL DEFAULT TRUE)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k) WHERE active", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO u(active) VALUES(FALSE),(NULL)", 2)))
		run(fixture.Exec("INSERT INTO u DEFAULT VALUES", 1))
		run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO u DEFAULT VALUES", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists."))))
		run(fixture.Select("SELECT id,k,active FROM u ORDER BY id", []string{"id", "k", "active"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(7), false}, {int64(2), int64(7), nil}, {int64(3), int64(7), true}}))
		run(fixture.Exec("CREATE TABLE e(k INT,n INT)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pe ON e(k) WHERE 10/n>0", 0))
		run(fixture.ImplicitError("INSERT INTO e VALUES(NULL,0)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
		run(fixture.Select("SELECT k,n FROM e", []string{"k", "n"}, []string{"INT4", "INT4"}, nil))
	})
}

func TestReferencePartialIndexUniqueGeneratedMembership(t *testing.T) {
	partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE u(id INT,k INT,n INT,active BOOL GENERATED ALWAYS AS (n>0) STORED)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pu ON u(k) WHERE active", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO u(id,k,n) VALUES(1,7,-1),(2,7,NULL),(3,7,1)", 3)))
		run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO u(id,k,n) VALUES(4,8,1),(5,8,-1),(6,8,2)", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(8) already exists."))))
		run(fixture.ErrorDiagnostic("UPDATE u SET n=1 WHERE id=1", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")))
		run(fixture.Select("SELECT id,k,n,active FROM u ORDER BY id", []string{"id", "k", "n", "active"}, []string{"INT4", "INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(7), int64(-1), false}, {int64(2), int64(7), nil, nil}, {int64(3), int64(7), int64(1), true}}))
	})
}

func TestReferencePartialIndexUniqueBuildIncludeAtomicity(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE u(id INT,k INT,active BOOL)", 0))
				run(fixture.Exec("INSERT INTO u VALUES(1,7,TRUE),(2,7,TRUE),(3,7,FALSE),(4,7,NULL)", 4))
				before := ""
				if !b.PG {
					before = partialIndexDump(t, p)
				}
				q := "CREATE UNIQUE INDEX pu ON u(k DESC) INCLUDE(id) WHERE active"
				s := fixture.ErrorDiagnostic(q, uniqueIndexReferenceBuildDiagnostic("u", "pu", "Key (k)=(7) is duplicated."))
				s.ErrorStage = "execute"
				if held {
					s = fixture.Prepared(s)
				}
				run(s)
				if !b.PG && before != partialIndexDump(t, p) {
					t.Fatal("build failure changed Dump")
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pu'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Select("SELECT id,k,active FROM u ORDER BY id", []string{"id", "k", "active"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(7), true}, {int64(2), int64(7), true}, {int64(3), int64(7), false}, {int64(4), int64(7), nil}}))
				run(fixture.Exec("DELETE FROM u WHERE id=2", 1))
				s = fixture.Exec(q, 0)
				if held {
					s = fixture.Prepared(s)
				}
				run(s)
				run(fixture.ErrorDiagnostic("INSERT INTO u VALUES(5,7,TRUE)", fixture.UniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")))
			})
		})
	}
}
