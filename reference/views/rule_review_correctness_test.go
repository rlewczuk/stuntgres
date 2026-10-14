package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ruleReviewCoreRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
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

func TestReferenceRuleReviewPrincipalSources(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"review_input_actor_false", "review_input_owner_false", "review_input_actor_true", "review_input_owner_true"})
	ruleReviewCoreRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		actor, owner := fmt.Sprintf("review_input_actor_%v", held), fmt.Sprintf("review_input_owner_%v", held)
		run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
		run(fixture.Exec("CREATE USER "+actor, 0), fixture.Exec("CREATE USER "+owner, 0),
			fixture.Exec("CREATE TABLE input(id INT,v INT)", 0), fixture.Exec("INSERT INTO input VALUES(1,10),(2,20),(3,30)", 3),
			fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0),
			fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v)", 0),
			fixture.Exec("GRANT SELECT ON input TO "+actor, 0), fixture.Exec("GRANT INSERT ON sink TO "+owner, 0),
			fixture.Exec("ALTER TABLE src OWNER TO "+owner, 0), fixture.Exec("GRANT INSERT ON src TO "+actor, 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor+","+owner, 0))
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION "+actor); err != nil {
			t.Fatal(err)
		}
		q := "INSERT INTO src SELECT id,v FROM input WHERE id=$1"
		var executor fixture.Executor = conn
		if held {
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, 1))
		run(fixture.Exec("REVOKE SELECT ON input FROM "+actor, 0))
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table input"}, 2)
		// A held statement must consult current grants, not its successful first execution.
		if held {
			s.ErrorStage = "execute"
		}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
		run(fixture.Exec("GRANT SELECT ON input TO "+actor, 0))
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, 3))
		if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Fatal(err)
		}
		run(ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(10)}, []any{int64(3), int64(30)}), ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(10)}, []any{int64(3), int64(30)}))
	})
}

func TestReferenceRuleReviewConflictRecipes(t *testing.T) {
	for _, event := range []string{"INSERT", "UPDATE"} {
		t.Run(event, func(t *testing.T) {
			ruleReviewCoreRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
				run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("INSERT INTO sink VALUES(1,5),(2,50)", 2))
				where, value := "NEW.v>sink.v", "NEW.v"
				if event == "UPDATE" {
					run(fixture.Exec("INSERT INTO src VALUES(1,10),(2,20),(3,30)", 3))
					where, value = "NEW.v>OLD.v AND NEW.v>sink.v", "NEW.v+OLD.v"
				}
				invalid := "CREATE RULE r AS ON " + event + " TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v) ON CONFLICT(id) DO UPDATE SET v=" + value + " WHERE " + where
				wantError := fixture.Diagnostic{Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"new\"", Detail: "There is an entry for table \"new\", but it cannot be referenced from this part of the query.", Position: "108"}
				run(fixture.ImplicitError(invalid, wantError),
					ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}),
					ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(5)}, []any{int64(2), int64(50)}))
				if event == "UPDATE" {
					run(ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}, []any{int64(3), int64(30)}))
				} else {
					run(ruleInts("SELECT id,v FROM src"))
				}
				// OLD/NEW are admitted in the producer, not in conflict UPDATE's namespace.
				run(fixture.Exec("CREATE RULE r AS ON "+event+" TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v) ON CONFLICT(id) DO UPDATE SET v=EXCLUDED.v WHERE EXCLUDED.v>sink.v", 0))
				q := "INSERT INTO src VALUES(1,20),(2,40),(3,60)"
				if event == "UPDATE" {
					q = "UPDATE src SET v=v*2"
				}
				run(fixture.Exec(q, 3), ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(20)}, []any{int64(2), int64(50)}, []any{int64(3), int64(60)}))
			})
		})
	}
	t.Run("partial_assignment", func(t *testing.T) {
		ruleReviewCoreRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
			run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,a INT[])", 0), fixture.Exec("INSERT INTO sink VALUES(1,ARRAY[5,6])", 1))
			wantError := fixture.Diagnostic{Code: "42P01", Message: "invalid reference to FROM-clause entry for table \"new\"", Detail: "There is an entry for table \"new\", but it cannot be referenced from this part of the query.", Position: "120"}
			run(fixture.ImplicitError("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,ARRAY[NEW.v,0]) ON CONFLICT(id) DO UPDATE SET a[1]=NEW.v WHERE NEW.v>sink.a[1]", wantError),
				ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}),
				ruleInts("SELECT id,v FROM src"),
				ruleRows("SELECT id,a[1] AS first,a[2] AS second FROM sink", []string{"id", "first", "second"}, []string{"INT4", "INT4", "INT4"}, []any{int64(1), int64(5), int64(6)}),
				fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,ARRAY[NEW.v,0]) ON CONFLICT(id) DO UPDATE SET a[1]=EXCLUDED.a[1] WHERE EXCLUDED.a[1]>sink.a[1]", 0),
				fixture.Exec("INSERT INTO src VALUES(1,20)", 1),
				ruleRows("SELECT id,a[1] AS first,a[2] AS second FROM sink", []string{"id", "first", "second"}, []string{"INT4", "INT4", "INT4"}, []any{int64(1), int64(20), int64(6)}))
		})
	})
}

func TestReferenceRuleReviewConflictAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"review_conflict_actor_false", "review_conflict_owner_false", "review_conflict_actor_true", "review_conflict_owner_true"})
	ruleReviewCoreRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		actor, owner := fmt.Sprintf("review_conflict_actor_%v", held), fmt.Sprintf("review_conflict_owner_%v", held)
		run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
		run(fixture.Exec("CREATE USER "+actor, 0), fixture.Exec("CREATE USER "+owner, 0), fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("INSERT INTO sink VALUES(1,5)", 1), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v) ON CONFLICT(id) DO UPDATE SET v=EXCLUDED.v", 0), fixture.Exec("GRANT INSERT,UPDATE,SELECT ON sink TO "+owner, 0), fixture.Exec("ALTER TABLE src OWNER TO "+owner, 0), fixture.Exec("GRANT INSERT ON src TO "+actor, 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor+","+owner, 0))
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION "+actor); err != nil {
			t.Fatal(err)
		}
		q := "INSERT INTO src VALUES($1,$2)"
		var executor fixture.Executor = conn
		if held {
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, 1, 20))
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, 2, 30))
		run(fixture.Exec("REVOKE UPDATE ON sink FROM "+owner, 0))
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table sink"}, 1, 40)
		if held {
			s.ErrorStage = "execute"
		}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
		if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Fatal(err)
		}
		run(ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(20)}, []any{int64(2), int64(30)}), ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(20)}, []any{int64(2), int64(30)}))
	})
}

func TestReferenceRuleReviewActionRoundTrip(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		t.Run(fmt.Sprintf("bounded=%v", bounded), func(t *testing.T) {
			ruleReviewCoreRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
				action := "INSERT INTO sink VALUES(NEW.id,NEW.v) ON CONFLICT(id) DO UPDATE SET v=EXCLUDED.v WHERE EXCLUDED.v>sink.v"
				if bounded {
					action = "INSERT INTO sink VALUES(NEW.id,NEW.v),(NEW.id,NEW.v+100) ORDER BY 2 DESC LIMIT 1"
				}
				run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT PRIMARY KEY,v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO "+action, 0), fixture.Exec("INSERT INTO src VALUES(1,10)", 1), fixture.Exec("ALTER TABLE sink RENAME TO renamed_sink", 0), fixture.Exec("ALTER TABLE renamed_sink RENAME COLUMN v TO value", 0))
				initial := int64(10)
				if bounded {
					initial = 110
				}
				check := func() {
					run(ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}), ruleRows("SELECT id,value FROM renamed_sink", []string{"id", "value"}, []string{"INT4", "INT4"}, []any{int64(1), initial}))
				}
				check()
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					// Restore over populated data: post-data rules must not fire while loading it.
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					check()
				}
				id, want := 1, int64(40)
				if bounded {
					id, want = 2, 140
				}
				run(fixture.Exec(fmt.Sprintf("INSERT INTO src VALUES(%d,40)", id), 1))
				if bounded {
					run(ruleRows("SELECT id,value FROM renamed_sink ORDER BY id", []string{"id", "value"}, []string{"INT4", "INT4"}, []any{int64(1), initial}, []any{int64(2), want}))
				} else {
					run(ruleRows("SELECT id,value FROM renamed_sink", []string{"id", "value"}, []string{"INT4", "INT4"}, []any{int64(1), want}))
				}
			})
		})
	}
}

func TestReferenceRuleReviewProgramPreflight(t *testing.T) {
	for _, runtime := range []bool{false, true} {
		t.Run(fmt.Sprintf("runtime=%v", runtime), func(t *testing.T) {
			ruleReviewCoreRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
				run(fixture.Exec("CREATE SEQUENCE review_seq", 0), fixture.Exec("CREATE TABLE src(id INT DEFAULT nextval('review_seq'),v INT)", 0), fixture.Exec("CREATE TABLE sink(v INT)", 0), fixture.Exec("CREATE TABLE input(v INT)", 0), fixture.Exec("INSERT INTO input VALUES(0)", 1))
				expr := "1/0"
				if runtime {
					expr = "1/NEW.v"
				}
				run(fixture.Exec("CREATE RULE r AS ON INSERT TO src DO (INSERT INTO sink VALUES(7); SELECT "+expr+")", 0))
				s := fixture.ImplicitError("INSERT INTO src(v) SELECT v FROM input", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.Prepared = held
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				run(ruleInts("SELECT id,v FROM src"), ruleRows("SELECT v FROM sink", []string{"v"}, []string{"INT4"}), ruleRows("SELECT last_value,is_called FROM review_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, []any{int64(1), runtime}))
			})
		})
	}
}

func TestReferenceRuleReviewQualifiedSet(t *testing.T) {
	for _, qualification := range []string{"rule", "original", "none"} {
		t.Run(qualification, func(t *testing.T) {
			ruleReviewCoreRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
				run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("INSERT INTO src VALUES(1,10)", 1), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0))
				qual := ""
				if qualification == "rule" {
					qual = " WHERE OLD.v>0"
				}
				create := "CREATE RULE r AS ON UPDATE TO src" + qual + " DO (INSERT INTO sink VALUES(7,70); SELECT 1 UNION SELECT 2)"
				if qualification == "rule" {
					run(fixture.ImplicitError(create, fixture.Diagnostic{Code: "0A000", Message: "conditional UNION/INTERSECT/EXCEPT statements are not implemented"}),
						ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}),
						ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}), ruleInts("SELECT id,v FROM sink"))
					return
				}
				run(fixture.Exec(create, 0))
				q := "UPDATE src SET v=20"
				if qualification == "original" {
					q += " WHERE id=1"
				}
				if qualification == "none" {
					run(fixture.Exec(q, 1), ruleInts("SELECT id,v FROM src", []any{int64(1), int64(20)}), ruleInts("SELECT id,v FROM sink", []any{int64(7), int64(70)}))
				} else {
					run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "conditional UNION/INTERSECT/EXCEPT statements are not implemented"}), ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}), ruleInts("SELECT id,v FROM sink"))
				}
			})
		})
	}
}
