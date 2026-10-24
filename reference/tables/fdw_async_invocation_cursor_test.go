package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwAsyncIntegerBag(t *testing.T, result fdwAsyncResult, want [][]any) {
	t.Helper()
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !reflect.DeepEqual(result.columns, []string{"seq", "id"}) || !reflect.DeepEqual(result.types, []string{"INT4", "INT4"}) {
		t.Fatalf("invocation cursor metadata %v %v", result.columns, result.types)
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
		t.Fatalf("invocation cursor bag got %#v want %#v", result.rows, want)
	}
}

func TestReferenceFDWAsyncInvocationCursor(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(fmt.Sprintf("invocation_early_close_%v", early), func(t *testing.T) {
			fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
				fdwAsyncOuter(t, c)
				for _, threshold := range []int{3, 4, 3} {
					c.run(t, fixture.Exec("BEGIN", 0))
					c.run(t, fixture.Exec("DECLARE async_invocation_cursor NO SCROLL CURSOR FOR "+strings.ReplaceAll(fdwAsyncExists, "$1", fmt.Sprint(threshold)), 0))
					var all [][]any
					var metadata fdwAsyncResult
					fetch := func(count string) int {
						result := fdwAsyncCollect(c.conn.QueryContext(c.p.Ctx, "FETCH "+count+" FROM async_invocation_cursor"))
						if result.err != nil {
							t.Fatal(result.err)
						}
						if !reflect.DeepEqual(result.columns, []string{"seq", "id"}) || !reflect.DeepEqual(result.types, []string{"INT4", "INT4"}) {
							t.Fatalf("FETCH metadata %v %v", result.columns, result.types)
						}
						metadata = result
						all = append(all, result.rows...)
						c.invocationResources(t, 2, false)
						return len(result.rows)
					}
					if n := fetch("1"); n != 1 {
						t.Fatalf("FETCH 1 returned %d rows", n)
					}
					first := fmt.Sprintf("%#v", all[:1])
					if !early {
						if n := fetch("2"); n != 2 {
							t.Fatalf("FETCH 2 returned %d rows", n)
						}
						for fetch("3") != 0 {
						}
						if fmt.Sprintf("%#v", all[:1]) != first {
							t.Fatal("prior finite batch mutated")
						}
						metadata.rows = all
						fdwAsyncIntegerBag(t, metadata, fdwAsyncInvocationBag(threshold, false))
					}
					c.run(t, fixture.Exec("CLOSE async_invocation_cursor", 0))
					c.invocationResources(t, 2, true)
					c.run(t, fixture.Exec("ROLLBACK", 0))
				}
			})
		})
	}
	t.Run("safe_root_cursor_progress", func(t *testing.T) {
		fdwAsyncScenario(t, func(t *testing.T, c *fdwAsyncCase) {
			c.run(t, fixture.Exec("BEGIN", 0))
			c.run(t, fixture.Exec("DECLARE async_safe_cursor NO SCROLL CURSOR FOR "+fdwAsyncQuery, 0))
			first := fdwAsyncGated(t, c, true, func(ctx context.Context) fdwAsyncResult {
				return fdwAsyncCollect(c.conn.QueryContext(ctx, "FETCH 1 FROM async_safe_cursor"))
			})
			if first.err != nil || len(first.rows) != 1 {
				t.Fatalf("safe cursor first batch %v %v", first.rows, first.err)
			}
			if !c.b.PG {
				state := c.reader.resources()
				if state.peakAsync == 0 || state.peakAsync > 2 || state.peakScans > 2 || state.peakCursors > 2 || state.requests != 0 || state.handlers != 0 || state.asynchronous != 0 {
					t.Fatalf("safe retained cursor lost async progress/quiescence: %+v", state)
				}
			}
			snapshot := fmt.Sprintf("%#v", first.rows)
			rest := fdwAsyncCollect(c.conn.QueryContext(c.p.Ctx, "FETCH ALL FROM async_safe_cursor"))
			if rest.err != nil {
				t.Fatal(rest.err)
			}
			if fmt.Sprintf("%#v", first.rows) != snapshot {
				t.Fatal("safe cursor prior batch mutated")
			}
			first.rows = append(first.rows, rest.rows...)
			fdwAsyncAssert(t, first, fdwAsyncBag)
			c.run(t, fixture.Exec("CLOSE async_safe_cursor", 0))
			c.run(t, fixture.Exec("ROLLBACK", 0))
			c.idle(t, true)
		})
	})
}
