package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const fdwAsyncQuery = "SELECT id,label FROM async_slow UNION ALL SELECT id,label FROM async_ready"

var fdwAsyncBag = [][]any{{int64(1), "slow"}, {int64(1), "slow"}, {int64(2), nil}, {int64(3), "ready"}, {int64(4), "tail"}}

func fdwAsyncRows(q string, bag [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, []string{"id", "label"}, []string{"INT4", "TEXT"}, bag, args...)
	s.Bag = true
	return s
}

type fdwAsyncResult struct {
	rows           [][]any
	columns, types []string
	err            error
}

func fdwAsyncCollect(rows *sql.Rows, err error) (result fdwAsyncResult) {
	if err != nil {
		result.err = err
		return
	}
	defer rows.Close()
	result.columns, result.err = rows.Columns()
	if result.err != nil {
		return
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		result.err = err
		return
	}
	for _, column := range types {
		result.types = append(result.types, column.DatabaseTypeName())
	}
	for rows.Next() {
		var id, label any
		if err := rows.Scan(&id, &label); err != nil {
			result.err = err
			return
		}
		result.rows = append(result.rows, []any{fixture.Normalize(id), fixture.Normalize(label)})
	}
	result.err = rows.Err()
	return
}
func fdwAsyncAssert(t *testing.T, result fdwAsyncResult, want [][]any) {
	t.Helper()
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !reflect.DeepEqual(result.columns, []string{"id", "label"}) || !reflect.DeepEqual(result.types, []string{"INT4", "TEXT"}) {
		t.Fatalf("metadata %v %v", result.columns, result.types)
	}
	bag := func(rows [][]any) []string {
		values := make([]string, 0, len(rows))
		for _, row := range rows {
			values = append(values, fmt.Sprintf("%#v", row))
		}
		sort.Strings(values)
		return values
	}
	if !reflect.DeepEqual(bag(result.rows), bag(want)) {
		t.Fatalf("bag got %#v want %#v", result.rows, want)
	}
}
func fdwAsyncGated(t *testing.T, c *fdwAsyncCase, both bool, query func(context.Context) fdwAsyncResult) fdwAsyncResult {
	t.Helper()
	ctx, cancel := context.WithCancel(c.p.Ctx)
	done := make(chan fdwAsyncResult, 1)
	go func() { done <- query(ctx) }()
	joined := false
	defer func() {
		c.release()
		cancel()
		if !joined {
			<-done
		}
	}()
	observe, stop := context.WithTimeout(c.p.Ctx, 5*time.Second)
	err := c.witness(observe, both)
	stop()
	c.release()
	result := <-done
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestReferenceFDWAsyncProgress(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared_%v", prepared), func(t *testing.T) {
			fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
				query := fdwAsyncQuery
				var stmt *sql.Stmt
				if prepared {
					query = "SELECT id,label FROM async_slow WHERE id >= $1 UNION ALL SELECT id,label FROM async_ready WHERE id >= $1"
					var err error
					stmt, err = c.conn.PrepareContext(c.p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				result := fdwAsyncGated(t, c, true, func(ctx context.Context) fdwAsyncResult {
					if stmt != nil {
						return fdwAsyncCollect(stmt.QueryContext(ctx, 1))
					}
					return fdwAsyncCollect(c.conn.QueryContext(ctx, query))
				})
				fdwAsyncAssert(t, result, fdwAsyncBag)
				c.idle(t, true)
				if stmt != nil {
					fdwAsyncAssert(t, fdwAsyncCollect(stmt.QueryContext(c.p.Ctx, 3)), [][]any{{int64(3), "ready"}, {int64(4), "tail"}})
					rows, err := stmt.QueryContext(c.p.Ctx, "not-an-integer")
					if rows != nil {
						_ = rows.Close()
					}
					fixture.State(t, err, "22P02")
				}
			})
		})
	}
}
func TestReferenceFDWAsyncBags(t *testing.T) {
	cases := []struct {
		name, q string
		bag     [][]any
	}{
		{"left_nested", "(" + fdwAsyncQuery + ") UNION ALL SELECT id,label FROM async_third", append(append([][]any{}, fdwAsyncBag...), []any{int64(5), "third"})},
		{"right_nested", "SELECT id,label FROM async_slow UNION ALL (SELECT id,label FROM async_ready UNION ALL SELECT id,label FROM async_third)", append(append([][]any{}, fdwAsyncBag...), []any{int64(5), "third"})},
		{"mixed_local", fdwAsyncQuery + " UNION ALL SELECT id,label FROM async_local", append(append([][]any{}, fdwAsyncBag...), []any{int64(9), "local"})},
		{"empty", "SELECT id,label FROM async_slow WHERE id<0 UNION ALL SELECT id,label FROM async_ready WHERE id<0", [][]any{}},
		{"arm_limit", "(SELECT id,label FROM async_slow ORDER BY id LIMIT 1) UNION ALL SELECT id,label FROM async_ready", [][]any{{int64(1), "slow"}, {int64(3), "ready"}, {int64(4), "tail"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
				c.release()
				c.run(t, fixture.Exec("CREATE TABLE async_local(id integer,label text)", 0))
				c.run(t, fixture.Exec("INSERT INTO async_local VALUES(9,'local')", 1))
				for _, prepared := range []bool{false, true} {
					s := fdwAsyncRows(tc.q, tc.bag)
					if prepared {
						s = fixture.Prepared(s)
					}
					c.run(t, s)
				}
				s := fixture.Select("SELECT label FROM async_slow WHERE id=2 UNION ALL SELECT label FROM async_ready WHERE id=3", []string{"label"}, []string{"TEXT"}, [][]any{{nil}, {"ready"}})
				s.Bag = true
				c.run(t, s)
				c.idle(t, true)
			})
		})
	}
}
func TestReferenceFDWAsyncOptions(t *testing.T) {
	t.Run("settings", func(t *testing.T) {
		fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
			c.release()
			c.run(t, fixture.Select("SHOW enable_async_append", []string{"enable_async_append"}, []string{"TEXT"}, [][]any{{"on"}}))
			c.run(t, fixture.Select("SELECT setting,vartype,context,boot_val,reset_val,category FROM pg_settings WHERE name='enable_async_append'", []string{"setting", "vartype", "context", "boot_val", "reset_val", "category"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"on", "bool", "user", "on", "on", "Query Tuning / Planner Method Configuration"}}))
			c.run(t, fixture.Exec("SET enable_async_append=off", 0))
			c.run(t, fdwAsyncRows(fdwAsyncQuery, fdwAsyncBag))
			c.run(t, fixture.Exec("RESET enable_async_append", 0))
			c.run(t, fixture.Exec("BEGIN", 0))
			c.run(t, fixture.Exec("SET LOCAL enable_async_append=off", 0))
			c.run(t, fixture.Exec("ROLLBACK", 0))
			c.run(t, fixture.Select("SHOW enable_async_append", []string{"enable_async_append"}, []string{"TEXT"}, [][]any{{"on"}}))
			c.run(t, fixture.ErrorDiagnostic("SET enable_async_append='invalid'", fdwDiagnostic("22023", `parameter "enable_async_append" requires a Boolean value`)))
		})
	})
	for _, tc := range []struct {
		name, server, table string
		progress            bool
	}{
		{"default_false", "false", "", false}, {"table_false", "true", "false", false}, {"table_true", "false", "true", true}, {"setting_off", "true", "", false},
		{"provider_absent", "false", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
				for _, name := range []string{"slow", "ready"} {
					c.run(t, fixture.Exec("ALTER SERVER async_"+name+"_server OPTIONS(SET async_capable '"+tc.server+"')", 0))
					if tc.table != "" {
						c.run(t, fixture.Exec("ALTER FOREIGN TABLE async_"+name+" OPTIONS(ADD async_capable '"+tc.table+"')", 0))
					}
				}
				if tc.name == "default_false" {
					for _, name := range []string{"slow", "ready"} {
						c.run(t, fixture.Exec("ALTER SERVER async_"+name+"_server OPTIONS(DROP async_capable)", 0))
					}
				}
				if tc.name == "setting_off" {
					c.run(t, fixture.Exec("SET enable_async_append=off", 0))
				}
				if tc.name == "provider_absent" && !c.b.PG {
					c.run(t, fixture.Exec("ALTER FOREIGN DATA WRAPPER async_native HANDLER fdw_async_plain_handler", 0))
				}
				result := fdwAsyncGated(t, c, tc.progress, func(ctx context.Context) fdwAsyncResult {
					return fdwAsyncCollect(c.conn.QueryContext(ctx, fdwAsyncQuery))
				})
				fdwAsyncAssert(t, result, fdwAsyncBag)
				c.idle(t, true)
			})
		})
	}
}
