package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewCheckLifecycle(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(a INT,b INT DEFAULT 10)", 0), fixture.Exec("CREATE VIEW l AS SELECT a,b FROM t WHERE a>0", 0), fixture.Exec("CREATE VIEW u AS SELECT a,b FROM l WHERE a<b WITH CASCADED CHECK OPTION", 0), fixture.Exec("INSERT INTO u(a) VALUES(3)", 1))
		q := "INSERT INTO u VALUES($1,$2)"
		var e fixture.Executor = b.DB
		if held {
			e = fixture.ViewHold(t, p, b, q)
		}
		write := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
		write(viewCheckError(q, "l", "-1, 10", -1, 10))
		run(fixture.Exec("ALTER VIEW u SET(check_option=local)", 0))
		write(fixture.Exec(q, 1, -2, 10))
		run(fixture.Exec("ALTER VIEW l SET(check_option=local)", 0))
		write(viewCheckError(q, "l", "-3, 10", -3, 10))
		run(fixture.Exec("CREATE OR REPLACE VIEW u AS SELECT a,b FROM l WHERE a<b", 0))
		write(fixture.Exec(q, 1, 20, 10))
		run(fixture.Exec("CREATE OR REPLACE VIEW u AS SELECT a,b FROM l WHERE a<b WITH CASCADED CHECK OPTION", 0))
		write(viewCheckError(q, "u", "21, 10", 21, 10))
		for cycle := range 2 {
			if cycle == 0 {
				run(fixture.Exec("ALTER VIEW u SET(check_option=local)", 0))
			} else {
				run(fixture.Exec("ALTER VIEW u SET(check_option=cascaded)", 0))
			}
			if !b.PG {
				dump := fixture.ViewDump(t, p)
				rows, err := b.DB.QueryContext(p.Ctx, "SELECT a,b FROM t ORDER BY a")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.ViewRestore(t, p, dump)
				want := [][]any{{int64(-2), int64(10)}, {int64(3), int64(10)}, {int64(20), int64(10)}}
				if cycle == 1 {
					want = [][]any{{int64(-2), int64(10)}, {int64(3), int64(10)}, {int64(5), int64(10)}, {int64(20), int64(10)}}
				}
				fixture.DeleteReferenceConsume(t, false, rows, nil, fixture.Select("", []string{"a", "b"}, []string{"INT4", "INT4"}, want))
				readFailure := errors.New("check restore reader failure")
				if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: readFailure}); !errors.Is(err, readFailure) {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(append(append([]byte{}, dump...), []byte("\nCREATE VIEW broken AS SELECT missing FROM t WITH CHECK OPTION;")...))); err == nil {
					t.Fatal("bad suffix accepted")
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				live, stop := context.WithCancel(p.Ctx)
				if err := stuntgres.Restore(live, p.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(dump), Cancel: stop}); !errors.Is(err, context.Canceled) {
					stop()
					t.Fatal(err)
				}
				stop()
				if !bytes.Equal(dump, fixture.ViewDump(t, p)) {
					t.Fatal("failed restore changed rows or mode")
				}
			}
			write(viewCheckError(q, "l", "-4, 10", -4, 10))
			write(viewCheckError(q, "u", "12, 10", 12, 10))
			write(fixture.Exec(q, 1, 5+cycle, 10))
		}
		run(viewCheckPairRows([]any{int64(-2), int64(10)}, []any{int64(3), int64(10)}, []any{int64(5), int64(10)}, []any{int64(6), int64(10)}, []any{int64(20), int64(10)}))
	})
}

func TestReferenceViewCheckReplay(t *testing.T) {
	for _, held := range []bool{false, true} {
		p := fixture.NewScenario(t)
		var emitted []byte
		for _, b := range p.Backends() {
			run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
			run(fixture.Exec("CREATE TABLE t(a INT,b INT DEFAULT 10)", 0), fixture.Exec("CREATE VIEW l AS SELECT a,b FROM t WHERE a>0", 0), fixture.Exec("CREATE VIEW u AS SELECT a,b FROM l WHERE a<b WITH CASCADED CHECK OPTION", 0), fixture.Exec("INSERT INTO u(a) VALUES(3)", 1))
			if !b.PG {
				emitted = fixture.ViewDump(t, p)
			} else if len(emitted) > 0 {
				run(fixture.Exec("DROP TABLE t CASCADE", 0))
				if _, err := b.DB.ExecContext(p.Ctx, string(emitted)); err != nil {
					t.Fatal("exact native Dump replay", err)
				}
			}
			q := "UPDATE u SET a=$1 RETURNING a,b"
			var e fixture.Executor = b.DB
			if held {
				e = fixture.ViewHold(t, p, b, q)
			}
			s := viewCheckError(q, "l", "-1, 10", -1)
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select(q, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(10)}}, 4))
			run(viewCheckPairRows([]any{int64(4), int64(10)}))
		}
	}
}

func TestReferenceViewCheckFeatureSmoke(t *testing.T) {
	viewCheckRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE t(a INT,b INT DEFAULT 10,secret TEXT DEFAULT 'kept')", 0), fixture.Exec("CREATE VIEW l AS SELECT a,b FROM t WHERE a>0", 0), fixture.Exec("CREATE VIEW u AS SELECT a,b FROM l WHERE a<b WITH CASCADED CHECK OPTION", 0), fixture.Exec("INSERT INTO u(a) VALUES(3)", 1))
		q := "UPDATE u SET a=$1 RETURNING a,b"
		var e fixture.Executor = b.DB
		if held {
			e = fixture.ViewHold(t, p, b, q)
		}
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select(q, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(10)}}, 4))
		for _, tc := range []struct {
			n    int
			name string
		}{{-1, "l"}, {11, "u"}} {
			s := viewCheckError(q, tc.name, map[int]string{-1: "-1, 10, kept", 11: "11, 10, kept"}[tc.n], tc.n)
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
			run(fixture.Select("SELECT a,b,secret FROM t", []string{"a", "b", "secret"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(4), int64(10), "kept"}}))
		}
		run(fixture.Exec("ALTER VIEW u SET(check_option=local)", 0), fixture.Exec("INSERT INTO u VALUES(-2,10)", 1), fixture.Exec("ALTER VIEW l SET(check_option=local)", 0), viewCheckError("INSERT INTO u VALUES(-3,10)", "l", "-3, 10, kept"))
		for cycle := range 2 {
			if cycle == 1 {
				run(fixture.Exec("ALTER VIEW u SET(check_option=cascaded)", 0))
			}
			if !b.PG {
				dump := fixture.ViewDump(t, p)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.ViewRestore(t, p, dump)
			}
			run(viewCheckError("INSERT INTO u VALUES(-4,10)", "l", "-4, 10, kept"), viewCheckError("INSERT INTO u VALUES(12,10)", "u", "12, 10, kept"), fixture.Exec("INSERT INTO u(a) VALUES($1)", 1, 5+cycle))
		}
		run(fixture.Select("SELECT a,b,secret FROM t ORDER BY a", []string{"a", "b", "secret"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(-2), int64(10), "kept"}, {int64(4), int64(10), "kept"}, {int64(5), int64(10), "kept"}, {int64(6), int64(10), "kept"}}))
	})
}
