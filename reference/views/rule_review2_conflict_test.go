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

func ruleReview2ConflictRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
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

func ruleReview2ConflictHeld(t *testing.T, ctx context.Context, conn *sql.Conn, q string, held bool) (fixture.Executor, func()) {
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

func TestReferenceRuleReview2ConflictReuse(t *testing.T) {
	ruleReview2ConflictRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,v INT)", 0),
			fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v) ON CONFLICT(id) DO UPDATE SET v=sink.v+100/EXCLUDED.v", 0))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "INSERT INTO src VALUES($1,$2)"
		x, closeStmt := ruleReview2ConflictHeld(t, p.Ctx, conn, q, held)
		defer closeStmt()
		for _, v := range []int{10, 20, 25} {
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 1, v))
		}
		run(ruleInts("SELECT id,v FROM sink", []any{int64(1), int64(19)}))
		failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, 1, 0)
		fixture.RunStep(t, p.Ctx, x, b.Recorder, failure)
		run(ruleInts("SELECT id,v FROM src ORDER BY v", []any{int64(1), int64(10)}, []any{int64(1), int64(20)}, []any{int64(1), int64(25)}), ruleInts("SELECT id,v FROM sink", []any{int64(1), int64(19)}))
		// Reuse the SAME successful and failing consumer after rollback of its command.
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 1, 10))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 2, 30))
		run(ruleInts("SELECT id,v FROM src ORDER BY id,v", []any{int64(1), int64(10)}, []any{int64(1), int64(10)}, []any{int64(1), int64(20)}, []any{int64(1), int64(25)}, []any{int64(2), int64(30)}), ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(29)}, []any{int64(2), int64(30)}))
	})
}

func TestReferenceRuleReview2ConflictSessions(t *testing.T) {
	for _, sameKey := range []bool{false, true} {
		t.Run(fmt.Sprintf("sameKey=%v", sameKey), func(t *testing.T) {
			ruleReview2ConflictRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
				run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("INSERT INTO sink VALUES(1,0),(2,0)", 2), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v) ON CONFLICT(id) DO UPDATE SET v=sink.v+EXCLUDED.v", 0))
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				q := "INSERT INTO src VALUES($1,$2)"
				var conns [2]*sql.Conn
				var xs [2]fixture.Executor
				for i := range conns {
					conn, err := b.DB.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					conns[i] = conn
					defer conn.Close()
					x, closeStmt := ruleReview2ConflictHeld(t, ctx, conn, q, held)
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
						id := i + 1
						if sameKey {
							id = 1
						}
						var o outcome
						for range 4 {
							result, err := xs[i].ExecContext(ctx, q, id, 1)
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
				// Always cancel and join both workers before asserting or returning handles.
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
						t.Fatalf("command count=%d, want 4", o.count)
					}
				}
				if sameKey {
					run(ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(8)}, []any{int64(2), int64(0)}))
				} else {
					run(ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(4)}, []any{int64(2), int64(4)}))
				}
				want := make([][]any, 0, 8)
				for id := 1; id <= 2; id++ {
					n := 4
					if sameKey {
						if id == 2 {
							break
						}
						n = 8
					}
					for range n {
						want = append(want, []any{int64(id), int64(1)})
					}
				}
				run(fixture.Select("SELECT id,v FROM src ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, want))
			})
		})
	}
}

func TestReferenceRuleReview2ConflictLayout(t *testing.T) {
	for _, sameWidth := range []bool{false, true} {
		t.Run(fmt.Sprintf("sameWidth=%v", sameWidth), func(t *testing.T) {
			ruleReview2ConflictRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
				run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(unused INT,id INT PRIMARY KEY,v INT)", 0), fixture.Exec("INSERT INTO sink VALUES(99,1,5)", 1), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink(id,v) VALUES(NEW.id,NEW.v) ON CONFLICT(id) DO UPDATE SET v=sink.v+EXCLUDED.v WHERE EXCLUDED.v>sink.v", 0))
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				q := "INSERT INTO src VALUES($1,$2)"
				x, closeStmt := ruleReview2ConflictHeld(t, p.Ctx, conn, q, held)
				defer closeStmt()
				run(fixture.Exec("ALTER TABLE sink ADD COLUMN extra INT", 0))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 1, 20))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 2, 30))
				run(ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(25)}, []any{int64(2), int64(30)}))
				run(fixture.Exec("ALTER TABLE sink DROP COLUMN extra", 0))
				if sameWidth {
					run(fixture.Exec("ALTER TABLE sink ADD COLUMN replacement INT", 0))
				}
				run(fixture.Exec("ALTER TABLE sink RENAME TO renamed_sink", 0), fixture.Exec("ALTER TABLE renamed_sink RENAME COLUMN v TO value", 0))
				check := func() {
					run(fixture.Select("SELECT id,value FROM renamed_sink ORDER BY id", []string{"id", "value"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(25)}, {int64(2), int64(30)}}), ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(20)}, []any{int64(2), int64(30)}))
				}
				check()
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					check()
				}
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 1, 40))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 3, 50))
				run(fixture.Select("SELECT id,value FROM renamed_sink ORDER BY id", []string{"id", "value"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(65)}, {int64(2), int64(30)}, {int64(3), int64(50)}}), ruleInts("SELECT id,v FROM src ORDER BY id,v", []any{int64(1), int64(20)}, []any{int64(1), int64(40)}, []any{int64(2), int64(30)}, []any{int64(3), int64(50)}))
			})
		})
	}
}

