package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func keywordUnitCall(t *testing.T, types map[sqltype.ID]*sqltype.Descriptor) Keywords {
	t.Helper()
	names := [5]string{"word","catcode","barelabel","catdesc","baredesc"}
	ids := [5]sqltype.ID{sqltype.Text,sqltype.Char,sqltype.Bool,sqltype.Text,sqltype.Text}
	members := make([]sqltype.CompositeMember,5)
	for i := range members { members[i] = sqltype.CompositeMember{Attribute:int32(i+1),Name:names[i],Usage:types[ids[i]].Usage()} }
	shape,err := sqltype.RecordShape(members)
	if err != nil { t.Fatal(err) }
	return Keywords{expr.Info{Usage:shape.Usage(),Occurrence:17,Property:expr.Properties{Volatility:expr.Stable}}}
}

func TestEncodingKeywordsProtocol(t *testing.T) {
	types := formatUnitTypes()
	ctx := &catalogRelationsUnitContext{Context:context.Background()}
	for _, toID := range []bool{false,true} {
		formal,result := types[sqltype.Int4],types[sqltype.Name]
		if toID { formal,result = result,formal }
		for _, null := range []bool{false,true} {
			var value sqltype.Value
			if null { value = sqltype.Null(formal) } else if toID { value = sqltype.String(formal,"UTF8") } else { var err error; value,err = sqltype.Integer(formal,6); if err != nil { t.Fatal(err) } }
			var order []int
			arg := stringUnitArgument{Info:expr.Info{Usage:formal.Usage()},value:value,order:&order}
			n := EncodingNameCall{Info:expr.Info{Usage:result.Usage(),Occurrence:23,Property:expr.Properties{Volatility:expr.Stable}},Arg:arg,ToID:toID}
			if err := n.Validate(); err != nil { t.Fatal(err) }
			got,err := n.Eval(ctx)
			if err != nil || got.Type() != result || got.IsNull() != null || len(order) != 1 { t.Fatal("strict evaluation and type",got,order,err) }
			if !null { if toID { id,e := got.Integer(); if id != 6 || e != nil { t.Fatal(id,e) } } else { name,e := got.Text(); if name != "UTF8" || e != nil { t.Fatal(name,e) } } }
			failure := errors.New("operand failure")
			arg.err = failure; n.Arg = arg
			if _,err := n.Eval(ctx); !errors.Is(err,failure) { t.Fatal("child failure suppressed",err) }
			arg.err = nil; n.Arg = arg
			rebuilt,err := n.Rebuild(n.Info,n.Children())
			if err != nil || !reflect.DeepEqual(rebuilt,n) { t.Fatal("rebuild lost immutable provenance",err) }
			if _,err := n.Rebuild(n.Info,nil); err == nil { t.Fatal("bad child count accepted") }
			if _,err := n.Rebuild(n.Info,[]expr.Expr{nil}); err == nil { t.Fatal("nil child accepted") }
			bad := n.Info; bad.Usage = types[sqltype.Bool].Usage()
			if _,err := n.Rebuild(bad,n.Children()); err == nil { t.Fatal("foreign result signature accepted") }
			capture,ok,err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != 1 || !reflect.DeepEqual(capture.Inline[0],n.Arg) { t.Fatal("capture lost operand",capture,err) }
			render,err := n.Render()
			if err != nil || render.Form != expr.FormCall || len(render.Args) != 1 || render.Replay.CatalogSnapshot { t.Fatal("symbolic render",render,err) }
			if n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren || n.IntrinsicVolatility() != expr.Stable || n.CallCost() != 1 { t.Fatal("scalar lifecycle policy") }
			if !n.SameGroup(n,func(a,b expr.Expr) bool{return reflect.DeepEqual(a,b)},true) { t.Fatal("logical group changed") }
		}
	}
	n := keywordUnitCall(t,types)
	if n.Validate() != nil || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightKeep || n.CallCost() != 10 { t.Fatal("keyword protocol") }
	if _,err := n.Rebuild(n.Info,[]expr.Expr{n}); err == nil { t.Fatal("keyword children accepted") }
	bad := n.Info; bad.Usage = types[sqltype.Text].Usage()
	if _,err := n.Rebuild(bad,nil); err == nil { t.Fatal("keyword scalar shape accepted") }
	rebuilt,err := n.Rebuild(n.Info,nil)
	if err != nil || !reflect.DeepEqual(n,rebuilt) { t.Fatal("keyword provenance lost",err) }
	capture,ok,err := n.Capture(expr.CaptureFunction)
	if !ok || err != nil || capture.Count != 0 { t.Fatal(capture,ok,err) }
	render,err := n.Render()
	if err != nil || render.Name != "pg_get_keywords" || render.Replay.CatalogSnapshot { t.Fatal(render,err) }
}

