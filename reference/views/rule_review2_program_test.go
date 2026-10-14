package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func ruleReview2ProgramRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool)) {
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

func TestReferenceRuleReview2NestedPreparation(t *testing.T) {
	for _, shape := range []string{"demanded", "unused", "row", "parameterized_source", "parameter_value", "domain"} {
		t.Run(shape, func(t *testing.T) {
			ruleReview2ProgramRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
				run(fixture.Exec("CREATE SEQUENCE review2_seq", 0), fixture.Exec("CREATE TABLE src(id INT DEFAULT nextval('review2_seq'),v INT)", 0), fixture.Exec("CREATE TABLE sink(v INT)", 0), fixture.Exec("CREATE TABLE input(v INT)", 0), fixture.Exec("INSERT INTO input VALUES(0)", 1))
				expr, output := "1/0", "q.n"
				if shape == "unused" {
					output = "7"
				}
				if shape == "row" || shape == "parameterized_source" || shape == "parameter_value" {
					expr = "1/NEW.v"
				}
				if shape == "domain" {
					run(fixture.Exec("CREATE DOMAIN review2_int AS INT CHECK(VALUE>=0)", 0))
					// No row dependency masks the domain-coercion frontier.
					expr = "1/(0::review2_int)"
				}
				run(fixture.Exec("CREATE RULE r AS ON INSERT TO src DO (INSERT INTO sink VALUES(7); SELECT "+output+" AS out FROM (SELECT "+expr+" AS n) AS q)", 0))
				q := "INSERT INTO src(v) SELECT v FROM input"
				var args []any
				if shape == "parameterized_source" {
					// Caller-parameterized source control, not an arithmetic Param.
					q += " WHERE v=$1"
					args = []any{0}
				}
				if shape == "parameter_value" {
					// Real bound Param substituted into the demanded action recipe.
					// PostgreSQL custom-plan timing is an oracle candidate, not a
					// native capability exception or permission to fold all Params.
					q = "INSERT INTO src(v) VALUES($1)"
					args = []any{0}
				}
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				var executor fixture.Executor = conn
				if held {
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				}
				run(ruleRows("SELECT last_value,is_called FROM review2_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, []any{int64(1), false}))
				if shape == "unused" {
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, 1, args...))
					run(ruleInts("SELECT id,v FROM src", []any{int64(1), int64(0)}), ruleRows("SELECT v FROM sink", []string{"v"}, []string{"INT4"}, []any{int64(7)}))
				} else {
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, args...))
					run(ruleInts("SELECT id,v FROM src"), ruleRows("SELECT v FROM sink", []string{"v"}, []string{"INT4"}))
				}
				called := shape != "demanded" && shape != "parameter_value"
				run(ruleRows("SELECT last_value,is_called FROM review2_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, []any{int64(1), called}))
			})
		})
	}
}

