package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// No testing calls here: every worker closes its cursor before returning a result.
func referenceViewBackingConsumer(ctx context.Context, db *sql.DB, query string) (values [][]any, err error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(columns, []string{"q", "r", "s"}) {
		return nil, fmt.Errorf("columns %v", columns)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	if len(types) != 3 {
		return nil, fmt.Errorf("metadata width %d", len(types))
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "INT4" {
			return nil, fmt.Errorf("type %s", typ.DatabaseTypeName())
		}
	}
	for rows.Next() {
		var q, r, s any
		if err := rows.Scan(&q, &r, &s); err != nil {
			return nil, err
		}
		for _, v := range []any{q, r, s} {
			if _, ok := v.(int64); !ok {
				return nil, fmt.Errorf("raw INT4 %T", v)
			}
		}
		values = append(values, []any{q, r, s})
	}
	return values, rows.Err()
}

func TestReferenceViewProjectedBackingSortGroupAndConcurrency(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			b.DB.SetMaxOpenConns(8)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(a INT,b INT)", 0), fixture.Exec("INSERT INTO t VALUES(10,3),(20,1),(30,2),(40,3)", 4),
				fixture.Exec("CREATE VIEW p(q,r,s) AS SELECT b,a,b FROM t", 0), fixture.Exec("CREATE VIEW direct AS SELECT s,r,q FROM p", 0),
				fixture.Exec("CREATE VIEW computed(s,r,q) AS SELECT s,COALESCE(r,0),q FROM direct", 0), fixture.Exec("CREATE VIEW nested AS SELECT q,r,s FROM computed", 0),
			} {
				run(s)
			}
			ascending := [][]any{{int64(3), int64(10), int64(3)}, {int64(1), int64(20), int64(1)}, {int64(2), int64(30), int64(2)}, {int64(3), int64(40), int64(3)}}
			descending := [][]any{{int64(3), int64(40), int64(3)}, {int64(2), int64(30), int64(2)}, {int64(1), int64(20), int64(1)}, {int64(3), int64(10), int64(3)}}
			columns, types := []string{"q", "r", "s"}, []string{"INT4", "INT4", "INT4"}
			run(fixture.Select("SELECT q,r,s FROM nested ORDER BY r ASC", columns, types, ascending))
			run(fixture.Select("SELECT q,r,s FROM p ORDER BY r DESC", columns, types, descending))
			run(fixture.Select("SELECT q,COUNT(*) FROM p GROUP BY q HAVING COUNT(*)>=2 ORDER BY q", []string{"q", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(2)}}))
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			referenceViewBackingRetained(t, p, b, ascending)
			query := "SELECT q,r,s FROM p ORDER BY r DESC"
			want := append([][]any{{int64(4), int64(50), int64(4)}}, descending...)
			b.Recorder.Mu.Lock()
			startIndex := len(b.Recorder.Operations)
			b.Recorder.Mu.Unlock()
			type result struct {
				values [][]any
				err    error
			}
			results := make(chan result, 8)
			start := make(chan struct{})
			for range 8 {
				go func() {
					<-start
					values, err := referenceViewBackingConsumer(ctx, b.DB, query)
					results <- result{values, err}
				}()
			}
			close(start)
			all := make([]result, 0, 8)
			for range 8 {
				all = append(all, <-results)
			}
			// All workers are joined even when any consumer failed.
			for i, result := range all {
				if result.err != nil {
					t.Errorf("consumer %d: %v", i, result.err)
				}
				if !reflect.DeepEqual(result.values, want) {
					t.Errorf("consumer %d rows=%#v want=%#v", i, result.values, want)
				}
			}
			b.Recorder.Mu.Lock()
			operations := append([]*fixture.Operation(nil), b.Recorder.Operations[startIndex:]...)
			b.Recorder.Mu.Unlock()
			seen := map[*fixture.Operation]bool{}
			count := 0
			for _, op := range operations {
				if op.SQL != query || op.Columns == nil {
					continue
				}
				count++
				if seen[op] {
					t.Error("duplicate recording")
				}
				seen[op] = true
				recorded := make([][]any, len(op.Rows))
				for i, row := range op.Rows {
					recorded[i] = make([]any, len(row))
					for j, value := range row {
						fixture.AssertRawValue(t, b.PG, "INT4", value)
						recorded[i][j] = fixture.Normalize(value)
					}
				}
				if op.Err != nil || !op.Closed || op.CloseErr != nil || !reflect.DeepEqual(op.Columns, columns) || !reflect.DeepEqual(recorded, want) {
					t.Errorf("consumer recording %#v", op)
				}
			}
			if count != 8 {
				t.Fatalf("consumer recordings=%d want 8", count)
			}
		})
	}
}

func referenceViewBackingRetained(t *testing.T, p *fixture.Pair, b fixture.Backend, want [][]any) {
	t.Helper()
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
	query := "SELECT q,r,s FROM p ORDER BY r"
	rows, err := b.DB.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"q", "r", "s"}) {
		t.Fatal(columns, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != 3 {
		t.Fatal(metadata, err)
	}
	for _, c := range metadata {
		if c.DatabaseTypeName() != "INT4" {
			t.Fatal(c)
		}
	}
	if !rows.Next() {
		t.Fatal("missing held row", rows.Err())
	}
	var q, r, s any
	if err := rows.Scan(&q, &r, &s); err != nil {
		t.Fatal(err)
	}
	held := []any{q, r, s}
	for _, v := range held {
		fixture.AssertRawValue(t, b.PG, "INT4", v)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO t VALUES(50,4)", 1))
	values := [][]any{held}
	for rows.Next() {
		var a, c, d any
		if err := rows.Scan(&a, &c, &d); err != nil {
			t.Fatal(err)
		}
		for _, v := range []any{a, c, d} {
			fixture.AssertRawValue(t, b.PG, "INT4", v)
		}
		values = append(values, []any{a, c, d})
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(held, want[0]) {
		t.Fatal("exported row changed", held)
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatal("retained cursor rows", values)
	}
	fixture.ViewRecorded(t, b.Recorder, query, []string{"q", "r", "s"}, []string{"INT4", "INT4", "INT4"}, want, false)
}
