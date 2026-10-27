package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeDescribePreparedLifecycle(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, q := range []string{"CREATE SCHEMA td", "CREATE DOMAIN td.original AS integer", "CREATE DOMAIN public.td_other AS text"} {
			run(fixture.Exec(q, 0))
		}
		q := "SELECT format_type($1::oid,$2::integer) AS label"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			id   any
			mod  any
			want any
		}{{int64(23), nil, "integer"}, {int64(1700), int64(327686), "numeric(5,2)"}, {nil, nil, nil}, {int64(0), nil, "-"}} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"label"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.id, tc.mod))
		}
		// Real concurrent executions of the same public held handle own separate frames.
		results := make(chan error, 2)
		for _, tc := range []struct {
			id   int64
			want string
		}{{23, "integer"}, {25, "text"}} {
			go func(id int64, want string) {
				var got string
				err := stmt.QueryRowContext(p.Ctx, id, nil).Scan(&got)
				if err == nil && got != want {
					err = fmt.Errorf("concurrent label=%q want %q", got, want)
				}
				results <- err
			}(tc.id, tc.want)
		}
		for range 2 {
			if err := <-results; err != nil {
				t.Error(err)
			}
		}
		if t.Failed() {
			return
		}
		pathQuery := "SELECT format_type('td.original'::regtype::oid,NULL) AS label"
		held, err := c.PrepareContext(p.Ctx, pathQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: pathQuery}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(pathQuery, []string{"label"}, []string{"TEXT"}, [][]any{{"td.original"}}))
		run(fixture.Exec("SET search_path TO td,public", 0))
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(pathQuery, []string{"label"}, []string{"TEXT"}, [][]any{{"original"}}))
		run(fixture.Exec("SET search_path TO public", 0))
		if b.PG {
			return
		} // pq streams; native eagerly owns detached rows and names.
		id := typeDescribeOID(t, p.Ctx, c, "td.original")
		bytes := []byte(fmt.Sprint(id))
		old, err := stmt.QueryContext(p.Ctx, bytes, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		columns, err := old.Columns()
		if err != nil {
			t.Fatal(err)
		}
		columns[0] = "mutated"
		again, err := old.Columns()
		if err != nil || again[0] != "label" {
			t.Fatalf("detached columns=%v err=%v", again, err)
		}
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		detached, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		detached[0] = nil
		fresh, err := old.ColumnTypes()
		if err != nil || fresh[0] == nil || fresh[0].Name() != "label" {
			t.Fatalf("detached metadata=%v err=%v", fresh, err)
		}
		for i := range bytes {
			bytes[i] = '0'
		}
		// A different connection changes catalog presentation while the old cursor is retained.
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ALTER DOMAIN td.original RENAME TO replacement", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"label"}, []string{"TEXT"}, [][]any{{"td.replacement"}}, id, nil))
		if !old.Next() {
			t.Fatal("retained cursor", old.Err())
		}
		var label string
		if err := old.Scan(&label); err != nil {
			t.Fatal(err)
		}
		if label != "td.original" {
			t.Fatal("retained name changed", label)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained EOF", old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		if metadata[0].Name() != "label" || metadata[0].DatabaseTypeName() != "TEXT" {
			t.Fatal("retained metadata", metadata)
		}
	})
}
