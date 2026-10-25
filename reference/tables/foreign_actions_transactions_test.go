package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsTransactions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(id INT PRIMARY KEY CONSTRAINT c_fk REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE)", "CREATE TABLE g(cid INT CONSTRAINT deep_fk REFERENCES c ON DELETE RESTRICT)"} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
				run(fixture.Exec("INSERT INTO c VALUES(1),(2)", 2))
				run(fixture.Exec("INSERT INTO g VALUES(2)", 1))
				d := foreignActionsRestrict("c", "g", "deep_fk", `Key (id)=(2) is referenced from table "g".`)
				run(fixture.ForeignKeyFail("DELETE FROM p WHERE id=2", d, prepared))
				run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
				step(foreignActionsExec("UPDATE p SET id=11 WHERE id=1", 1, prepared))
				step(fixture.Exec("SAVEPOINT keep", 0))
				step(fixture.ForeignKeyFail("DELETE FROM p WHERE id=2", d, prepared))
				step(fixture.ForeignKeyFail("SELECT id FROM p", fixture.TransactionAborted(), false))
				step(fixture.Exec("ROLLBACK TO keep", 0))
				for _, table := range []string{"p", "c"} {
					step(foreignKeyIntRows("SELECT id FROM "+table+" ORDER BY id", "id", 2, 11))
				}
				step(foreignActionsExec("DELETE FROM p WHERE id=11", 1, prepared))
				step(fixture.Exec("ROLLBACK TO keep", 0))
				step(foreignKeyIntRows("SELECT id FROM c ORDER BY id", "id", 2, 11))
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				for _, table := range []string{"p", "c"} {
					run(foreignKeyIntRows("SELECT id FROM "+table+" ORDER BY id", "id", 2, 11))
				}
				run(foreignKeyIntRows("SELECT cid FROM g", "cid", 2))
			})
		})
	}
}

func TestReferenceForeignActionsProducers(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, producer := range []string{"merge", "conflict", "from", "using", "cursor"} {
			t.Run(fmt.Sprintf("%s/%t", producer, prepared), func(t *testing.T) {
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY,tag INT UNIQUE,note TEXT)", 0))
					run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE,note TEXT)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1,10,'parent')", 1))
					run(fixture.Exec("INSERT INTO c VALUES(1,'child'),(1,'second')", 2))
					q := ""
					switch producer {
					case "merge":
						q = "MERGE INTO p USING (SELECT 10 AS tag,3 AS id) s ON p.tag=s.tag WHEN MATCHED THEN UPDATE SET id=s.id"
					case "conflict":
						q = "INSERT INTO p VALUES(3,10,'new') ON CONFLICT(tag) DO UPDATE SET id=excluded.id"
					case "from":
						q = "UPDATE p SET id=s.id FROM (SELECT 10 AS tag,3 AS id) s WHERE p.tag=s.tag"
					case "using":
						q = "DELETE FROM p USING c WHERE p.id=c.pid"
					case "cursor":
						tx, err := b.DB.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
						step(fixture.Exec("DECLARE target NO SCROLL CURSOR FOR SELECT id FROM p FOR UPDATE", 0))
						step(foreignKeyIntRows("FETCH NEXT FROM target", "id", 1))
						step(foreignActionsExec("DELETE FROM p WHERE CURRENT OF target", 1, prepared))
						step(foreignKeyIntRows("SELECT pid FROM c", "pid"))
						if err = tx.Commit(); err != nil {
							t.Fatal(err)
						}
					}
					if q != "" {
						count := int64(1)
						// lib/pq's incumbent MERGE command-tag adapter reports0;
						// independent child rows below still require the real update.
						if b.PG && producer == "merge" {
							count = 0
						}
						run(foreignActionsExec(q, count, prepared))
					}
					var values [][]any
					if producer != "using" && producer != "cursor" {
						values = [][]any{{int64(3), "child"}, {int64(3), "second"}}
					}
					run(fixture.Select("SELECT pid,note FROM c ORDER BY note", []string{"pid", "note"}, []string{"INT4", "TEXT"}, values))
				})
			})
		}
	}
}

func TestReferenceForeignActionsProducersAtomicity(t *testing.T) {
	for _, q := range []string{
		"MERGE INTO p USING (SELECT 10 AS tag,3 AS id) s ON p.tag=s.tag WHEN MATCHED THEN UPDATE SET id=s.id",
		"INSERT INTO p VALUES(3,10) ON CONFLICT(tag) DO UPDATE SET id=excluded.id",
		"UPDATE p SET id=s.id FROM (SELECT 10 AS tag,3 AS id) s WHERE p.tag=s.tag",
		"DELETE FROM p USING c WHERE p.id=c.pid",
	} {
		foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY,tag INT UNIQUE)", 0))
			run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p ON UPDATE SET NULL ON DELETE SET NULL NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1,10)", 1))
			run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
			direction := "UPDATE"
			if q[0] == 'D' {
				direction = "DELETE"
			}
			d := fixture.ForeignKeyError("23502", `null value in column "pid" of relation "c" violates not-null constraint`, "Failing row contains (null).", "c", "")
			d.Column = "pid"
			d = foreignActionsContext(d, foreignActionsSQL("c", "pid", "SET NULL", direction))
			for _, prepared := range []bool{false, true} {
				run(fixture.ForeignKeyFail(q, d, prepared))
			}
			run(foreignKeyIntRows("SELECT id FROM p", "id", 1))
			run(foreignKeyIntRows("SELECT pid FROM c", "pid", 1))
		})
	}
}
