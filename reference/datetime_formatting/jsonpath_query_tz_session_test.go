package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
)

func TestReferenceJSONPathQueryTZSessionZone(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec(`SET TIME ZONE 'Asia/Tokyo'`, 0))
		for _, f := range jsonpathQueryTZFunctions {
			q := `SELECT ` + f.name + `($1,$2,$3,$4) AS changed`
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, zone := range []string{"UTC", "Asia/Tokyo", "UTC"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE '`+zone+`'`, 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathQueryResult(q, f.typ, jsonpathQueryTZExpected(f.name, zone == "UTC"), jsonpathQueryTZArgs(f.name)...))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, jsonpathQueryResult(q, f.typ, jsonpathQueryTZExpected(f.name, false), jsonpathQueryTZArgs(f.name)...))
				}
				old, err := stmt.QueryContext(p.Ctx, jsonpathQueryTZArgs(f.name)...)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				if b.PG {
					fixture.StringCoreRetainedRows(t, old, nil, []string{"changed"}, []string{f.typ}, jsonpathQueryTZExpected(f.name, true), false)
					old.Close()
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE 'Asia/Tokyo'`, 0))
				if !b.PG {
					fixture.StringCoreRetainedRows(t, old, nil, []string{"changed"}, []string{f.typ}, jsonpathQueryTZExpected(f.name, true), false)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`BEGIN`, 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET LOCAL TIME ZONE 'Asia/Tokyo'`, 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathQueryResult(q, f.typ, jsonpathQueryTZExpected(f.name, false), jsonpathQueryTZArgs(f.name)...))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`ROLLBACK`, 0))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathQueryResult(q, f.typ, jsonpathQueryTZExpected(f.name, true), jsonpathQueryTZArgs(f.name)...))
				// Start both owners together, collect plain values, and assert on the test
				// goroutine. No testing Fatal or fixture recorder is used by workers.
				start := make(chan struct{})
				failures := make(chan error, 2)
				var wg sync.WaitGroup
				for _, owner := range []struct {
					conn *sql.Conn
					yes  bool
				}{{c, true}, {peer, false}} {
					wg.Add(1)
					go func(conn *sql.Conn, yes bool) {
						defer wg.Done()
						<-start
						rows, err := conn.QueryContext(p.Ctx, q, jsonpathQueryTZArgs(f.name)...)
						if err != nil {
							failures <- err
							return
						}
						defer rows.Close()
						expected := jsonpathQueryTZExpected(f.name, yes)
						i := 0
						for rows.Next() {
							var v any
							if err := rows.Scan(&v); err != nil {
								failures <- err
								return
							}
							if i >= len(expected) || fmt.Sprint(fixture.Normalize(v)) != fmt.Sprint(expected[i][0]) {
								failures <- fmt.Errorf("%s zone=%v unexpected value %v", f.name, yes, v)
								return
							}
							i++
						}
						if err := rows.Err(); err != nil {
							failures <- err
							return
						}
						if i != len(expected) {
							failures <- fmt.Errorf("%s rows=%d want=%d", f.name, i, len(expected))
						}
					}(owner.conn, owner.yes)
				}
				close(start)
				wg.Wait()
				close(failures)
				for err := range failures {
					t.Error(err)
				}
			}()
		}
	})
}

func TestReferenceJSONPathQueryTZHeldParameters(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range jsonpathQueryTZFunctions {
			q := `SELECT ` + f.name + `($1,$2,$3,$4) AS changed`
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
				want := any(true)
				if f.typ == "JSONB" {
					want = `true`
				}
				if f.name == "jsonb_path_query_array_tz" {
					want = `[true]`
				}
				good := jsonpathQueryOne(q, f.typ, want, `true`, `$`, `{}`, false)
				run(good)
				run(jsonpathNavigateError(q, "2203A", `JSON object does not contain key "x"`, `[{"x":true},{}]`, `strict $[*].x`, `{}`, false))
				prefix := want
				if f.name == "jsonb_path_exists_tz" {
					prefix = nil
				}
				run(jsonpathQueryOne(q, f.typ, prefix, `[{"x":true},{}]`, `strict $[*].x`, `{}`, true))
				run(good)
				empty := jsonpathQueryRows(nil)
				if f.set {
					empty = nil
				}
				if f.name == "jsonb_path_exists_tz" {
					empty = jsonpathQueryRows(false)
				}
				if f.name == "jsonb_path_query_array_tz" {
					empty = jsonpathQueryRows(`[]`)
				}
				run(jsonpathQueryResult(q, f.typ, empty, `{}`, `$.missing`, `{}`, true))
				run(good)
			}()
		}
	})
}
