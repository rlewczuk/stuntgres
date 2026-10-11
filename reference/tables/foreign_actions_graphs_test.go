package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsGraphsSelf(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE self(a INT PRIMARY KEY,b INT CONSTRAINT self_fk REFERENCES self(a) ON UPDATE CASCADE ON DELETE CASCADE)", 0))
				run(fixture.Exec("INSERT INTO self VALUES(0,0),(1,1)", 2))
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
				step(foreignActionsExec("UPDATE self SET a=123 WHERE a=0", 1, prepared))
				step(foreignActionsExec("UPDATE self SET a=456 WHERE a=123", 1, prepared))
				step(fixture.Select("SELECT a,b FROM self ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(456), int64(456)}}))
				step(foreignActionsExec("INSERT INTO self VALUES(7,7)", 1, prepared))
				step(foreignActionsExec("UPDATE self SET a=8 WHERE a=7", 1, prepared))
				step(foreignKeyIntRows("SELECT b FROM self WHERE a=8", "b", 8))
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				run(foreignActionsExec("DELETE FROM self", 3, prepared))
				run(foreignKeyIntRows("SELECT a FROM self", "a"))
				run(fixture.Exec("INSERT INTO self VALUES(10,11),(11,10)", 2))
				run(foreignActionsExec("DELETE FROM self WHERE a=10", 1, prepared))
				run(foreignKeyIntRows("SELECT a FROM self", "a"))
			})
		})
	}
	for _, action := range []string{"NO ACTION", "RESTRICT"} {
		foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE self(id INT PRIMARY KEY,pid INT REFERENCES self ON DELETE "+action+")", 0))
			run(fixture.Exec("INSERT INTO self VALUES(1,1),(2,1)", 2))
			run(fixture.Exec("DELETE FROM self", 2))
			run(foreignKeyIntRows("SELECT id FROM self", "id"))
		})
	}
}

func TestReferenceForeignActionsGraphsChain(t *testing.T) {
	for _, direction := range []string{"UPDATE", "DELETE"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", direction, prepared), func(t *testing.T) {
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(id INT PRIMARY KEY CONSTRAINT c_fk REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE)", "CREATE TABLE g(id INT PRIMARY KEY CONSTRAINT g_fk REFERENCES c ON UPDATE CASCADE ON DELETE CASCADE)"} {
						run(fixture.Exec(q, 0))
					}
					for _, table := range []string{"p", "c", "g"} {
						run(fixture.Exec("INSERT INTO "+table+" VALUES(1),(2)", 2))
					}
					q := "DELETE FROM p"
					values := []int64(nil)
					if direction == "UPDATE" {
						q = "UPDATE p SET id=id+10"
						values = []int64{11, 12}
					}
					run(foreignActionsExec(q, 2, prepared))
					for _, table := range []string{"p", "c", "g"} {
						run(foreignKeyIntRows("SELECT id FROM "+table+" ORDER BY id", "id", values...))
					}
				})
			})
		}
	}
}

func TestReferenceForeignActionsGraphsDiamond(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			for _, q := range []string{
				"CREATE TABLE p(id INT PRIMARY KEY)",
				"CREATE TABLE a(id INT PRIMARY KEY CONSTRAINT a_fk REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE)",
				"CREATE TABLE b(id INT PRIMARY KEY CONSTRAINT b_fk REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE)",
				"CREATE TABLE g(id INT PRIMARY KEY,aid INT CONSTRAINT ga_fk REFERENCES a ON UPDATE CASCADE ON DELETE CASCADE,bid INT CONSTRAINT gb_fk REFERENCES b ON UPDATE CASCADE ON DELETE CASCADE,note TEXT)",
				"CREATE TABLE multiple(id INT PRIMARY KEY,x INT CONSTRAINT x_fk REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE,y INT CONSTRAINT y_fk REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE)",
			} {
				run(fixture.Exec(q, 0))
			}
			for _, table := range []string{"p", "a", "b"} {
				run(fixture.Exec("INSERT INTO "+table+" VALUES(1),(2)", 2))
			}
			run(fixture.Exec("INSERT INTO g VALUES(10,1,1,'both'),(20,2,2,'other')", 2))
			run(fixture.Exec("INSERT INTO multiple VALUES(10,1,1),(20,2,2)", 2))
			run(foreignActionsExec("UPDATE p SET id=3 WHERE id=1", 1, prepared))
			run(fixture.Select("SELECT id,aid,bid,note FROM g ORDER BY id", []string{"id", "aid", "bid", "note"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(10), int64(3), int64(3), "both"}, {int64(20), int64(2), int64(2), "other"}}))
			run(fixture.Select("SELECT id,x,y FROM multiple ORDER BY id", []string{"id", "x", "y"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(3), int64(3)}, {int64(20), int64(2), int64(2)}}))
			run(foreignActionsExec("DELETE FROM p WHERE id=3", 1, prepared))
			run(foreignKeyIntRows("SELECT id FROM g", "id", 20))
			run(foreignKeyIntRows("SELECT id FROM multiple", "id", 20))
			for _, table := range []string{"p", "a", "b"} {
				run(foreignKeyIntRows("SELECT id FROM "+table, "id", 2))
			}
		})
	}
}

func TestReferenceForeignActionsAtomicity(t *testing.T) {
	for _, direction := range []string{"DELETE", "UPDATE"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", direction, prepared), func(t *testing.T) {
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(id INT PRIMARY KEY CONSTRAINT c_fk REFERENCES p ON DELETE CASCADE ON UPDATE CASCADE)", "CREATE TABLE early(id INT CONSTRAINT early_fk REFERENCES p ON DELETE CASCADE ON UPDATE CASCADE)", "CREATE TABLE g(cid INT CONSTRAINT deep_fk REFERENCES c ON DELETE RESTRICT ON UPDATE RESTRICT)"} {
						run(fixture.Exec(q, 0))
					}
					for _, table := range []string{"p", "c", "early"} {
						run(fixture.Exec("INSERT INTO "+table+" VALUES(1),(2)", 2))
					}
					run(fixture.Exec("INSERT INTO g VALUES(2)", 1))
					q := "DELETE FROM p"
					if direction == "UPDATE" {
						q = "UPDATE p SET id=id+10"
					}
					d := foreignActionsRestrict("c", "g", "deep_fk", `Key (id)=(2) is referenced from table "g".`)
					run(foreignKeyFail(q, d, prepared))
					s := foreignKeyFail(q+" RETURNING id", d, prepared)
					s.SelectRows = true
					run(s)
					for _, table := range []string{"p", "c", "early"} {
						run(foreignKeyIntRows("SELECT id FROM "+table+" ORDER BY id", "id", 1, 2))
					}
					run(foreignKeyIntRows("SELECT cid FROM g", "cid", 2))
				})
			})
		}
	}
}

func TestReferenceForeignActionsAtomicityMultirow(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(id INT,pid INT REFERENCES p ON DELETE SET NULL,CONSTRAINT ck CHECK(id=10 OR pid IS NOT NULL))", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
			run(fixture.Exec("INSERT INTO c VALUES(10,1),(20,2)", 2))
			d := foreignActionsContext(foreignKeyError("23514", `new row for relation "c" violates check constraint "ck"`, "Failing row contains (20, null).", "c", "ck"), foreignActionsSQL("c", "pid", "SET NULL", "DELETE"))
			run(foreignKeyFail("DELETE FROM p", d, prepared))
			run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 1, 2))
			run(fixture.Select("SELECT id,pid FROM c ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(1)}, {int64(20), int64(2)}}))
		})
	}
}
