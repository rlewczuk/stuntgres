package enginetests

import (
	"context"
	"errors"
	"testing"
	"unsafe"

	"stuntgres/internal/sqltype"
)

func TestHstoreKeysTyped(t *testing.T) {
	ctx := context.Background()
	h := hstoreUnitType(t, 65000)
	_, array, boolean := hstoreConstructBuiltins()
	input := func(d *sqltype.Descriptor, s string) sqltype.Value {
		t.Helper()
		v, err := d.Input(ctx, s, -1)
		if err != nil { t.Fatal(err) }
		return v
	}
	v := input(h, `a=>NULL,b=>2`)
	for _, tc := range []struct { keys string; all, any bool }{
		{"{}", true, false}, {"{NULL}", true, false}, {"{a,NULL,a}", true, true}, {"{a,NULL,missing}", false, true}, {"{missing}", false, false}, {"[0:1][3:4]={{a,b},{a,NULL}}", true, true},
	} {
		keys := input(array, tc.keys)
		for _, all := range []bool{true, false} {
			got, err := v.HstoreExistsKeys(ctx, h, boolean, keys, all)
			want := tc.any
			if all { want = tc.all }
			actual, decode := got.Boolean()
			if err != nil || decode != nil || actual != want || got.Type() != boolean { t.Fatal("key truth", tc.keys, all, actual, err, decode) }
		}
	}
	for _, tc := range []struct { subset string; want bool }{{"",true},{"a=>NULL",true},{`a=>"NULL"`,false},{"b=>3",false},{"c=>2",false}} {
		got, err := v.HstoreContains(ctx,h,boolean,input(h,tc.subset))
		actual, decode := got.Boolean()
		if err != nil || decode != nil || actual != tc.want { t.Fatal("pair truth",tc.subset,err) }
	}
	for _, operation := range []func() (sqltype.Value,error){
		func()(sqltype.Value,error){return sqltype.Null(h).HstoreExistsKeys(ctx,h,boolean,input(array,"{}"),true)},
		func()(sqltype.Value,error){return v.HstoreExistsKeys(ctx,h,boolean,sqltype.Null(array),false)},
		func()(sqltype.Value,error){return sqltype.Null(h).HstoreContains(ctx,h,boolean,v)},
		func()(sqltype.Value,error){return v.HstoreContains(ctx,h,boolean,sqltype.Null(h))},
	} {got,err:=operation();if err!=nil||!got.IsNull()||got.Type()!=boolean{t.Fatal("whole NULL predicate",err)}}
	flat,err:=v.HstoreArray(ctx,h,array,false)
	if err!=nil{t.Fatal(err)}
	for _, values := range []bool{false,true} {
		got, err := v.HstoreKeyValues(ctx,h,array,values)
		if err != nil || got.Type()!=array || got.ArrayRank()!=1 || got.ArrayDimension(0)!=(sqltype.ArrayDimension{Lower:1,Length:2}) {t.Fatal("projection shape",err)}
		for i:=range got.ArrayLen() {if got.ArrayElement(i).Type()!=array.Element(){t.Fatal("element generation")}}
		if values && (!got.ArrayElement(0).IsNull() || got.ArrayElement(1).IsNull()) {t.Fatal("value NULL bit")}
		for i:=range got.ArrayLen() {
			offset:=i*2
			if values {offset++}
			if got.ArrayElement(i).IsNull(){continue}
			projected,_:=got.ArrayElement(i).Text()
			original,_:=flat.ArrayElement(offset).Text()
			if unsafe.StringData(projected)!=unsafe.StringData(original){t.Fatal("projection copied immutable pair bytes")}
		}
		dimensions:=got.ArrayDimensions();dimensions[0].Lower=99
		if got.ArrayDimension(0).Lower!=1 {t.Fatal("dimension ownership")}
		empty,err:=input(h,"").HstoreKeyValues(ctx,h,array,values)
		if err!=nil||empty.IsNull()||empty.ArrayRank()!=0||empty.ArrayLen()!=0 {t.Fatal("empty projection",err)}
		null,err:=sqltype.Null(h).HstoreKeyValues(ctx,h,array,values)
		if err!=nil||!null.IsNull()||null.Type()!=array {t.Fatal("whole NULL projection",err)}
	}
	foreign:=hstoreUnitType(t,65000)
	for _, operation:=range []func(context.Context)(sqltype.Value,error){
		func(c context.Context)(sqltype.Value,error){return v.HstoreContains(c,foreign,boolean,input(h,""))},
		func(c context.Context)(sqltype.Value,error){return v.HstoreContains(c,h,boolean,input(foreign,""))},
		func(c context.Context)(sqltype.Value,error){return v.HstoreExistsKeys(c,foreign,boolean,input(array,"{}"),true)},
		func(c context.Context)(sqltype.Value,error){return v.HstoreKeyValues(c,foreign,array,false)},
	} {if _,err:=operation(ctx);err==nil {t.Fatal("equal-OID foreign hstore accepted")}}
	canceled,cancel:=context.WithCancel(ctx);cancel()
	for _, operation:=range []func()(sqltype.Value,error){
		func()(sqltype.Value,error){return v.HstoreContains(canceled,h,boolean,v)},
		func()(sqltype.Value,error){return v.HstoreExistsKeys(canceled,h,boolean,input(array,"{}"),true)},
		func()(sqltype.Value,error){return v.HstoreKeyValues(canceled,h,array,false)},
	} {if _,err:=operation();!errors.Is(err,context.Canceled){t.Fatal("cancellation",err)}}
	before,err:=h.Output(ctx,v);if err!=nil||before!=`"a"=>NULL, "b"=>"2"` {t.Fatal("immutable source",before,err)}
}
