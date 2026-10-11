package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsLifecycle(t *testing.T) {
	for _, action := range []string{"NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"} {
		for _, direction := range []string{"UPDATE", "DELETE"} {
			t.Run(direction+"/"+action, func(t *testing.T) {
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id INT)", 0))
					run(fixture.Exec("CREATE UNIQUE INDEX supporting ON p(id)", 0))
					run(fixture.Exec("CREATE TABLE c(pid INT DEFAULT 1 CONSTRAINT fk REFERENCES p(id) ON "+direction+" "+action+")", 0))
					run(fixture.Exec("CREATE TABLE empty(pid INT REFERENCES p(id) ON UPDATE CASCADE ON DELETE SET NULL)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
					run(fixture.Exec("INSERT INTO c VALUES(2)", 1))
					if !b.PG {
						foreignKeyRoundtrip(t, p)
					}
					def := "FOREIGN KEY (pid) REFERENCES p(id)"
					if action != "NO ACTION" {
						def += " ON " + direction + " " + action
					}
					run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{def}}))
					q := "DELETE FROM p WHERE id=2"
					if direction == "UPDATE" {
						q = "UPDATE p SET id=3 WHERE id=2"
					}
					if action == "NO ACTION" || action == "RESTRICT" {
						d := foreignKeyParent("p", "c", "fk", `Key (id)=(2) is still referenced from table "c".`)
						if action == "RESTRICT" {
							d = foreignActionsRestrict("p", "c", "fk", `Key (id)=(2) is referenced from table "c".`)
						}
						run(foreignKeyFail(q, d, true))
						run(foreignKeyIntRows("SELECT pid FROM c", "pid", 2))
					} else {
						run(fixture.Prepared(fixture.Exec(q, 1)))
						var rows [][]any
						var v any = nil
						if action == "SET DEFAULT" {
							v = int64(1)
						}
						if action == "CASCADE" && direction == "UPDATE" {
							v = int64(3)
						}
						if action != "CASCADE" || direction != "DELETE" {
							rows = [][]any{{v}}
						}
						run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, rows))
					}
					// DDL CASCADE removes the FK definition, never referencing data.
					d := foreignKeyError("2BP01", "cannot drop index supporting because other objects depend on it", "constraint fk on table c depends on index supporting\nconstraint empty_pid_fkey on table empty depends on index supporting", "", "")
					d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					run(foreignKeyFail("DROP INDEX supporting RESTRICT", d, false))
					run(fixture.Exec("DROP INDEX supporting CASCADE", 0))
					run(fixture.Exec("INSERT INTO c VALUES(99)", 1))
					run(foreignKeyIntRows("SELECT pid FROM c WHERE pid=99", "pid", 99))
				})
			})
		}
	}
}

func TestReferenceForeignActionsLifecycleLists(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "fa_lifecycle_moved")
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(tenant INT,author INT,PRIMARY KEY(tenant,author))", 0))
		run(fixture.Exec("CREATE TABLE c(tenant INT,author INT,note TEXT,CONSTRAINT fk FOREIGN KEY(tenant,author) REFERENCES p ON UPDATE CASCADE ON DELETE SET NULL (author,author))", 0))
		run(fixture.Exec("INSERT INTO p VALUES(7,2),(8,2)", 2))
		run(fixture.Exec("INSERT INTO c VALUES(7,2,'changed'),(8,2,'other')", 2))
		for _, q := range []string{"ALTER TABLE c RENAME author TO renamed", "ALTER TABLE c RENAME TO moved_child", "CREATE SCHEMA fa_lifecycle_moved", "ALTER TABLE moved_child SET SCHEMA fa_lifecycle_moved", "ALTER TABLE fa_lifecycle_moved.moved_child ALTER renamed TYPE BIGINT"} {
			run(fixture.Exec(q, 0))
		}
		if !b.PG {
			foreignKeyRoundtrip(t, p)
		}
		run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='fk'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"FOREIGN KEY (tenant, renamed) REFERENCES p(tenant, author) ON UPDATE CASCADE ON DELETE SET NULL (renamed)"}}))
		run(fixture.Prepared(fixture.Exec("DELETE FROM p WHERE tenant=7", 1)))
		run(fixture.Select("SELECT tenant,renamed,note FROM fa_lifecycle_moved.moved_child ORDER BY tenant", []string{"tenant", "renamed", "note"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(7), nil, "changed"}, {int64(8), int64(2), "other"}}))
		run(fixture.Exec("UPDATE p SET author=3 WHERE tenant=8", 1))
		run(fixture.Select("SELECT tenant,renamed FROM fa_lifecycle_moved.moved_child ORDER BY tenant", []string{"tenant", "renamed"}, []string{"INT4", "INT8"}, [][]any{{int64(7), nil}, {int64(8), int64(3)}}))
		run(fixture.Exec("ALTER TABLE p DROP COLUMN author CASCADE", 0))
		run(fixture.Select("SELECT tenant,renamed FROM fa_lifecycle_moved.moved_child ORDER BY tenant", []string{"tenant", "renamed"}, []string{"INT4", "INT8"}, [][]any{{int64(7), nil}, {int64(8), int64(3)}}))
	})
}

