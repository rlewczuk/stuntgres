package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const preparedFoldPointSQL = "SELECT (point '0.10000000000000002,1')::text AS p"
const preparedFoldLengthSQL = "SELECT length((point '0.10000000000000002,1')::text) AS n"

// One backend loop is shared by the point scenarios, including oracle discovery.
// There is deliberately no permanent PostgreSQL-only filter.
func preparedFoldScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "fold_path_a", "fold_path_b")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.Close() })
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "DEALLOCATE ALL", "RESET extra_float_digits", "RESET search_path"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			body(t, p, b, c)
		})
	}
}

func preparedFoldHold(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, route, name, query string) preparedRebindRoute {
	t.Helper()
	if route == "sql" {
		sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE "+name+" AS "+query, 0))
		return preparedRebindRoute{Executor: c, SQL: "EXECUTE " + name}
	}
	s, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return preparedRebindRoute{Executor: fixture.HeldStatement{Executor: c, Statement: s, SQL: query}, SQL: query}
}

func preparedFoldRead(t *testing.T, p *fixture.Pair, b fixture.Backend, r preparedRebindRoute, nested, long bool) {
	t.Helper()
	name, typ, value := "p", "TEXT", any("(0.1,1)")
	if long {
		value = "(0.10000000000000002,1)"
	}
	if nested {
		name, typ, value = "n", "INT4", int64(7)
		if long {
			value = int64(23)
		}
	}
	preparedRebindRead(t, p, b, r, []string{name}, []string{typ}, [][]any{{value}})
}

func TestReferencePreparedFoldPointLifetime(t *testing.T) {
	for _, route := range []string{"go", "sql"} {
		for _, nested := range []bool{false, true} {
			label, query := "text", preparedFoldPointSQL
			if nested {
				label, query = "nested", preparedFoldLengthSQL
			}
			t.Run(route+"/"+label+"/before_first_execute", func(t *testing.T) {
				preparedFoldScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					sqlPreparedStep(t, p, b, c, fixture.Exec("SET extra_float_digits TO 3", 0))
					r := preparedFoldHold(t, p, b, c, route, "fold_first", query)
					sqlPreparedStep(t, p, b, c, fixture.Exec("SET extra_float_digits TO 0", 0))
					preparedFoldRead(t, p, b, r, nested, false)
				})
			})
			t.Run(route+"/"+label+"/warm_and_fresh", func(t *testing.T) {
				preparedFoldScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					sqlPreparedStep(t, p, b, c, fixture.Exec("SET extra_float_digits TO 3", 0))
					r := preparedFoldHold(t, p, b, c, route, "fold_warm", query)
					preparedFoldRead(t, p, b, r, nested, true)
					sqlPreparedStep(t, p, b, c, fixture.Exec("SET extra_float_digits TO 0", 0))
					preparedFoldRead(t, p, b, r, nested, true)
					preparedFoldRead(t, p, b, preparedRebindRoute{Executor: c, SQL: query}, nested, false)
					fresh := preparedFoldHold(t, p, b, c, route, "fold_fresh", query)
					preparedFoldRead(t, p, b, fresh, nested, false)
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { peer.Close() })
					// A distinct physical session and definition cannot borrow r's fold.
					sqlPreparedStep(t, p, b, peer, fixture.Exec("SET extra_float_digits TO 0", 0))
					peerRoute := preparedFoldHold(t, p, b, peer, "go", "", query)
					preparedFoldRead(t, p, b, peerRoute, nested, false)
					preparedFoldRead(t, p, b, r, nested, true)
				})
			})
		}
		t.Run(route+"/runtime_operand", func(t *testing.T) {
			preparedFoldScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				query := "SELECT ($1::point)::text AS p"
				var r preparedRebindRoute
				if route == "sql" {
					sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE fold_parameter(point) AS "+query, 0))
					r = preparedRebindRoute{Executor: c, SQL: "EXECUTE fold_parameter('0.10000000000000002,1')"}
				} else {
					r = preparedFoldHold(t, p, b, c, route, "", query)
				}
				for i := range 8 {
					setting, value := "3", "(0.10000000000000002,1)"
					if i%2 != 0 {
						setting, value = "0", "(0.1,1)"
					}
					sqlPreparedStep(t, p, b, c, fixture.Exec("SET extra_float_digits TO "+setting, 0))
					var args []any
					if route == "go" {
						args = []any{"0.10000000000000002,1"}
					}
					preparedRebindRead(t, p, b, r, []string{"p"}, []string{"TEXT"}, [][]any{{value}}, args...)
				}
			})
		})
	}
}
