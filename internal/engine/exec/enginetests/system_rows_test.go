package enginetests_test

import (
	"context"
	"math"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestSystemRowsVisibleSelection(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_rows")
	query, captured := enginetest.QueryFixture(t, e, "SELECT id FROM t TABLESAMPLE system_rows(3)")
	leaf := query.Root.(*plan.ProjectNode).Child.(*plan.SourceNode)
	original := leaf.Sample
	check := func(source *exec.Source, allowed map[int64]bool, count int64) {
		t.Helper()
		node, sample := *leaf, *original
		value, err := sqltype.Integer(original.Argument.ResultUsage().Type, count)
		if err != nil {
			t.Fatal(err)
		}
		sample.Argument = plan.LiteralExpr{Info: expr.Info{Usage: original.Argument.ResultUsage()}, Value: value}
		node.Sample = &sample
		node.Ref = source.Ref
		visible := slices.Clone(source.Visible)
		stage, err := exec.OpenNode(context.Background(), &exec.State{Source: source}, &node)
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		want := min(int64(len(allowed)), count)
		if source.Inheritance != nil {
			want = 0
			for _, segment := range source.Inheritance {
				want += min(int64(segment.Prefix.Count()), count)
			}
		}
		if int64(stage.Count()) != want {
			t.Fatal("wrong visible sample cardinality", stage.Count(), count, want)
		}
		seen := make(map[int64]bool)
		groups := make(map[ids.ObjectRef]int64)
		for i := range stage.Count() {
			id, err := stage.Source.Row(stage.Index(i))[0].Integer()
			if err != nil || !allowed[id] || seen[id] {
				t.Fatal("dead, duplicated or foreign visible occurrence", id, err)
			}
			seen[id] = true
			groups[stage.Source.PhysicalRef(stage.Index(i))]++
		}
		for _, segment := range source.Inheritance {
			if got, want := groups[segment.Leaf.Ref], min(int64(segment.Prefix.Count()), count); got != want {
				t.Fatal("physical child did not receive its own reservoir", got, want, segment.Leaf.Ref)
			}
		}
		selection := slices.Clone(stage.Selection)
		if changed, err := stage.Rewind(context.Background()); changed || err != nil || !slices.Equal(selection, stage.Selection) {
			t.Fatal("unchanged scan resampled", changed, err)
		}
		if !reflect.DeepEqual(source.Visible, visible) || leaf.Sample != original {
			t.Fatal("sample changed accepted backing")
		}
		if err := stage.Close(); err != nil || stage.Source != nil || stage.Selection != nil || stage.Reset != nil || stage.Finish != nil {
			t.Fatal("closed selection retained live backing", err)
		}
	}
	for _, count := range []int64{0, 1, 3, 4, 5, math.MaxInt64} {
		check(captured, map[int64]bool{1: true, 2: true, 3: true, 4: true}, count)
	}
	session := enginetest.StartSession(t, e, nil)
	for _, sql := range []string{"BEGIN", "DELETE FROM t WHERE id=2", "UPDATE t SET v=99 WHERE id=3", "INSERT INTO t VALUES(5,5,TRUE)"} {
		enginetest.SessionSQL(t, session, sql)
	}
	private := enginetest.SessionSQL(t, session, "SELECT * FROM t").Rows.(*exec.FinalRows)
	defer private.Close()
	for _, count := range []int64{0, 1, 3, math.MaxInt64} {
		check(private.Source, map[int64]bool{1: true, 3: true, 4: true, 5: true}, count)
	}
	enginetest.SessionSQL(t, session, "ROLLBACK")
	if captured.Count() != 4 {
		t.Fatal("private sampling changed committed source")
	}
	empty := &exec.Source{View: captured.View, Ref: captured.Ref, Anonymous: [][]sqltype.Value{}}
	check(empty, map[int64]bool{}, math.MaxInt64)
	if _, _, eligible := exec.ParallelQueryPlan(query.Root, captured); eligible {
		t.Fatal("sampling split independently across workers")
	}
	if exec.CursorSimpleScan(query.Root, captured) != nil {
		t.Fatal("sampling exposed an unsampled direct cursor scan")
	}
	// Different physical layouts still sample their own visible rows; returned
	// indices address the original unified source, not copied parent payloads.
	for _, sql := range []string{
		"CREATE TABLE sample_prefix(prefix text)",
		"CREATE TABLE sample_child(extra bigint) INHERITS(sample_prefix,t)",
		"CREATE TABLE sample_other() INHERITS(t)",
		"INSERT INTO sample_child(prefix,id,v,f,extra) VALUES('a',11,1,true,1),('b',12,2,false,2),('c',13,3,true,3),('d',14,4,false,4)",
		"INSERT INTO sample_other VALUES(21,1,true),(22,2,false),(23,3,true),(24,4,false)",
	} {
		enginetest.Run(t, e, sql)
	}
	hierarchy := enginetest.Run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
	defer hierarchy.Close()
	if len(hierarchy.Source.Inheritance) != 3 {
		t.Fatal("fixture lacks actual physical child prefixes")
	}
	ends := slices.Clone(hierarchy.Source.InheritanceEnds)
	allowed := map[int64]bool{1: true, 2: true, 3: true, 4: true, 11: true, 12: true, 13: true, 14: true, 21: true, 22: true, 23: true, 24: true}
	for _, count := range []int64{0, 1, 2, 4, math.MaxInt64} {
		check(hierarchy.Source, allowed, count)
	}
	if !slices.Equal(ends, hierarchy.Source.InheritanceEnds) {
		t.Fatal("sampling mutated captured physical boundaries")
	}
}

func TestSamplingHandlerOriginalDeclaration(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_rows")
	query, captured := enginetest.QueryFixture(t, e, "SELECT id FROM t TABLESAMPLE system_rows(3)")
	leaf := query.Root.(*plan.ProjectNode).Child.(*plan.SourceNode)
	for _, change := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.ObjectAddress.ObjectID++ },
		func(p *catalog.Procedure) { p.Signature = "23" },
		func(p *catalog.Procedure) { p.Result = 23 },
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) { p.BrinSupport = 1 },
		func(p *catalog.Procedure) { p.GinSupport = 1 },
		func(p *catalog.Procedure) { p.SnowballHelper = 1 },
		func(p *catalog.Procedure) { p.Display.Source = "forged_handler" },
	} {
		node, sample, procedure := *leaf, *leaf.Sample, *leaf.Sample.Procedure
		change(&procedure)
		sample.Procedure = &procedure
		evaluated := false
		sample.Argument = enginetest.QueryProbe(leaf.Sample.Argument.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
			evaluated = true
			return leaf.Sample.Argument.Eval(c)
		})
		node.Sample = &sample
		stage, err := exec.OpenNode(context.Background(), &exec.State{Source: captured}, &node)
		stage.Close()
		if err == nil || evaluated {
			t.Fatal("forged selected declaration reached sampling argument", err, evaluated)
		}
	}
	// The original plan may retain its declaration after a lawful SQL rename.
	// A newly captured source supplies the genuine current catalog, not an old
	// descriptor installed into a fabricated read.
	enginetest.Run(t, e, "ALTER FUNCTION system_rows(internal) RENAME TO genuine_rows")
	current := enginetest.Run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
	defer current.Close()
	stage, err := exec.OpenNode(context.Background(), &exec.State{Source: current.Source}, leaf)
	if err != nil {
		t.Fatal("genuine retained handler lost renamed current identity", err)
	}
	defer stage.Close()
	if stage.Count() != 3 {
		t.Fatal("renamed handler changed actual rows recipe", stage.Count())
	}
}