func TestReferenceForeignActionsGraphsCycle(t *testing.T) {
	for _, direction := range []string{"DELETE", "UPDATE"} {
		t.Run(direction, func(t *testing.T) {
			foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				if b.PG {
					// Oracle fixture setup only: public native ALTER ADD remains excluded.
					for _, q := range []string{"CREATE TABLE a(id INT PRIMARY KEY,bid INT)", "CREATE TABLE b(id INT PRIMARY KEY,aid INT)", "INSERT INTO a VALUES(1,2)", "INSERT INTO b VALUES(2,1)", "ALTER TABLE a ADD CONSTRAINT a_fk FOREIGN KEY(bid) REFERENCES b ON DELETE CASCADE ON UPDATE CASCADE", "ALTER TABLE b ADD CONSTRAINT b_fk FOREIGN KEY(aid) REFERENCES a ON DELETE CASCADE ON UPDATE CASCADE"} {
						n := int64(0)
						if strings.HasPrefix(q, "INSERT") {
							n = 1
						}
						run(fixture.Exec(q, n))
					}
				} else {
					script := "CREATE TABLE a(id INT PRIMARY KEY,bid INT CONSTRAINT a_fk REFERENCES b ON DELETE CASCADE ON UPDATE CASCADE);CREATE TABLE b(id INT PRIMARY KEY,aid INT CONSTRAINT b_fk REFERENCES a ON DELETE CASCADE ON UPDATE CASCADE);INSERT INTO a VALUES(1,2);INSERT INTO b VALUES(2,1);"
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
						t.Fatal(err)
					}
					foreignKeyRoundtrip(t, p)
				}
				if direction == "UPDATE" {
					run(fixture.Prepared(fixture.Exec("UPDATE a SET id=3 WHERE id=1", 1)))
					run(fixture.Exec("UPDATE b SET id=4 WHERE id=2", 1))
					run(fixture.Select("SELECT id,bid FROM a", []string{"id", "bid"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(4)}}))
					run(fixture.Select("SELECT id,aid FROM b", []string{"id", "aid"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(3)}}))
				} else {
					run(fixture.Prepared(fixture.Exec("DELETE FROM a", 1)))
					run(foreignKeyIntRows("SELECT id FROM a", "id"))
					run(foreignKeyIntRows("SELECT id FROM b", "id"))
				}
			})
		})
	}
}

func TestReferenceForeignActionsLifecycleInvalidImport(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p ON DELETE CASCADE ON UPDATE CASCADE)", 0))
		run(fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
		if b.PG {
			// PostgreSQL's SQL import check is genuine, not a Restore API substitute.
			run(foreignKeyFail("INSERT INTO c VALUES(9)", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`), false))
			run(foreignKeyIntRows("SELECT v FROM sentinel", "v", 91))
			return
		}
		before := fixture.CheckDump(t, p)
		ids := map[string]int64{}
		for _, q := range []string{"SELECT 'p'::regclass::oid", "SELECT 'c'::regclass::oid", "SELECT 'sentinel'::regclass::oid", "SELECT oid::bigint FROM pg_constraint WHERE conname='fk'"} {
			var v int64
			if err := b.DB.QueryRowContext(p.Ctx, q).Scan(&v); err != nil || v <= 0 {
				t.Fatal(q, v, err)
			}
			ids[q] = v
		}
		for _, tc := range []struct {
			script     string
			diagnostic fixture.Diagnostic
		}{
			{"CREATE TABLE p(id INT PRIMARY KEY);CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p ON DELETE CASCADE);INSERT INTO c VALUES(9);", foreignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`)},
			{"CREATE TABLE p(id INT PRIMARY KEY);CREATE TABLE c(pid INT,note INT,CONSTRAINT fk FOREIGN KEY(pid) REFERENCES p ON DELETE SET NULL(note));", foreignKeyError("42P10", `column "note" referenced in ON DELETE SET action must be part of foreign key`, "", "", "")},
		} {
			held, err := b.DB.QueryContext(p.Ctx, "SELECT v FROM sentinel")
			if err != nil {
				t.Fatal(err)
			}
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(tc.script))
			fixture.AssertDiagnostic(t, err, &tc.diagnostic)
			fixture.TransactionValues(t, held, 91)
			held.Close()
			if after := fixture.CheckDump(t, p); !bytes.Equal(before, after) {
				t.Fatal("invalid action import changed target")
			}
			for q, want := range ids {
				var got int64
				if err = b.DB.QueryRowContext(p.Ctx, q).Scan(&got); err != nil || got != want {
					t.Fatal(q, got, want, err)
				}
			}
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(before)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		ioErr := errors.New("action import I/O failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, foreignKeyFailedReader{ioErr}); !errors.Is(err, ioErr) {
			t.Fatal(err)
		}
		if after := fixture.CheckDump(t, p); !bytes.Equal(before, after) {
			t.Fatal("failed action import changed target")
		}
		run(fixture.Exec("UPDATE p SET id=2", 1))
		run(foreignKeyIntRows("SELECT pid FROM c", "pid", 2))
	})
}

