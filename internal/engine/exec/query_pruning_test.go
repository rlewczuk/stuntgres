package exec

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestQueryBranchPruning(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
	}
	rows := make([][]sqltype.Value, 8)
	for i := range rows {
		v, err := sqltype.Integer(integer, int64(i))
		if err != nil {
			t.Fatal(err)
		}
		rows[i] = []sqltype.Value{v}
	}
	for _, c := range []struct {
		name      string
		selection QueryBranchSelection
		scan      bool
		want      []int
	}{
		{"all", QueryBranchSelection{}, false, []int{0, 1, 2, 3, 4, 5, 6, 7}},
		{"empty", QueryBranchSelection{Applied: true}, false, nil},
		{"intervals", QueryBranchSelection{Applied: true, Intervals: []QueryBranchInterval{{1, 3}, {5, 7}}}, false, []int{1, 2, 5, 6}},
		{"scan_intersection", QueryBranchSelection{Applied: true, Intervals: []QueryBranchInterval{{1, 3}, {5, 7}}}, true, []int{2, 5}},
	} {
		t.Run(c.name, func(t *testing.T) {
			child := Stage{Source: &Source{Anonymous: rows}, scanRange: c.scan, scanStart: 2, scanEnd: 6}
			cursor := queryBranchCursor{selection: c.selection}
			defer child.Close()
			for range 2 {
				var got []int
				for {
					i, present, err := cursor.next(context.Background(), &child)
					if err != nil {
						t.Fatal(err)
					}
					if !present {
						break
					}
					raw := child.Index(i)
					value, err := child.Source.Row(raw)[0].Integer()
					if err != nil || value != int64(raw) {
						t.Fatal("raw row identity changed", value, raw, err)
					}
					got = append(got, raw)
				}
				if !reflect.DeepEqual(got, c.want) {
					t.Fatal("interval producer", got, c.want)
				}
				if _, err := child.Rewind(context.Background()); err != nil {
					t.Fatal(err)
				}
				cursor.position = 0
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, _, err := cursor.next(ctx, &child); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled selection", err)
			}
		})
	}
}

// This owns cursor/frontier mechanics only. Positive provider events are proved
// independently by the composed engine-backed PGStatAllTablesScanDemand cases.
func TestQueryBranchPruningEmptyDemand(t *testing.T) {
	source:=&Source{Anonymous:make([][]sqltype.Value,4),InheritanceEnds:[]int{0,2,2,4,4},Inheritance:make([]InheritanceQuerySegment,5)}
	for i:=range source.Inheritance {source.Inheritance[i].Prefix=&Source{}}
	scan:=NewRelationScan(source)
	scan.pruned=true
	child:=Stage{Source:source,scan:&scan}
	defer child.Close()
	cursor:=queryBranchCursor{selection:QueryBranchSelection{Applied:true,Intervals:[]QueryBranchInterval{{Start:0,End:4}},EmptySegments:[]int{0,2,4}}}
	started:=func(want []bool) {
		t.Helper()
		got:=make([]bool,5)
		for i:=range scan.segments {got[i]=scan.segments[i].started}
		if !reflect.DeepEqual(got,want) {t.Fatal("demanded physical segment frontier",got,want)}
	}
	started([]bool{false,false,false,false,false})
	if _,ok,err:=cursor.next(context.Background(),&child);err!=nil||!ok {t.Fatal(ok,err)}
	started([]bool{true,true,false,false,false})
	ctx,cancel:=context.WithCancel(context.Background());cancel()
	if _,_,err:=cursor.next(ctx,&child);!errors.Is(err,context.Canceled) {t.Fatal(err)}
	started([]bool{true,true,false,false,false})
	for range 3 {if _,ok,err:=cursor.next(context.Background(),&child);err!=nil||!ok {t.Fatal(ok,err)}}
	started([]bool{true,true,true,true,false})
	if _,ok,err:=cursor.next(context.Background(),&child);err!=nil||ok {t.Fatal(ok,err)}
	started([]bool{true,true,true,true,true})
}
