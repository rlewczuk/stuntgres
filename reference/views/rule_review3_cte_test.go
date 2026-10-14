package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ruleReview3CTERoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
	t.Helper()
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, held) })
			}
		})
	}
}

func ruleReview3CTEHeld(t *testing.T, ctx context.Context, conn *sql.Conn, q string, held bool) (fixture.Executor, func()) {
	t.Helper()
	if !held {
		return conn, func() {}
	}
	stmt, err := conn.PrepareContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	return fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, func() { stmt.Close() }
}

func TestReferenceRuleReview3CTEReuse(t *testing.T) {
	ruleReview3CTERoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("CREATE TABLE log(id INT)", 0),
			fixture.Exec("CREATE RULE r AS ON INSERT TO src DO WITH moved AS (INSERT INTO sink(id,v) VALUES(1,10) ON CONFLICT(id) DO UPDATE SET v=sink.v+1 RETURNING id) INSERT INTO log SELECT NEW.id FROM moved", 0))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "INSERT INTO src VALUES($1)"
		x, closeStmt := ruleReview3CTEHeld(t, p.Ctx, conn, q, held)
		defer closeStmt()
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 1))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 2))
		run(ruleInts("SELECT id,v FROM sink", []any{int64(1), int64(11)}),
			fixture.Select("SELECT id FROM src ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			fixture.Select("SELECT id FROM log ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	})
}

func TestReferenceRuleReview3CTERecovery(t *testing.T) {
	ruleReview3CTERoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("CREATE TABLE log(id INT)", 0),
			fixture.Exec("CREATE TABLE input(divisor INT)", 0), fixture.Exec("INSERT INTO input VALUES(10)", 1),
			fixture.Exec("CREATE RULE r AS ON INSERT TO src DO WITH moved AS (INSERT INTO sink(id,v) VALUES(1,10) ON CONFLICT(id) DO UPDATE SET v=sink.v+100/(SELECT divisor FROM input) RETURNING id) INSERT INTO log SELECT NEW.id FROM moved", 0))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "INSERT INTO src VALUES($1)"
		x, closeStmt := ruleReview3CTEHeld(t, p.Ctx, conn, q, held)
		defer closeStmt()
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 1))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 2))
		run(fixture.Exec("UPDATE input SET divisor=0", 1))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Error(q, "22012", 3))
		check := func(ids [][]any, value int64) {
			run(ruleInts("SELECT id,v FROM sink", []any{int64(1), value}),
				fixture.Select("SELECT id FROM src ORDER BY id", []string{"id"}, []string{"INT4"}, ids),
				fixture.Select("SELECT id FROM log ORDER BY id", []string{"id"}, []string{"INT4"}, ids))
		}
		check([][]any{{int64(1)}, {int64(2)}}, 20)
		run(fixture.Exec("UPDATE input SET divisor=10", 1))
		// The identical persistent rule and held object recover after atomic failure.
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 4))
		check([][]any{{int64(1)}, {int64(2)}, {int64(4)}}, 30)
	})
}