func TestReferenceForeignActionsTemporary(t *testing.T) {
	for _, action := range []string{"CASCADE", "SET DEFAULT"} {
		t.Run(action, func(t *testing.T) {
			foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
				for _, q := range []string{"CREATE TABLE persistent(id INT PRIMARY KEY)", "CREATE TEMP TABLE tp(id INT PRIMARY KEY)", "CREATE TEMP TABLE tc(pid INT DEFAULT 1 CONSTRAINT retained_action_fk REFERENCES tp ON UPDATE CASCADE ON DELETE " + action + ")"} {
					step(fixture.Exec(q, 0))
				}
				step(fixture.Exec("INSERT INTO persistent VALUES(7)", 1))
				step(fixture.Exec("INSERT INTO tp VALUES(1),(2)", 2))
				step(fixture.Exec("INSERT INTO tc VALUES(2)", 1))
				ids := map[string]int64{}
				for _, q := range []string{"SELECT 'tp'::regclass::oid", "SELECT 'tc'::regclass::oid", "SELECT oid::bigint FROM pg_constraint WHERE conname='retained_action_fk'"} {
					var v int64
					if err := conn.QueryRowContext(p.Ctx, q).Scan(&v); err != nil || v <= 0 {
						t.Fatal(q, v, err)
					}
					ids[q] = v
				}
				q := "UPDATE tp SET id=3 WHERE id=2"
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held, err := conn.QueryContext(p.Ctx, "SELECT pid FROM tc")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				if !b.PG {
					dump := fixture.CheckDump(t, p)
					if strings.Contains(string(dump), "retained_action_fk") {
						t.Fatal("TEMP graph leaked to Dump")
					}
					for _, replace := range []func() error{func() error { return stuntgres.Reset(p.Ctx, p.Native) }, func() error { return stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)) }} {
						if err = replace(); err != nil {
							t.Fatal(err)
						}
						for query, want := range ids {
							var got int64
							if err = conn.QueryRowContext(p.Ctx, query).Scan(&got); err != nil || got != want {
								t.Fatal("TEMP provenance", query, got, want, err)
							}
						}
					}
					if after := fixture.CheckDump(t, p); !bytes.Equal(dump, after) {
						t.Fatal("TEMP changed persistent dump")
					}
				}
				labels, err := held.Columns()
				if err != nil || len(labels) != 1 || labels[0] != "pid" {
					t.Fatal("held TEMP labels", labels, err)
				}
				types, err := held.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
					t.Fatal("held TEMP metadata", types, err)
				}
				fixture.TransactionRowValues(t, held, 2)
				held.Close()
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 1))
				step(foreignKeyIntRows("SELECT pid FROM tc", "pid", 3))
				step(fixture.Exec("DELETE FROM tp WHERE id=3", 1))
				values := []int64(nil)
				if action == "SET DEFAULT" {
					values = []int64{1}
				}
				step(foreignKeyIntRows("SELECT pid FROM tc", "pid", values...))
				step(fixture.Exec("DROP TABLE tc,tp", 0))
			})
		})
	}
}

func TestReferenceForeignActionsFeatureSmoke(t *testing.T) {
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY,note TEXT)", "CREATE TABLE c(id INT PRIMARY KEY CONSTRAINT cascade_fk REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE)", "CREATE TABLE g(cid INT CONSTRAINT restrict_fk REFERENCES c ON DELETE RESTRICT)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO p VALUES(1,'parent'),(2,'other')", 2))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		run(fixture.Prepared(fixture.Select("UPDATE p SET id=3 WHERE id=1 RETURNING id,note", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "parent"}})))
		run(foreignKeyIntRows("SELECT id FROM c", "id", 3))
		run(fixture.Exec("INSERT INTO g VALUES(3)", 1))
		if !b.PG {
			foreignKeyRoundtrip(t, p)
		}
		d := foreignActionsRestrict("c", "g", "restrict_fk", `Key (id)=(3) is referenced from table "g".`)
		run(foreignKeyFail("DELETE FROM p WHERE id=3", d, true))
		run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 2, 3))
		run(foreignKeyIntRows("SELECT id FROM c", "id", 3))
		run(foreignKeyIntRows("SELECT cid FROM g", "cid", 3))
		run(fixture.Exec("DELETE FROM g", 1))
		run(fixture.Exec("DELETE FROM p WHERE id=3", 1))
		run(foreignKeyIntRows("SELECT id FROM c", "id"))
	})
}
