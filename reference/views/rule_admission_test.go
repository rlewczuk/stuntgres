package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRuleSelectReturnAdmission(t *testing.T) {
	cases := []struct{ name, sql, code, message, detail string }{
		{"alias", "CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO v DO INSTEAD SELECT id AS other FROM base", "42P17", "SELECT rule's target entry 1 has different column name from column \"id\"", "SELECT target entry is named \"other\"."},
		{"few", "CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO v DO INSTEAD SELECT", "42P17", "SELECT rule's target list has too few entries", ""},
		{"many", "CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO v DO INSTEAD SELECT id,id FROM base", "42P17", "SELECT rule's target list has too many entries", ""},
		{"also", "CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO v DO ALSO SELECT id FROM base", "0A000", "rules on SELECT must have action INSTEAD SELECT", ""},
		{"condition", "CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO v WHERE true DO INSTEAD SELECT id FROM base", "0A000", "event qualifications are not implemented for rules on SELECT", ""},
		{"multiple", "CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO v DO INSTEAD (SELECT id FROM base; SELECT id FROM base)", "0A000", "multiple actions for rules on SELECT are not implemented", ""},
		{"nothing", "CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO v DO INSTEAD NOTHING", "0A000", "rules on SELECT must have action INSTEAD SELECT", ""},
		{"name", "CREATE OR REPLACE RULE other AS ON SELECT TO v DO INSTEAD SELECT id FROM base", "42P17", "view rule for \"v\" must be named \"_RETURN\"", ""},
		{"modifying_with", "CREATE OR REPLACE RULE \"_RETURN\" AS ON SELECT TO v DO INSTEAD WITH d AS (INSERT INTO base VALUES(2) RETURNING id) SELECT id FROM d", "0A000", "rules on SELECT must not contain data-modifying statements in WITH", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE base(id INT)", 0), fixture.Exec("INSERT INTO base VALUES(1)", 1), fixture.Exec("CREATE VIEW v AS SELECT id FROM base", 0), fixture.ImplicitError(c.sql, fixture.Diagnostic{Code: c.code, Message: c.message, Detail: c.detail}), ruleRows("SELECT id FROM v", []string{"id"}, []string{"INT4"}, []any{int64(1)}))
				if c.name == "modifying_with" {
					ruleRun(t, p, b, held, ruleRows("SELECT id FROM base ORDER BY id", []string{"id"}, []string{"INT4"}, []any{int64(1)}))
				}
			})
		})
	}
}

func TestReferenceRuleRestrictionsAdmission(t *testing.T) {
	cases := []struct{ name, sql, code, message, hint string }{
		{"insert_old", "CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(OLD.id,NEW.v)", "42P17", "ON INSERT rule cannot use OLD", ""},
		{"delete_new", "CREATE RULE r AS ON DELETE TO src DO INSERT INTO sink VALUES(OLD.id,NEW.v)", "42P17", "ON DELETE rule cannot use NEW", ""},
		{"target_old", "CREATE RULE r AS ON UPDATE TO src DO UPDATE OLD SET v=1", "42P01", "relation \"old\" does not exist", ""},
		{"target_new", "CREATE RULE r AS ON UPDATE TO src DO UPDATE NEW SET v=1", "42P01", "relation \"new\" does not exist", ""},
		{"predicate", "CREATE RULE r AS ON INSERT TO src WHERE NEW.id DO NOTHING", "42804", "argument of WHERE must be type boolean, not type integer", ""},
		{"aggregate", "CREATE RULE r AS ON INSERT TO src WHERE count(*)>0 DO NOTHING", "42803", "aggregate functions are not allowed in WHERE", ""},
		{"with_body_new", "CREATE RULE r AS ON INSERT TO src DO WITH q AS (SELECT NEW.id AS id,NEW.v AS v) INSERT INTO sink SELECT id,v FROM q", "0A000", "cannot refer to NEW within WITH query", ""},
		{"with_body_old", "CREATE RULE r AS ON DELETE TO src DO WITH q AS (SELECT OLD.id AS id) DELETE FROM sink USING q WHERE sink.id=q.id", "0A000", "cannot refer to OLD within WITH query", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				want := fixture.Diagnostic{Code: c.code, Message: c.message, Hint: c.hint}
				switch c.name {
				case "target_old", "target_new":
					want.Position = "45"
				case "predicate", "aggregate":
					want.Position = "41"
				}
				ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0), fixture.ImplicitError(c.sql, want), ruleRows("SELECT rulename FROM pg_rewrite WHERE ev_class='src'::regclass::oid", []string{"rulename"}, []string{"NAME"}))
			})
		})
	}
	t.Run("with_legal_outer_new", func(t *testing.T) {
		ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			ruleRun(t, p, b, held,
				fixture.Exec("CREATE TABLE src(id INT,v INT)", 0),
				fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0),
				fixture.Exec("CREATE TABLE lookup(v INT)", 0),
				fixture.Exec("INSERT INTO lookup VALUES(9)", 1),
				fixture.Exec("CREATE RULE r AS ON INSERT TO src DO WITH q AS (SELECT v FROM lookup) INSERT INTO sink SELECT NEW.id,NEW.v+q.v FROM q", 0),
				fixture.Exec("INSERT INTO src VALUES(1,10),(2,20)", 2),
				ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}),
				ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(19)}, []any{int64(2), int64(29)}),
				ruleRows("SELECT v FROM lookup", []string{"v"}, []string{"INT4"}, []any{int64(9)}),
			)
		})
	})
	t.Run("with_nested_correlated_new", func(t *testing.T) {
		ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			ruleRun(t, p, b, held,
				fixture.Exec("CREATE TABLE src(id INT,v INT)", 0),
				fixture.Exec("CREATE TABLE sink(id INT,v INT)", 0),
				fixture.Exec("CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink VALUES(NEW.id,(WITH q AS (SELECT NEW.v+9 AS v) SELECT v FROM q))", 0),
				fixture.Exec("INSERT INTO src VALUES(1,10),(2,20)", 2),
				ruleInts("SELECT id,v FROM src ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}),
				ruleInts("SELECT id,v FROM sink ORDER BY id", []any{int64(1), int64(19)}, []any{int64(2), int64(29)}),
			)
		})
	})
}

