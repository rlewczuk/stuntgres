package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewUpdatableLifecycle(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE t1(junk INT,id INT,a INT DEFAULT 7,hidden TEXT DEFAULT 'kept')", 0), fixture.Exec("CREATE TABLE t2(id INT,a INT DEFAULT 8)", 0), fixture.Exec("INSERT INTO t1 VALUES(0,1,10,'old')", 1), fixture.Exec("INSERT INTO t2 VALUES(1,20)", 1), fixture.Exec("CREATE VIEW v AS SELECT id AS key,a AS x FROM t1", 0),
		} {
			run(s)
		}
		h := fixture.ViewHold(t, p, b, "UPDATE v SET x=$1 WHERE key=1 RETURNING key,x")
		write := func(n int) {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(h.SQL, []string{"key", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(n)}}, n))
		}
		write(11)
		run(fixture.Exec("ALTER TABLE t1 DROP COLUMN junk", 0))
		run(fixture.Exec("ALTER TABLE t1 RENAME COLUMN a TO renamed", 0))
		run(fixture.Exec("ALTER TABLE t1 ADD COLUMN a INT DEFAULT 99", 0))
		run(fixture.Exec("ALTER TABLE t1 RENAME TO original", 0))
		run(fixture.Exec("CREATE TABLE t1(id INT,a INT)", 0))
		write(12)
		run(fixture.Select("SELECT id,renamed,hidden,a FROM original", []string{"id", "renamed", "hidden", "a"}, []string{"INT4", "INT4", "TEXT", "INT4"}, [][]any{{int64(1), int64(12), "old", int64(99)}}))
		run(fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT id AS key,a AS x FROM t2", 0))
		write(21)
		run(fixture.Select("SELECT id,a FROM t2", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(21)}}))
		run(fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT id AS key,a+1 AS x FROM t2", 0))
		s := viewWriteComputed(h.SQL, "UPDATE", "v", "x")
		s.Args = []any{22}
		fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		run(fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT id AS key,a AS x FROM t2", 0))
		run(fixture.Exec("ALTER TABLE t2 ALTER COLUMN a SET DEFAULT 30", 0))
		run(fixture.Exec("INSERT INTO v(key) VALUES(2)", 1))
		run(fixture.Select("SELECT id,a FROM t2 ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(21)}, {int64(2), int64(30)}}))
	})
}
func TestReferenceViewUpdatableSmoke(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE smoke_base(id INT DEFAULT 9,a INT,b INT,hidden TEXT DEFAULT 'base')", 0), fixture.Exec("CREATE VIEW smoke_view AS SELECT b AS y,id AS key,a AS x FROM smoke_base WHERE id>0", 0), fixture.Exec("CREATE VIEW nested AS SELECT key,x,y FROM smoke_view WHERE x>0", 0),
			fixture.Exec("INSERT INTO smoke_view(x,y) VALUES(10,20)", 1),
			fixture.Select("UPDATE smoke_view SET x=y,y=x WHERE key=$1 RETURNING key,x,y", []string{"key", "x", "y"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(20), int64(10)}}, 9), fixture.Exec("DELETE FROM smoke_view WHERE key=9", 1),
			fixture.Exec("CREATE VIEW smoke_mixed AS SELECT id,a,a+1 AS calc FROM smoke_base", 0), viewWriteComputed("UPDATE smoke_mixed SET calc=1", "UPDATE", "smoke_mixed", "calc"), fixture.Exec("INSERT INTO smoke_view(x,y) VALUES(7,8)", 1),
		} {
			run(s)
		}
		for cycle := range 2 {
			if !b.PG {
				dump := fixture.ViewDump(t, p)
				retained, err := b.DB.QueryContext(p.Ctx, "SELECT key,x,y FROM nested")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.ViewRestore(t, p, dump)
				fixture.DeleteReferenceConsume(t, false, retained, nil, fixture.Select("", []string{"key", "x", "y"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(7 + cycle*4), int64(8)}}))
			}
			fixture.ViewCopy(t, p, b, "nested", "replayed")
			run(fixture.Select("SELECT key,x,y FROM replayed", []string{"key", "x", "y"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(7 + cycle*4), int64(8)}}))
			run(fixture.Exec("DROP VIEW replayed", 0))
			run(fixture.Exec("UPDATE nested SET x=x+4 WHERE key=9", 1))
			run(fixture.Select("SELECT id,a,b,hidden FROM smoke_base", []string{"id", "a", "b", "hidden"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(9), int64(11 + cycle*4), int64(8), "base"}}))
		}
		if !b.PG {
			dump := fixture.ViewDump(t, p)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(append(append([]byte{}, dump...), []byte("\nCREATE VIEW broken AS SELECT missing FROM smoke_base;")...))); err == nil {
				t.Fatal("invalid suffix Restore succeeded")
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			readFailure := errors.New("view restore reader failure")
			if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: readFailure}); !errors.Is(err, readFailure) {
				t.Fatal(err)
			}
			live, stop := context.WithCancel(p.Ctx)
			if err := stuntgres.Restore(live, p.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(dump), Cancel: stop}); !errors.Is(err, context.Canceled) {
				stop()
				t.Fatal(err)
			}
			stop()
			if !bytes.Equal(fixture.ViewDump(t, p), dump) {
				t.Fatal("failed Restore changed committed definitions/data")
			}
			run(fixture.Select("SELECT id,a,b,hidden FROM smoke_base", []string{"id", "a", "b", "hidden"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(9), int64(15), int64(8), "base"}}))
		}
		run(fixture.Exec("DELETE FROM nested WHERE key=9", 1))
		run(fixture.Select("SELECT id,a,b,hidden FROM smoke_base", []string{"id", "a", "b", "hidden"}, []string{"INT4", "INT4", "INT4", "TEXT"}, nil))
	})
}