func TestReferenceRuleReview2CTEPreparation(t *testing.T) {
	for _, primary := range []string{"select", "insert"} {
		t.Run(primary, func(t *testing.T) {
			ruleReview2ProgramRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
				run(fixture.Exec("CREATE SEQUENCE review2_seq", 0), fixture.Exec("CREATE TABLE src(id INT DEFAULT nextval('review2_seq'),v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.Exec("CREATE TABLE dst(v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO sink VALUES(NEW.id,NEW.v) RETURNING id,v", 0))
				q := "WITH d AS (INSERT INTO src(v) VALUES(0) RETURNING id,v) SELECT 1/0 AS out FROM d"
				if primary == "insert" {
					q = "WITH d AS (INSERT INTO src(v) VALUES(0) RETURNING id,v) INSERT INTO dst SELECT 1/0 FROM d"
				}
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				var executor fixture.Executor = conn
				if held {
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				}
				run(ruleRows("SELECT last_value,is_called FROM review2_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, []any{int64(1), false}))
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
				run(ruleInts("SELECT id,v FROM src"), ruleInts("SELECT id,v FROM sink"), ruleRows("SELECT v FROM dst", []string{"v"}, []string{"INT4"}), ruleRows("SELECT last_value,is_called FROM review2_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, []any{int64(1), false}))
			})
		})
	}
}

type ruleReview2Set struct {
	Columns []string
	Types   []string
	Rows    [][]any
}

// Capture every public descriptor and value before asserting independent oracle
// expectations. A mismatch prints the entire ordered stream, not just parity.
func ruleReview2Consume(t *testing.T, rows *sql.Rows, want []ruleReview2Set) {
	t.Helper()
	defer rows.Close()
	var got []ruleReview2Set
	for {
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		set := ruleReview2Set{Columns: cols}
		for _, typ := range types {
			set.Types = append(set.Types, typ.DatabaseTypeName())
		}
		for rows.Next() {
			values := make([]any, len(cols))
			pointers := make([]any, len(cols))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			for i := range values {
				values[i] = fixture.Normalize(values[i])
			}
			set.Rows = append(set.Rows, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		got = append(got, set)
		if !rows.NextResultSet() {
			break
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered rowsets got %#v want %#v", got, want)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceRuleReview2CombinedOutputs(t *testing.T) {
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/empty=%v", event, empty), func(t *testing.T) {
				ruleReview2ProgramRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
					run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
					run(fixture.Exec("CREATE TABLE src(id INT)", 0), fixture.Exec("CREATE TABLE log(v INT)", 0))
					if event != "INSERT" {
						run(fixture.Exec("INSERT INTO src VALUES(1)", 1))
					}
					run(fixture.Exec("CREATE RULE r AS ON "+event+" TO src DO ALSO (INSERT INTO log VALUES(7); SELECT 11 AS first; SELECT 22 AS second)", 0))
					q := "INSERT INTO src SELECT 1"
					if event == "UPDATE" {
						q = "UPDATE src SET id=id+1"
					}
					if event == "DELETE" {
						q = "DELETE FROM src"
					}
					if empty {
						q += " WHERE FALSE"
					}
					q += " RETURNING id AS original"
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					var executor fixture.Executor = conn
					if held {
						stmt, err := conn.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
					}
					original := ruleReview2Set{Columns: []string{"original"}, Types: []string{"INT4"}}
					value := int64(1)
					if event == "UPDATE" {
						value = 2
					}
					if !empty {
						original.Rows = [][]any{{value}}
					}
					want := []ruleReview2Set{original}
					rows, err := executor.QueryContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					ruleReview2Consume(t, rows, want)
					count := int64(1)
					if empty || event == "DELETE" {
						count = 0
					}
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(q, count))
					// Close an unread authoritative RETURNING cursor, then prove
					// this same physical owner still executes with exact effects.
					rows, err = executor.QueryContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					logCount := int64(3)
					if empty {
						logCount = 0
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, ruleRows("SELECT count(*) AS n FROM log", []string{"n"}, []string{"INT8"}, []any{logCount}))
					var sourceRows [][]any
					if empty && event != "INSERT" {
						sourceRows = [][]any{{int64(1)}}
					}
					if !empty && event == "INSERT" {
						sourceRows = [][]any{{int64(1)}, {int64(1)}, {int64(1)}}
					}
					if !empty && event == "UPDATE" {
						sourceRows = [][]any{{int64(4)}}
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id FROM src ORDER BY id", []string{"id"}, []string{"INT4"}, sourceRows))
					if !b.PG {
						// Native eager transport has an explicit precancel boundary;
						// do not manufacture equivalent PostgreSQL wire admission.
						ctx, cancel := context.WithCancel(p.Ctx)
						cancel()
						rows, err := executor.QueryContext(ctx, q)
						if err != context.Canceled || rows != nil {
							t.Fatalf("precanceled query rows=%v error=%v", rows, err)
						}
					}
				})
			})
		}
	}
}

// Additional source-root candidates: expected outcomes are independent SQL
// specifications pending root's pinned oracle run, not inferred native goldens.
func TestReferenceRuleReview2MutationPreparation(t *testing.T) {
	for _, action := range []string{"update", "delete"} {
		for _, payload := range []string{"demanded", "unused", "dead_case"} {
			t.Run(action+"/"+payload, func(t *testing.T) {
				ruleReview2ProgramRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
					run := func(s ...fixture.Step) { ruleRun(t, p, b, false, s...) }
					run(fixture.Exec("CREATE SEQUENCE review2_seq", 0),
						fixture.Exec("CREATE TABLE src(id INT DEFAULT nextval('review2_seq'),v INT)", 0),
						fixture.Exec("CREATE TABLE sink(v INT)", 0),
						fixture.Exec("INSERT INTO sink VALUES(5)", 1))
					body := "UPDATE sink SET v=q.n FROM (SELECT 1/0 AS n) AS q"
					if payload == "unused" {
						body = "UPDATE sink SET v=7 FROM (SELECT 1/0 AS n) AS q"
					}
					if payload == "dead_case" {
						body = "UPDATE sink SET v=CASE WHEN FALSE THEN q.n ELSE 7 END FROM (SELECT 1/0 AS n) AS q"
					}
					if action == "delete" {
						predicate := "q.n=0"
						if payload == "unused" {
							predicate = "TRUE"
						}
						if payload == "dead_case" {
							predicate = "CASE WHEN FALSE THEN q.n=0 ELSE TRUE END"
						}
						body = "DELETE FROM sink USING (SELECT 1/0 AS n) AS q WHERE " + predicate
					}
					run(fixture.Exec("CREATE RULE r AS ON INSERT TO src DO ALSO "+body, 0))
					const query = "INSERT INTO src(v) VALUES(0)"
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					var executor fixture.Executor = conn
					if held {
						stmt, err := conn.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
					}
					run(ruleRows("SELECT last_value,is_called FROM review2_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, []any{int64(1), false}))
					if payload == "demanded" {
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.ImplicitError(query, fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
						run(ruleInts("SELECT id,v FROM src"), ruleRows("SELECT v FROM sink", []string{"v"}, []string{"INT4"}, []any{int64(5)}))
					} else {
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(query, 1))
						run(ruleInts("SELECT id,v FROM src", []any{int64(1), int64(0)}))
						var sinkRows [][]any
						if action == "update" {
							sinkRows = [][]any{{int64(7)}}
						}
						run(fixture.Select("SELECT v FROM sink", []string{"v"}, []string{"INT4"}, sinkRows))
					}
					run(ruleRows("SELECT last_value,is_called FROM review2_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, []any{int64(1), payload != "demanded"}))
				})
			})
		}
	}
}
