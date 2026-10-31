package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryOuterJoinsMergedOriginsAndNullableFrames(t *testing.T) {
	for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
		for _, operands := range []string{"a " + kind + " JOIN wide", "wide " + kind + " JOIN a"} {
			_, query, source := queryJoinsFixture(t, "SELECT k,a.k,wide.k FROM "+operands+" USING(k)")
			schema := query.Root.Schema()
			direct := kind == "LEFT" && operands[:4] == "wide" || kind == "RIGHT" && operands[:1] == "a"
			if schema[0].Usage.Type.ID() != sqltype.Int8 || (schema[0].OriginTable != 0) != direct {
				t.Fatal(operands, schema)
			}
			if direct && (schema[0].OriginTable != schema[2].OriginTable || schema[0].OriginAttribute != schema[2].OriginAttribute) {
				t.Fatal("lost preserved origin", schema)
			}
			join := query.Root.(*plan.ProjectNode).Child.(*plan.JoinNode)
			beforeLeft := append([]catalog.Column(nil), join.Left.Schema()...)
			beforeRight := append([]catalog.Column(nil), join.Right.Schema()...)
			if schema[1].OriginTable == 0 || schema[2].OriginTable == 0 {
				t.Fatal("lost qualified originals", schema)
			}
			if kind == "FULL" && (!schema[1].Nullable || !schema[1].NullabilityKnown || !schema[2].Nullable || !schema[2].NullabilityKnown) {
				t.Fatal(schema)
			}
			result, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
			want := 4
			if kind == "RIGHT" && operands[:1] == "a" || kind == "LEFT" && operands[:4] == "wide" {
				want = 2
			}
			if err != nil || count != want {
				t.Fatal(count, want, err)
			}
			result.Close()
			if !reflect.DeepEqual(beforeLeft, join.Left.Schema()) || !reflect.DeepEqual(beforeRight, join.Right.Schema()) {
				t.Fatal("child schema mutated")
			}
		}
	}
}

func TestQueryOuterJoinsDemandAndCancellation(t *testing.T) {
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		for _, limit := range []int64{0, 1} {
			e, query, source := queryJoinsFixture(t, "SELECT a.k FROM a JOIN b ON a.k=b.k")
			project := query.Root.(*plan.ProjectNode)
			join := project.Child.(*plan.JoinNode)
			join.Kind = kind
			typ, _ := e.CatalogRead().LookupType(sqltype.Bool)
			calls := 0
			join.Predicate = enginetest.QueryProbe(typ.Usage(), func(*exec.Context) (sqltype.Value, error) {
				calls++
				if calls > 1 {
					return sqltype.Value{}, errors.New("undemanded pair")
				}
				return sqltype.Boolean(typ, true), nil
			})
			project.Child = &plan.LimitNode{QuerySchema: join.Schema(), Child: join, Count: queryLimitBound(t, e, limit)}
			result, count, err := exec.Finalize(context.Background(), project, source, nil)
			if err != nil || count != int(limit) || calls != int(limit) {
				t.Fatal(kind, limit, count, calls, err)
			}
			result.Close()
		}
		e, query, source := queryJoinsFixture(t, "SELECT a.k FROM a JOIN b ON a.k=b.k")
		project := query.Root.(*plan.ProjectNode)
		join := project.Child.(*plan.JoinNode)
		join.Kind = kind
		typ, _ := e.CatalogRead().LookupType(sqltype.Bool)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		join.Predicate = enginetest.QueryProbe(typ.Usage(), func(*exec.Context) (sqltype.Value, error) {
			calls++
			if calls == 3 {
				cancel()
			}
			return sqltype.Boolean(typ, false), nil
		})
		result, _, err := exec.Finalize(ctx, project, source, nil)
		if result != nil || !errors.Is(err, context.Canceled) || calls != 3 {
			t.Fatal(kind, result, calls, err)
		}
	}
}
