package reference_test

import (
	"context"
	"math"
	"sync"
	"testing"
)

func floatReferenceSpecial(t *testing.T, id int64, value any) {
	t.Helper()
	if id == 9 {
		if value != nil {
			t.Fatal(value)
		}
		return
	}
	n, ok := value.(float64)
	if !ok {
		t.Fatalf("raw REAL %T", value)
	}
	if id == 7 || id == 8 {
		if !math.IsNaN(n) {
			t.Fatal(n)
		}
		return
	}
	want := map[int64]float64{1: math.Inf(-1), 2: -1, 3: math.Copysign(0, -1), 4: 0, 5: 1, 6: math.Inf(1)}[id]
	if math.Float64bits(n) != math.Float64bits(want) {
		t.Fatal(id, n, want)
	}
}

func TestReferenceFloatSpecialOrdering(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE ordered(id INTEGER,r REAL)", 0))
	for _, backend := range p.backends() {
		func() {
			q := "INSERT INTO ordered VALUES($1,$2)"
			insert, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: insert, query: q}
			for i, value := range []any{math.Inf(-1), float64(-1), math.Copysign(0, -1), float32(0), float64(1), math.Inf(1), math.NaN(), "nan", nil} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, i+1, value))
			}
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			for _, descending := range []bool{false, true} {
				q := "SELECT id,r FROM ordered ORDER BY r"
				groups := []int{0, 1, 2, 2, 3, 4, 5, 5, 6}
				if descending {
					q += " DESC"
					groups = []int{6, 5, 5, 4, 3, 2, 2, 1, 0}
				}
				rows, err := backend.db.QueryContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				seen, index := map[int64]bool{}, 0
				for rows.Next() {
					var id int64
					var value any
					if err := rows.Scan(&id, &value); err != nil {
						t.Fatal(err)
					}
					group, valid := map[int64]int{1: 0, 2: 1, 3: 2, 4: 2, 5: 3, 6: 4, 7: 5, 8: 5, 9: 6}[id]
					if !valid || seen[id] || index >= 9 || group != groups[index] {
						t.Fatal(index, id, value)
					}
					seen[id] = true
					floatReferenceSpecial(t, id, value)
					index++
				}
				if rows.Err() != nil || index != 9 {
					t.Fatal(index, rows.Err())
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				backend.recorder.mu.Lock()
				var captured *referenceOperation
				for _, op := range backend.recorder.operations {
					if op.SQL == q && op.Columns != nil {
						captured = op
					}
				}
				backend.recorder.mu.Unlock()
				if captured == nil || len(captured.Rows) != 9 || !captured.Closed || captured.CloseErr != nil {
					t.Fatal("missing owned special float observation")
				}
				seen = map[int64]bool{}
				for i, row := range captured.Rows {
					referenceAssertRawValue(t, backend.pg, "INT4", row[0])
					id := row[0].(int64)
					group, valid := map[int64]int{1: 0, 2: 1, 3: 2, 4: 2, 5: 3, 6: 4, 7: 5, 8: 5, 9: 6}[id]
					if !valid || seen[id] || group != groups[i] {
						t.Fatal("recorded peer groups", row)
					}
					seen[id] = true
					floatReferenceSpecial(t, id, row[1])
				}
			}
			for _, step := range []referenceStep{
				referenceSelect("SELECT id FROM ordered WHERE r='NaN' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}),
				referenceSelect("SELECT r FROM ordered WHERE r=NULL", []string{"r"}, []string{"FLOAT4"}, nil),
				referenceSelect("SELECT r FROM ordered WHERE r=$1", []string{"r"}, []string{"FLOAT4"}, nil, nil),
			} {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(step))
			}
			start, failures := make(chan struct{}), make(chan error, 8)
			var wg sync.WaitGroup
			for i := range 8 {
				wg.Go(func() {
					<-start
					result, err := insert.ExecContext(ctx, i+20, float64(i))
					if err == nil {
						count, e := result.RowsAffected()
						err = e
						if count != 1 {
							t.Errorf("count=%d", count)
						}
					}
					failures <- err
				})
			}
			close(start)
			wg.Wait()
			close(failures)
			for err := range failures {
				if err != nil {
					t.Fatal(err)
				}
			}
			want := make([][]any, 8)
			for i := range want {
				want[i] = []any{int64(i + 20), float64(i)}
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT id,r FROM ordered WHERE id>=20 ORDER BY id", []string{"id", "r"}, []string{"INT4", "FLOAT4"}, want))
		}()
	}
}
