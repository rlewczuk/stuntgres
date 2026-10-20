package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowRankDescriptors(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT id,rank() OVER w AS r,dense_rank() OVER w AS d,percent_rank() OVER w AS pr,cume_dist() OVER w AS cd FROM t WINDOW w AS(ORDER BY v ROWS CURRENT ROW)")
	schema := query.Root.Schema()
	if len(schema) != 5 || schema[0].OriginTable == 0 || schema[0].OriginAttribute != 1 {
		t.Fatal("source origin or exposed width", schema)
	}
	for i, column := range schema[1:] {
		want := sqltype.Int8
		if i >= 2 {
			want = sqltype.Float8
		}
		if column.Usage.Type.ID() != want || column.Usage.Typmod != -1 || column.Nullable || !column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 {
			t.Fatal("ranking descriptor", i, column)
		}
	}
	node := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if len(node.Calls) != 4 {
		t.Fatal("execution calls", len(node.Calls))
	}
	for _, call := range node.Calls {
		if !call.Operation.Ranking() || call.Operation.FrameDependent() || call.Operation.EvaluatesArgument() || call.Argument != nil || call.Ordinal != nil || call.DefaultValue != nil {
			t.Fatal("ranking demand", call)
		}
	}
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil || rows == nil || count != 4 || source.view != nil {
		t.Fatal(rows, count, err, source.view)
	}
	rows.Close()
	if len(rows.Schema()) != 5 || rows.Schema()[3].Name != "pr" || rows.Schema()[3].Usage.Type.ID() != sqltype.Float8 {
		t.Fatal("metadata lifetime", rows.Schema())
	}
}

func TestQueryWindowRankPeerDemand(t *testing.T) {
	for _, frame := range []string{"ROWS CURRENT ROW", "ROWS NULL PRECEDING", "ROWS -1 PRECEDING", "RANGE CURRENT ROW EXCLUDE GROUP"} {
		t.Run(frame, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT rank() OVER w,dense_rank() OVER w,percent_rank() OVER w,cume_dist() OVER w FROM t WINDOW w AS(ORDER BY v "+frame+")")
			rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
			if err != nil || rows == nil || count != 4 {
				t.Fatal(rows, count, err)
			}
			defer rows.Close()
			values := make([]sqltype.Value, 4)
			for i, want := range [][4]float64{{1, 1, 0, .5}, {4, 3, 1, 1}, {3, 2, float64(2) / 3, .75}, {1, 1, 0, .5}} {
				present, err := rows.Next(context.Background(), values)
				if !present || err != nil {
					t.Fatal(i, present, err)
				}
				for column, v := range values {
					var got float64
					if column < 2 {
						n, e := v.Integer()
						err = e
						got = float64(n)
					} else {
						got, err = v.Float()
					}
					if err != nil || got != want[column] {
						t.Fatal(i, column, got, want[column], err)
					}
				}
			}
			if present, err := rows.Next(context.Background(), values); present || err != nil {
				t.Fatal("extra output", present, err)
			}
		})
	}
	// Equivalent key sorts can share peer endpoints while distinct frame specs
	// retain their own membership and bound-validation demand.
	_, query, source := queryCoreFixture(t, "SELECT rank() OVER(ORDER BY v ROWS CURRENT ROW),dense_rank() OVER(ORDER BY v RANGE CURRENT ROW),count(*) OVER(ORDER BY v ROWS CURRENT ROW EXCLUDE GROUP) FROM t")
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil || rows == nil || count != 4 {
		t.Fatal(rows, count, err)
	}
	values := make([]sqltype.Value, 3)
	for i, want := range [][3]int64{{1, 1, 0}, {4, 3, 0}, {3, 2, 0}, {1, 1, 0}} {
		present, err := rows.Next(context.Background(), values)
		if !present || err != nil {
			rows.Close()
			t.Fatal(i, present, err)
		}
		for column, v := range values {
			got, err := v.Integer()
			if err != nil || got != want[column] {
				rows.Close()
				t.Fatal(i, column, got, want[column], err)
			}
		}
	}
	rows.Close()
	for _, offset := range []string{"NULL", "-1"} {
		_, query, source := queryCoreFixture(t, "SELECT rank() OVER w,count(*) OVER w FROM t WINDOW w AS(ORDER BY v ROWS "+offset+" PRECEDING)")
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
		code, message := "22004", "frame starting offset must not be null"
		if offset == "-1" {
			code, message = "22013", "frame starting offset must not be negative"
		}
		var diagnostic *sqlstate.Error
		if rows != nil || count != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || source.view != nil {
			t.Fatal(rows, count, err, source.view)
		}
	}
}

func TestQueryWindowRankCancellation(t *testing.T) {
	for _, phase := range []string{"sort", "peer"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT rank() OVER w,dense_rank() OVER w,percent_rank() OVER w,cume_dist() OVER w FROM t WINDOW w AS(ORDER BY v ROWS CURRENT ROW)")
			continuation, err := captureQuerySource(context.Background(), source.view, source.ref)
			if err != nil {
				t.Fatal(err)
			}
			defer continuation.close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &windowCancelContext{Context: ctx, cancel: cancel, phase: phase}
			rows, count, err := finalizeQuery(probe, query.Root, source, nil)
			if !probe.entered || !errors.Is(err, context.Canceled) || rows != nil || count != 0 || source.view != nil {
				t.Fatal(probe.entered, rows, count, err, source.view)
			}
			recovered, recoveredCount, recoveryErr := finalizeQuery(context.Background(), query.Root, continuation, nil)
			if recoveryErr != nil || recovered == nil || recoveredCount != 4 {
				t.Fatal(recovered, recoveredCount, recoveryErr)
			}
			recovered.Close()
		})
	}
}