func TestReferenceRuleReview3CTESessions(t *testing.T) {
	for _, separateKeys := range []bool{false, true} {
		t.Run(fmt.Sprintf("separateKeys=%v", separateKeys), func(t *testing.T) {
			ruleReview3CTERoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
				values := "(1,0)"
				keyCount := int64(1)
				if separateKeys {
					values += ",(2,0)"
					keyCount = 2
				}
				run(fixture.Exec("CREATE TABLE src(id INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("CREATE TABLE log(id INT)", 0),
					fixture.Exec("INSERT INTO sink VALUES"+values, keyCount),
					fixture.Exec("CREATE RULE r AS ON INSERT TO src DO WITH moved AS (INSERT INTO sink(id,v) VALUES"+values+" ON CONFLICT(id) DO UPDATE SET v=sink.v+1 RETURNING id) INSERT INTO log SELECT NEW.id FROM moved", 0))
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				q := "INSERT INTO src VALUES($1)"
				var xs [2]fixture.Executor
				for i := range xs {
					conn, err := b.DB.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					x, closeStmt := ruleReview3CTEHeld(t, ctx, conn, q, held)
					xs[i] = x
					defer closeStmt()
				}
				type outcome struct {
					count int64
					err   error
				}
				start := make(chan struct{})
				done := make(chan outcome, 2)
				for i := range xs {
					go func(i int) {
						<-start
						var o outcome
						for n := range 4 {
							result, err := xs[i].ExecContext(ctx, q, i*4+n+1)
							if err != nil {
								o.err = err
								break
							}
							count, err := result.RowsAffected()
							if err != nil {
								o.err = err
								break
							}
							o.count += count
						}
						done <- o
					}(i)
				}
				close(start)
				var outcomes [2]outcome
				for i := range outcomes {
					select {
					case outcomes[i] = <-done:
					case <-ctx.Done():
						cancel()
						outcomes[i] = <-done
					}
				}
				for _, o := range outcomes {
					if o.err != nil {
						t.Fatal(o.err)
					}
					if o.count != 4 {
						t.Fatalf("original affected count=%d, want 4", o.count)
					}
				}
				wantSink := [][]any{{int64(1), int64(8)}}
				if separateKeys {
					wantSink = append(wantSink, []any{int64(2), int64(8)})
				}
				var wantSrc, wantLog [][]any
				for id := int64(1); id <= 8; id++ {
					wantSrc = append(wantSrc, []any{id})
					wantLog = append(wantLog, []any{id})
					if separateKeys {
						wantLog = append(wantLog, []any{id})
					}
				}
				run(fixture.Select("SELECT id,v FROM sink ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, wantSink),
					fixture.Select("SELECT id FROM src ORDER BY id", []string{"id"}, []string{"INT4"}, wantSrc),
					fixture.Select("SELECT id FROM log ORDER BY id", []string{"id"}, []string{"INT4"}, wantLog))
			})
		})
	}
}

func TestReferenceRuleReview3CTERestore(t *testing.T) {
	ruleReview3CTERoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("CREATE TABLE log(id INT)", 0),
			fixture.Exec("CREATE RULE r AS ON INSERT TO src DO WITH moved AS (INSERT INTO sink(id,v) VALUES(1,10) ON CONFLICT(id) DO UPDATE SET v=sink.v+1 RETURNING id) INSERT INTO log SELECT NEW.id FROM moved", 0))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "INSERT INTO src VALUES($1)"
		x, closeStmt := ruleReview3CTEHeld(t, p.Ctx, conn, q, held)
		defer closeStmt()
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 1))
		run(fixture.Exec("ALTER TABLE sink RENAME TO renamed_sink", 0), fixture.Exec("ALTER TABLE renamed_sink RENAME COLUMN id TO key", 0), fixture.Exec("ALTER TABLE renamed_sink RENAME COLUMN v TO value", 0))
		if !b.PG {
			// Actual native control: PostgreSQL has no Dump/Restore API substitute.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 2))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 3))
		run(fixture.Select("SELECT key,value FROM renamed_sink", []string{"key", "value"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(12)}}),
			fixture.Select("SELECT id FROM src ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}),
			fixture.Select("SELECT id FROM log ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
	})
}

func TestReferenceRuleReview3CTECardinality(t *testing.T) {
	ruleReview3CTERoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("CREATE TABLE log(id INT)", 0),
			fixture.Exec("CREATE RULE r AS ON INSERT TO src DO WITH moved AS (INSERT INTO sink(id,v) VALUES(1,10),(1,20) ON CONFLICT(id) DO UPDATE SET v=sink.v+1 RETURNING id) INSERT INTO log SELECT NEW.id FROM moved", 0))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "INSERT INTO src VALUES($1)"
		x, closeStmt := ruleReview3CTEHeld(t, p.Ctx, conn, q, held)
		defer closeStmt()
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Error(q, "21000", 1))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Error(q, "21000", 2))
		run(ruleInts("SELECT id,v FROM sink"), fixture.Select("SELECT id FROM src", []string{"id"}, []string{"INT4"}, nil), fixture.Select("SELECT id FROM log", []string{"id"}, []string{"INT4"}, nil))
	})
}