func TestEncodingKeywordsSource(t *testing.T) {
	n := keywordUnitCall(t,formatUnitTypes())
	ctx := &catalogRelationsUnitContext{Context:context.Background()}
	first,err := n.OpenSource(ctx); if err != nil { t.Fatal(err) }; defer first.Close()
	second,err := n.OpenSource(ctx); if err != nil { t.Fatal(err) }; defer second.Close()
	if first.Values != nil || first.State != nil || second.State != nil || first.Position != 0 || second.Position != 0 { t.Fatal("eager source state") }
	row,present,err := first.Next(context.Background())
	if err != nil || !present || first.Position != 1 || second.Position != 0 || row.Type() != n.Usage.Type { t.Fatal("cursor position/type",err) }
	word,_ := row.CompositeField(0).Text(); code,_ := row.CompositeField(1).Text()
	if word != "abort" || code != "U" { t.Fatal("literal first row",word,code) }
	count := 1
	for { value,present,err := first.Next(context.Background()); if err != nil { t.Fatal(err) }; if !present { break }; count++; for i := range 5 { if value.CompositeField(i).Type() != n.Usage.Type.CompositeMember(i).Usage.Type { t.Fatal("foreign typed field",i) } } }
	if count != 494 || !first.Exhausted { t.Fatal("EOF count",count) }
	if value,present,err := first.Next(context.Background()); present || err != nil || !value.IsNull() || value.Type() != n.Usage.Type { t.Fatal("unstable or untyped EOF",err) }
	second.Close(); second.Close()
	if !second.Exhausted || second.State != nil { t.Fatal("close retained state") }
	canceled,cancel := context.WithCancel(context.Background()); cancel()
	third,err := n.OpenSource(ctx); if err != nil { t.Fatal(err) }; defer third.Close()
	if _,present,err := third.Next(canceled); present || !errors.Is(err,context.Canceled) || third.Position != 0 || !third.Exhausted { t.Fatal("canceled source advanced",err) }
	// A new generation owns every field through the supplied record descriptor;
	// no previously constructed datum survives rebuilding.
	next := keywordUnitCall(t,formatUnitTypes())
	if next.Usage.Type == n.Usage.Type { t.Fatal("remap witness reused record generation") }
	if !n.SameSourceOccurrence(n) || n.SameSourceOccurrence(EncodingNameCall{}) { t.Fatal("source occurrence signature") }
	rebuilt,err := n.Rebuild(next.Info,nil); if err != nil { t.Fatal(err) }
	cursor,err := rebuilt.(Keywords).OpenSource(ctx); if err != nil { t.Fatal(err) }; defer cursor.Close()
	row,present,err = cursor.Next(context.Background())
	if !present || err != nil || row.Type() != next.Usage.Type { t.Fatal("rebuilt record ownership",err) }
	for i := range 5 { if row.CompositeField(i).Type() != next.Usage.Type.CompositeMember(i).Usage.Type { t.Fatal("rebuilt field ownership",i) } }
}
