package reference_test

import (
	"bytes"
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRuleAtomicity(t *testing.T) {
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		t.Run(event, func(t *testing.T) {
			ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
				run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE log(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT NOT NULL)", 0))
				pseudo, command := "NEW", "INSERT INTO src VALUES(1,10)"
				var source [][]any
				if event != "INSERT" {
					run(fixture.Exec("INSERT INTO src VALUES(1,10)", 1))
					source = [][]any{{int64(1), int64(10)}}
					command = "UPDATE src SET v=20"
				}
				if event == "DELETE" {
					pseudo = "OLD"
					command = "DELETE FROM src"
				}
				run(fixture.Exec("CREATE RULE r AS ON "+event+" TO src DO (INSERT INTO log VALUES("+pseudo+".id,"+pseudo+".v); INSERT INTO sink VALUES("+pseudo+".id,NULL))", 0), fixture.ImplicitError(command, fixture.Diagnostic{Code: "23502", Message: "null value in column \"v\" of relation \"sink\" violates not-null constraint", Detail: "Failing row contains (1, null).", Schema: "public", Table: "sink", Column: "v"}), fixture.Select("SELECT id,v FROM src", []string{"id", "v"}, []string{"INT4", "INT4"}, source), ruleInts("SELECT id,v FROM log"), ruleInts("SELECT id,v FROM sink"))
			})
		})
	}
}

func TestReferenceRulePrepared(t *testing.T) {
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0))
		stmt, err := b.DB.PrepareContext(p.Ctx, "INSERT INTO src VALUES($1,$2)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		exec := func(id, v int) {
			t.Helper()
			result, err := stmt.ExecContext(p.Ctx, id, v)
			if err != nil {
				t.Fatal(err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != 1 {
				t.Fatalf("affected %d error %v want 1", n, err)
			}
		}
		exec(1, 10)
		run(fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v)", 0))
		exec(2, 20)
		run(fixture.Exec("CREATE OR REPLACE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO sink VALUES(NEW.id,NEW.v+1)", 0))
		exec(3, 30)
		run(fixture.Exec("ALTER RULE r ON src RENAME TO renamed", 0))
		exec(4, 40)
		run(fixture.Exec("DROP RULE renamed ON src", 0))
		exec(5, 50)
		run(ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}, []any{int64(5), int64(50)}), ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(2), int64(20)}, []any{int64(3), int64(31)}, []any{int64(4), int64(41)}))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, q := range []string{"PREPARE rule_insert(INT,INT) AS INSERT INTO src VALUES($1,$2)", "CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v)", "EXECUTE rule_insert(6,60)", "DEALLOCATE rule_insert"} {
			if _, err := conn.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		run(ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(2), int64(20)}, []any{int64(3), int64(31)}, []any{int64(4), int64(41)}, []any{int64(6), int64(60)}))
	})
}

func TestReferenceRuleControls(t *testing.T) {
	t.Run("action_with_modifying_cte", func(t *testing.T) {
		ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			run := func(steps ...fixture.Step) { ruleRun(t, p, b, held, steps...) }
			run(
				fixture.Exec("CREATE TABLE src(id INT,v INT)", 0),
				fixture.Exec("CREATE TABLE auxiliary(id INT,v INT)", 0),
				fixture.Exec("CREATE TABLE unused(id INT)", 0),
				fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0),
				fixture.Exec("CREATE RULE r AS ON INSERT TO src DO WITH moved(mid,mv) AS (INSERT INTO auxiliary VALUES(7,70) RETURNING id,v), ignored AS (INSERT INTO unused VALUES(9)) INSERT INTO sink SELECT NEW.id,NEW.v+moved.mv FROM moved", 0),
				fixture.Exec("INSERT INTO src VALUES(1,10)", 1),
				ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}),
				ruleInts("SELECT id,v FROM sink", []any{int64(1), int64(80)}),
				ruleInts("SELECT id,v FROM auxiliary", []any{int64(7), int64(70)}),
				ruleRows("SELECT id FROM unused", []string{"id"}, []string{"INT4"}, []any{int64(9)}),
				fixture.Exec("ALTER TABLE auxiliary RENAME COLUMN v TO value", 0),
				fixture.Exec("ALTER TABLE unused RENAME COLUMN id TO key", 0),
			)
			check := func() {
				run(ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}),
					ruleInts("SELECT id,v FROM sink", []any{int64(1), int64(80)}),
					ruleRows("SELECT id,value FROM auxiliary", []string{"id", "value"}, []string{"INT4", "INT4"}, []any{int64(7), int64(70)}),
					ruleRows("SELECT key FROM unused", []string{"key"}, []string{"INT4"}, []any{int64(9)}))
			}
			check()
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				check()
			}
			run(fixture.Exec("INSERT INTO src VALUES(2,20)", 1),
				ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}),
				ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(80)}, []any{int64(2), int64(90)}),
				ruleRows("SELECT id,value FROM auxiliary ORDER BY id,value", []string{"id", "value"}, []string{"INT4", "INT4"}, []any{int64(7), int64(70)}, []any{int64(7), int64(70)}),
				ruleRows("SELECT key FROM unused ORDER BY key", []string{"key"}, []string{"INT4"}, []any{int64(9)}, []any{int64(9)}))
		})
	})
	ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s ...fixture.Step) { ruleRun(t, p, b, held, s...) }
		run(fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,NEW.v)", 0), fixture.Exec("INSERT INTO src VALUES(1,10)", 1), fixture.Exec("ALTER TABLE src RENAME TO renamed", 0), fixture.Exec("ALTER TABLE sink RENAME COLUMN v TO value", 0))
		check := func() {
			run(ruleInts("SELECT id,v FROM renamed", []any{int64(1), int64(10)}), ruleRows("SELECT id,value FROM sink", []string{"id", "value"}, []string{"INT4", "INT4"}, []any{int64(1), int64(10)}))
		}
		check()
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
			bad := append(append([]byte(nil), dump.Bytes()...), []byte("\nCREATE RULE invalid AS ON INSERT TO renamed DO INSERT INTO missing VALUES(NEW.id);\n")...)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
				t.Fatal("invalid trailing rule accepted")
			}
			check()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check()
		}
		run(fixture.Exec("INSERT INTO renamed VALUES(2,20)", 1), ruleRows("SELECT id,value FROM sink ORDER BY id", []string{"id", "value"}, []string{"INT4", "INT4"}, []any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
	})
}
