package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
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
			join := query.Root.(*projectNode).child.(*joinNode)
			beforeLeft := append([]ColumnDescriptor(nil), join.left.Schema()...)
			beforeRight := append([]ColumnDescriptor(nil), join.right.Schema()...)
			if schema[1].OriginTable == 0 || schema[2].OriginTable == 0 {
				t.Fatal("lost qualified originals", schema)
			}
			if kind == "FULL" && (!schema[1].Nullable || !schema[1].NullabilityKnown || !schema[2].Nullable || !schema[2].NullabilityKnown) {
				t.Fatal(schema)
			}
			result, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
			want := 4
			if kind == "RIGHT" && operands[:1] == "a" || kind == "LEFT" && operands[:4] == "wide" {
				want = 2
			}
			if err != nil || count != want {
				t.Fatal(count, want, err)
			}
			result.Close()
			if !reflect.DeepEqual(beforeLeft, join.left.Schema()) || !reflect.DeepEqual(beforeRight, join.right.Schema()) {
				t.Fatal("child schema mutated")
			}
		}
	}
}

func TestQueryOuterJoinsDemandAndCancellation(t *testing.T) {
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		for _, limit := range []int64{0, 1} {
			e, query, source := queryJoinsFixture(t, "SELECT a.k FROM a JOIN b ON a.k=b.k")
			project := query.Root.(*projectNode)
			join := project.child.(*joinNode)
			join.kind = kind
			typ, _ := e.CatalogRead().LookupType(sqltype.Bool)
			calls := 0
			join.predicate = queryProbe(typ.Usage(), func(*EvalContext) (sqltype.Value, error) {
				calls++
				if calls > 1 {
					return sqltype.Value{}, errors.New("undemanded pair")
				}
				return sqltype.Boolean(typ, true), nil
			})
			project.child = &limitNode{querySchema: join.Schema(), child: join, count: queryLimitBound(t, e, limit)}
			result, count, err := finalizeQuery(context.Background(), project, source, nil)
			if err != nil || count != int(limit) || calls != int(limit) {
				t.Fatal(kind, limit, count, calls, err)
			}
			result.Close()
		}
		e, query, source := queryJoinsFixture(t, "SELECT a.k FROM a JOIN b ON a.k=b.k")
		project := query.Root.(*projectNode)
		join := project.child.(*joinNode)
		join.kind = kind
		typ, _ := e.CatalogRead().LookupType(sqltype.Bool)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		join.predicate = queryProbe(typ.Usage(), func(*EvalContext) (sqltype.Value, error) {
			calls++
			if calls == 3 {
				cancel()
			}
			return sqltype.Boolean(typ, false), nil
		})
		result, _, err := finalizeQuery(ctx, project, source, nil)
		if result != nil || !errors.Is(err, context.Canceled) || calls != 3 {
			t.Fatal(kind, result, calls, err)
		}
	}
}

func TestQueryOuterJoinsAbsentVersionsAndLineage(t *testing.T) {
	real := rowVersion{row: 7}
	source := &querySource{materialized: true, occurrences: []int{0, 1}, provenance: []int{-1, 3}, versions: []rowVersion{{}, real}}
	got := appendQueryVersions(nil, source, 0, &queryLockContext{})
	if !reflect.DeepEqual(got, []rowVersion{{}, real}) {
		t.Fatal("first absent version invalidated complete cache", got)
	}
	bridge := &querySource{lineage: &queryLockLineage{stage: source, currentIndex: 4, currentOrder: []sqltype.Value{{}}, cacheWidth: 1}}
	provenance := appendQueryProvenance(nil, bridge, absentQueryIndex)
	if !reflect.DeepEqual(provenance, []int{-1, -1}) || bridge.lineage.currentIndex != -1 || bridge.lineage.currentOrder != nil {
		t.Fatal(provenance, bridge.lineage)
	}
	if source.rawIndex(-1) != -1 || source.versionAt(-1).row != 0 || source.row(-1) != nil {
		t.Fatal("absent identity dereferenced")
	}
	if len(appendQueryVersions(nil, bridge, -1, &queryLockContext{})) != 2 {
		t.Fatal("absent version width")
	}
	sparse := &querySource{materialized: true, occurrences: []int{0, 1}, versions: []rowVersion{{}, {}}, versionValid: []bool{false}, lineage: &queryLockLineage{stage: source, selection: []int{0}}}
	got = appendQueryVersions(nil, sparse, 0, &queryLockContext{})
	if !reflect.DeepEqual(got, []rowVersion{{}, real}) {
		t.Fatal("sparse uninitialized cache mistaken for absent physical versions", got)
	}
}