func TestReferenceRuleReturningSelectActions(t *testing.T) {
	for _, actions := range []string{"SELECT 11 AS out", "(SELECT 11 AS out; SELECT 22 AS out)"} {
		t.Run(actions, func(t *testing.T) {
			ruleRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				ruleRun(t, p, b, held, fixture.Exec("CREATE TABLE src(id INT,v INT)", 0), fixture.Exec("CREATE RULE r AS ON INSERT TO src DO ALSO "+actions, 0))
				expected := []int64{11}
				if actions[0] == '(' {
					expected = append(expected, 22)
				}
				const query = "INSERT INTO src VALUES(1,10)"
				var rows *sql.Rows
				var err error
				if held {
					stmt, prepareErr := b.DB.PrepareContext(p.Ctx, query)
					if prepareErr != nil {
						t.Fatalf("SELECT action Prepare diagnostic: %#v", fixture.ExtractDiagnostic(t, prepareErr))
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = b.DB.QueryContext(p.Ctx, query)
				}
				if err != nil {
					t.Fatalf("SELECT action Query diagnostic: %#v", fixture.ExtractDiagnostic(t, err))
				}
				defer rows.Close()
				if held {
					// The retained original INSERT has no RETURNING description.
					// Its action still executes, but is not a prepared output contract.
					cols, err := rows.Columns()
					if err != nil || len(cols) != 0 {
						t.Fatalf("prepared columns got %v want empty: %v", cols, err)
					}
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 0 {
						t.Fatalf("prepared types got %v want empty: %v", types, err)
					}
					if rows.Next() {
						t.Fatal("unexpected prepared action output row")
					}
					if rows.NextResultSet() {
						t.Fatal("unexpected prepared action result set")
					}
				} else {
					for i, want := range expected {
						cols, err := rows.Columns()
						if err != nil || len(cols) != 1 || cols[0] != "out" {
							t.Fatalf("columns %v: %v", cols, err)
						}
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
							t.Fatal("SELECT action type is not INT4")
						}
						if !rows.Next() {
							t.Fatalf("result %d empty: %v", i, rows.Err())
						}
						var got int64
						if err := rows.Scan(&got); err != nil || got != want {
							t.Fatalf("result %d got %d want %d error %v", i, got, want, err)
						}
						if rows.Next() {
							t.Fatal("extra action row")
						}
						if i+1 < len(expected) && !rows.NextResultSet() {
							t.Fatalf("missing action result: %v", rows.Err())
						}
					}
					if rows.NextResultSet() {
						t.Fatal("unexpected trailing result set")
					}
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				ruleRun(t, p, b, held, ruleInts("SELECT id,v FROM src", []any{int64(1), int64(10)}))
			})
		})
	}
}