func TestReferenceRuleReview2ConflictColumnACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"review2_conflict_owner_false", "review2_conflict_owner_true"})
	ruleReview2ConflictRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		owner := fmt.Sprintf("review2_conflict_owner_%v", held)
		run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
		run(fixture.Exec("CREATE USER "+owner, 0), fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(unused INT,id INT PRIMARY KEY,v INT)", 0), fixture.Exec("INSERT INTO sink VALUES(99,1,5)", 1), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink(id,v) VALUES(NEW.id,NEW.v) ON CONFLICT(id) DO UPDATE SET v=sink.v+EXCLUDED.v WHERE EXCLUDED.v>sink.v", 0), fixture.Exec("GRANT INSERT(id,v),UPDATE(v),SELECT(id,v) ON sink TO "+owner, 0), fixture.Exec("ALTER TABLE src OWNER TO "+owner, 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+owner, 0))
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "INSERT INTO src VALUES($1,$2)"
		x, closeStmt := ruleReview2ConflictHeld(t, p.Ctx, conn, q, held)
		defer closeStmt()
		run(fixture.Exec("ALTER TABLE sink ADD COLUMN private INT", 0))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 1, 20))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 2, 30))
		run(fixture.Exec("REVOKE UPDATE(v) ON sink FROM "+owner, 0))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table sink"}, 1, 40))
		run(fixture.Exec("GRANT UPDATE(v) ON sink TO "+owner, 0))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 1, 1, 40))
		run(ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(65)}, []any{int64(2), int64(30)}), ruleInts("SELECT id,v FROM src ORDER BY id,v", []any{int64(1), int64(20)}, []any{int64(1), int64(40)}, []any{int64(2), int64(30)}))
	})
}

func TestReferenceRuleReview2ConflictColumnDependencies(t *testing.T) {
	ruleReview2ConflictRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(unused INT,id INT PRIMARY KEY,v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink(id,v) VALUES(NEW.id,NEW.v) ON CONFLICT(id) DO UPDATE SET v=EXCLUDED.v", 0))
		run(fixture.ImplicitError("ALTER TABLE sink DROP COLUMN unused RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column unused of table sink because other objects depend on it", Detail: "rule r on table src depends on column unused of table sink", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}), ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}, []any{"r"}))
		run(fixture.ImplicitError("ALTER TABLE sink DROP COLUMN v RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column v of table sink because other objects depend on it", Detail: "rule r on table src depends on column v of table sink", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}), ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}, []any{"r"}), fixture.Exec("ALTER TABLE sink DROP COLUMN v CASCADE", 0), ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}), fixture.Exec("INSERT INTO src VALUES(1,20)", 1), ruleInts("SELECT id,v FROM src", []any{int64(1), int64(20)}), ruleRows("SELECT id FROM sink", []string{"id"}, []string{"INT4"}))
	})
}

func TestReferenceRuleReview2ConflictNothingDependencies(t *testing.T) {
	ruleReview2ConflictRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(unused INT,id INT PRIMARY KEY,v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink(id,v) VALUES(NEW.id,NEW.v) ON CONFLICT(id) DO NOTHING", 0),
			fixture.Exec("ALTER TABLE sink DROP COLUMN unused", 0),
			fixture.Exec("INSERT INTO src VALUES(1,20)", 1), fixture.Exec("INSERT INTO src VALUES(1,40)", 1),
			ruleInts("SELECT id,v FROM sink", []any{int64(1), int64(20)}),
			ruleInts("SELECT id,v FROM src ORDER BY v", []any{int64(1), int64(20)}, []any{int64(1), int64(40)}))
		run(fixture.ImplicitError("ALTER TABLE sink DROP COLUMN v RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column v of table sink because other objects depend on it", Detail: "rule r on table src depends on column v of table sink", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}),
			fixture.Exec("ALTER TABLE sink DROP COLUMN v CASCADE", 0),
			ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}))
	})
}
