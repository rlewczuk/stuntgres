package enginetests_test

import (
	"context"
	"math"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestSystemTimeVisibleSelection(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_time")
	query, captured := enginetest.QueryFixture(t, e, "SELECT id FROM t TABLESAMPLE system_time(1)")
	leaf := query.Root.(*plan.ProjectNode).Child.(*plan.SourceNode)
	original := leaf.Sample
	check := func(source *exec.Source, allowed map[int64]bool, millis float64) {
		t.Helper()
		node, sample := *leaf, *original
		value, err := sqltype.Float(original.Argument.ResultUsage().Type, millis)
		if err != nil {
			t.Fatal(err)
		}
		sample.Argument = plan.LiteralExpr{Info: expr.Info{Usage: original.Argument.ResultUsage()}, Value: value}
		node.Sample, node.Ref = &sample, source.Ref
		visible, ends := slices.Clone(source.Visible), slices.Clone(source.InheritanceEnds)
		stage, err := exec.OpenNode(context.Background(), &exec.State{Source: source}, &node)
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		if stage.Source != source {
			t.Fatal("sampler copied source payloads")
		}
		if millis == 0 && stage.Count() != 0 || math.IsInf(millis, 1) && stage.Count() != len(allowed) {
			t.Fatal("zero/infinity cardinality", millis, stage.Count(), len(allowed))
		}
		seen := make(map[int64]bool)
		for i := range stage.Count() {
			id, err := stage.Source.Row(stage.Index(i))[0].Integer()
			if err != nil || !allowed[id] || seen[id] {
				t.Fatal("dead, duplicate or foreign occurrence", id, err)
			}
			seen[id] = true
			// Index still addresses the original source, including its MVCC head.
			if stage.Source.Row(stage.Index(i))[0] != source.Row(stage.Index(i))[0] {
				t.Fatal("physical identity changed")
			}
		}
		selection := slices.Clone(stage.Selection)
		if changed, err := stage.Rewind(context.Background()); changed || err != nil || !slices.Equal(selection, stage.Selection) {
			t.Fatal("unchanged invocation resampled", changed, err)
		}
		if !reflect.DeepEqual(visible, source.Visible) || !slices.Equal(ends, source.InheritanceEnds) || leaf.Sample != original {
			t.Fatal("sampling mutated accepted source/plan backing")
		}
		if err := stage.Close(); err != nil || stage.Source != nil || stage.Selection != nil || stage.CacheOccurrences != nil || stage.OrderValues != nil || stage.Reset != nil || stage.Finish != nil || stage.Produce != nil {
			t.Fatal("closed sampler retained invocation", err)
		}
	}
	for _, millis := range []float64{0, math.Copysign(0, -1), 0.001, 1, math.MaxFloat64, math.Inf(1)} {
		check(captured, map[int64]bool{1: true, 2: true, 3: true, 4: true}, millis)
	}
	session := enginetest.StartSession(t, e, nil)
	for _, sql := range []string{"BEGIN", "DELETE FROM t WHERE id=2", "UPDATE t SET v=99 WHERE id=3", "INSERT INTO t VALUES(5,5,TRUE)"} {
		enginetest.SessionSQL(t, session, sql)
	}
	private := enginetest.SessionSQL(t, session, "SELECT * FROM t").Rows.(*exec.FinalRows)
	defer private.Close()
	for _, millis := range []float64{0, 0.25, math.Inf(1)} {
		check(private.Source, map[int64]bool{1: true, 3: true, 4: true, 5: true}, millis)
	}
	enginetest.SessionSQL(t, session, "ROLLBACK")
	if captured.Count() != 4 {
		t.Fatal("private scan changed committed snapshot")
	}
	check(&exec.Source{View: captured.View, Ref: captured.Ref, Anonymous: [][]sqltype.Value{}}, map[int64]bool{}, math.Inf(1))
	if _, _, eligible := exec.ParallelQueryPlan(query.Root, captured); eligible {
		t.Fatal("sampling split across workers")
	}
	if exec.CursorSimpleScan(query.Root, captured) != nil {
		t.Fatal("sampling bypassed by direct cursor")
	}
	for _, sql := range []string{
		"CREATE TABLE time_prefix(prefix text)",
		"CREATE TABLE time_child(extra bigint) INHERITS(time_prefix,t)",
		"CREATE TABLE time_other() INHERITS(t)",
		"INSERT INTO time_child(prefix,id,v,f,extra) VALUES('a',11,1,true,1),('b',12,2,false,2)",
		"INSERT INTO time_other VALUES(21,1,true),(22,2,false)",
	} {
		enginetest.Run(t, e, sql)
	}
	hierarchy := enginetest.Run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
	defer hierarchy.Close()
	if len(hierarchy.Source.Inheritance) != 3 {
		t.Fatal("missing physical source groups")
	}
	for _, millis := range []float64{0, 0.001, math.Inf(1)} {
		check(hierarchy.Source, map[int64]bool{1: true, 2: true, 3: true, 4: true, 11: true, 12: true, 21: true, 22: true}, millis)
	}
}
