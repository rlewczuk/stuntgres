package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Each worker owns its connection/cursor; the parent joins both even on failure.
// Independent expected streams distinguish cursor state accidentally shared by
// separate executions of the same builtin.
func jsonElementsConcurrent(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, f := range []string{"json_array_elements", "json_array_elements_text"} {
		ctx, cancel := context.WithCancel(p.Ctx)
		start := make(chan struct{})
		type outcome struct {
			id     int
			values []string
			err    error
		}
		done := make(chan outcome, 2)
		for id, input := range []string{`[1,2,3]`, `[7,8]`} {
			go func(id int, input string) {
				out := outcome{id: id}
				defer func() { done <- out }()
				conn, err := b.DB.Conn(ctx)
				if err != nil {
					out.err = err
					return
				}
				defer conn.Close()
				select {
				case <-start:
				case <-ctx.Done():
					out.err = ctx.Err()
					return
				}
				rows, err := conn.QueryContext(ctx, "SELECT value FROM "+f+"($1) WITH ORDINALITY ORDER BY ordinality", input)
				if err != nil {
					out.err = err
					return
				}
				defer rows.Close()
				for rows.Next() {
					var v string
					if err := rows.Scan(&v); err != nil {
						out.err = err
						return
					}
					out.values = append(out.values, v)
				}
				out.err = rows.Err()
			}(id, input)
		}
		close(start)
		var failures []string
		want := [][]string{{"1", "2", "3"}, {"7", "8"}}
		for range 2 {
			out := <-done
			if out.err != nil || !reflect.DeepEqual(out.values, want[out.id]) {
				failures = append(failures, fmt.Sprintf("%s worker%d=%v err=%v", f, out.id, out.values, out.err))
				cancel()
			}
		}
		cancel()
		if len(failures) != 0 {
			t.Fatal(failures)
		}
	}
}
