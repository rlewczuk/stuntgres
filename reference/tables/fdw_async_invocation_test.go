package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const fdwAsyncCorrelatedIDs = "SELECT id FROM async_slow WHERE id=o.id UNION ALL SELECT id FROM async_ready WHERE id=o.id"
const fdwAsyncExists = "SELECT o.seq,o.id FROM async_outer o WHERE o.id >= $1 AND EXISTS (" + fdwAsyncCorrelatedIDs + ")"

func fdwAsyncOuter(t *testing.T, c *fdwAsyncCase) {
	t.Helper()
	c.release()
	c.run(t, fixture.Exec("CREATE TABLE async_outer(seq integer,id integer)", 0))
	values := make([]string, 24)
	for i := range values {
		values[i] = fmt.Sprintf("(%d,%d)", i+1, []int{3, 4, 9}[i%3])
	}
	c.run(t, fixture.Exec("INSERT INTO async_outer VALUES "+strings.Join(values, ","), 24))
}
func fdwAsyncInvocationBag(threshold int, scalar bool) [][]any {
	var rows [][]any
	for i := range 24 {
		id := []int{3, 4, 9}[i%3]
		if id < threshold {
			continue
		}
		if scalar {
			var matched any
			if id != 9 {
				matched = int64(id)
			}
			rows = append(rows, []any{int64(i + 1), matched})
		} else if id != 9 {
			rows = append(rows, []any{int64(i + 1), int64(id)})
		}
	}
	return rows
}
func fdwAsyncInvocationRows(query string, threshold int, scalar bool) fixture.Step {
	columns := []string{"seq", "id"}
	if scalar {
		columns[1] = "matched"
	}
	step := fixture.Select(query, columns, []string{"INT4", "INT4"}, fdwAsyncInvocationBag(threshold, scalar), threshold)
	step.Bag = true
	return step
}
func fdwAsyncRepeated(t *testing.T, c *fdwAsyncCase, query string, scalar bool) {
	t.Helper()
	c.run(t, fdwAsyncInvocationRows(query, 3, scalar))
	c.invocationResources(t, 2, true)
	stmt, err := c.conn.PrepareContext(c.p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, threshold := range []int{3, 4, 3} {
		fixture.RunStep(t, c.p.Ctx, fixture.HeldStatement{Executor: c.conn, Statement: stmt, SQL: query}, c.b.Recorder, fdwAsyncInvocationRows(query, threshold, scalar))
		c.invocationResources(t, 2, true)
	}
}

func TestReferenceFDWAsyncInvocations(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		scalar      bool
	}{
		{"exists", fdwAsyncExists, false},
		{"derived", "SELECT o.seq,o.id FROM async_outer o WHERE o.id >= $1 AND EXISTS (SELECT id FROM (" + fdwAsyncCorrelatedIDs + ") d)", false},
		{"view", "SELECT o.seq,o.id FROM async_outer o WHERE o.id >= $1 AND EXISTS (SELECT id FROM async_invocation_view v WHERE v.id=o.id)", false},
		{"materialized_cte", "SELECT o.seq,o.id FROM async_outer o WHERE o.id >= $1 AND EXISTS (WITH invocation_rows AS MATERIALIZED (" + fdwAsyncCorrelatedIDs + ") SELECT id FROM invocation_rows)", false},
		{"scalar", "SELECT o.seq,(" + fdwAsyncCorrelatedIDs + ") AS matched FROM async_outer o WHERE o.id >= $1", true},
		{"membership", "SELECT o.seq,o.id FROM async_outer o WHERE o.id >= $1 AND o.id IN (" + fdwAsyncCorrelatedIDs + ")", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
				fdwAsyncOuter(t, c)
				if tc.name == "view" {
					c.run(t, fixture.Exec("CREATE VIEW async_invocation_view AS "+fdwAsyncQuery, 0))
				}
				fdwAsyncRepeated(t, c, tc.query, tc.scalar)
			})
		})
	}
	t.Run("tuple_update", func(t *testing.T) {
		fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
			fdwAsyncOuter(t, c)
			c.run(t, fixture.Exec("CREATE TABLE async_invocation_target(seq integer,id integer,matched integer,label text)", 0))
			c.run(t, fixture.Exec("INSERT INTO async_invocation_target SELECT seq,id,-1,'untouched' FROM async_outer", 24))
			query := "UPDATE async_invocation_target t SET (matched,label)=(SELECT id,label FROM async_slow WHERE id=t.id UNION ALL SELECT id,label FROM async_ready WHERE id=t.id) WHERE t.id >= $1"
			check := func(threshold int) {
				var want [][]any
				for i := range 24 {
					id := []int{3, 4, 9}[i%3]
					var matched, label any = int64(-1), "untouched"
					if id >= threshold {
						if id == 9 {
							matched, label = nil, nil
						} else {
							matched = int64(id)
							label = map[int]string{3: "ready", 4: "tail"}[id]
						}
					}
					want = append(want, []any{int64(i + 1), int64(id), matched, label})
				}
				step := fixture.Select("SELECT seq,id,matched,label FROM async_invocation_target", []string{"seq", "id", "matched", "label"}, []string{"INT4", "INT4", "INT4", "TEXT"}, want)
				step.Bag = true
				c.run(t, step)
				c.invocationResources(t, 2, true)
			}
			c.run(t, fixture.Exec(query, 24, 3))
			check(3)
			stmt, err := c.conn.PrepareContext(c.p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, threshold := range []int{3, 4, 3} {
				c.run(t, fixture.Exec("UPDATE async_invocation_target SET matched=-1,label='untouched'", 24))
				affected := int64(24)
				if threshold == 4 {
					affected = 16
				}
				fixture.RunStep(t, c.p.Ctx, fixture.HeldStatement{Executor: c.conn, Statement: stmt, SQL: query}, c.b.Recorder, fixture.Exec(query, affected, threshold))
				check(threshold)
			}
		})
	})
}