func TestQueryOuterJoinsFullLegality(t *testing.T) {
	for _, tc := range []struct {
		condition string
		legal     bool
	}{
		{"b.k=a.k", true}, {"a.k+1=b.k+1", true}, {"CAST(a.k AS bigint)=b.k", true},
		{"a.k=a.k", false}, {"a.k=1", false}, {"a.k+b.k=b.k", false}, {"a.k<b.k", false},
		{"NOT NOT (a.k=b.k)", true}, {"NOT (NOT (a.k=b.k) OR FALSE)", true},
		{"(a.k=b.k AND a.q=9) OR (a.k=b.k AND b.q=8)", true},
		{"a.k=b.k OR a.q=9", false}, {"(a.k<b.k) AND FALSE", true},
		{"(a.k<b.k) OR (1+1=2)", true}, {"NULL::boolean AND a.k<b.k", false},
		{"CASE WHEN 1+1=2 THEN a.k=b.k ELSE a.k<b.k END", true},
		{"(a.k<b.k) AND (2=3)", true}, {"NOT (TRUE OR a.k<b.k)", true},
		{"CASE WHEN 2=3 THEN a.k<b.k ELSE FALSE END", true},
		{"COALESCE(NULL::boolean,TRUE)", true}, {"COALESCE(a.k=b.k,FALSE)", false},
		{"COALESCE(NULL::boolean,a.k=b.k)", false},
		{"COALESCE(NULL::boolean,CASE WHEN TRUE THEN a.k=b.k ELSE FALSE END)", false},
	} {
		t.Run(tc.condition, func(t *testing.T) {
			e, query, _ := queryJoinsFixture(t, "SELECT a.k FROM a FULL JOIN b ON "+tc.condition)
			evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
			root, err := preflightQuery(&evaluation, query.Root)
			if err != nil {
				t.Fatal(err)
			}
			err = validateQueryJoinExecution(&evaluation, root, e.CatalogRead(), 0)
			if tc.legal && err != nil || !tc.legal && err == nil {
				t.Fatal(tc, err)
			}
			if !tc.legal {
				state(t, err, "0A000")
			}
		})
	}
}

func TestQueryOuterJoinsNestedMergedKeyDependencies(t *testing.T) {
	for _, queryText := range []string{
		"SELECT j.k FROM (a FULL JOIN wide USING(k)) j FULL JOIN b ON j.k=b.k",
		"SELECT j.k FROM b FULL JOIN (a FULL JOIN wide USING(k)) j ON b.k=j.k",
		"SELECT a.k FROM a FULL JOIN b ON a.l||current_user=b.r",
		"SELECT a.k FROM a FULL JOIN b ON CASE WHEN a.q=9 THEN a.k ELSE a.k+1 END=b.k",
	} {
		e, query, _ := queryJoinsFixture(t, queryText)
		c := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
		root, err := preflightQuery(&c, query.Root)
		if err != nil {
			t.Fatal(queryText, err)
		}
		if err := validateQueryJoinExecution(&c, root, e.CatalogRead(), 0); err != nil {
			t.Fatal(queryText, err)
		}
	}
}

func TestQueryOuterJoinsExecutionRowmarkModes(t *testing.T) {
	for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
		for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
			e, query, _ := queryJoinsFixture(t, "SELECT a.k FROM a "+kind+" JOIN b ON a.k=b.k LIMIT 0 "+clause)
			c := EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
			root, err := preflightQuery(&c, query.Root)
			if err != nil {
				t.Fatal(err)
			}
			err = validateQueryJoinExecution(&c, root, e.CatalogRead(), 0)
			state(t, err, "0A000")
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: clause + " cannot be applied to the nullable side of an outer join"}
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(err)
			}
		}
	}
}

func TestQueryOuterJoinsLocalMarkFrontierManifest(t *testing.T) {
	inputs := &querySource{sources: []*querySource{{occurrence: 0}, {occurrence: 1}}}
	inputs.locks = &queryLockContext{leaves: []queryLockLeaf{
		{leaf: queryLeaf{relation: &catalog.Relation{RelationKind: catalog.TableRelation}}},
		{leaf: queryLeaf{relation: &catalog.Relation{RelationKind: catalog.TableRelation}}},
	}}
	left := &rowLockNode{child: &sourceNode{occurrence: 0}, marks: []queryRowMark{{mode: lock.ForUpdate}}, firstMode: lock.ForUpdate}
	for _, kind := range []parser.JoinKind{parser.JoinInner, parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		join := &joinNode{kind: kind, left: left, right: &sourceNode{occurrence: 1}}
		marks := pendingQueryMarks(join, inputs, nil)
		want := 0
		if kind == parser.JoinInner {
			want = 1
		}
		if len(marks) != want {
			t.Fatal(kind, marks)
		}
	}
}
