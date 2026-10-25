package sqltype

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func vectorUnitInput(t *testing.T, d *Descriptor, input string) Value {
	t.Helper()
	v,err:=d.Input(context.Background(),input,-1)
	if err!=nil {t.Fatal(input,err)}
	return v
}

func TestCatalogVectorCodec(t *testing.T) {
	types:=testTypes()
	for _,tc:=range []struct{id ID;input,output string}{
		{Int2Vector,"1 2 3","1 2 3"},{Int2Vector," +001 -0002 32767 -32768 ","1 -2 32767 -32768"},
		{Int2Vector,"",""},{Int2Vector," \t\n\r\v\f",""},{Int2Vector,"\t1  \n2","1 2"},
		{OidVector,"1 2 3","1 2 3"},{OidVector,"-1 -2147483648","4294967295 2147483648"},
		{OidVector,"010 0x10 +10","8 16 10"},{OidVector,"1-2","1 4294967294"},{OidVector,"1+2","1 2"},{OidVector,"08","0 8"},
		{OidVector,"0 2147483648 4294967295","0 2147483648 4294967295"},{OidVector,"1\t2\n3","1 2 3"},
		{OidVector,"18446744073709551615","4294967295"},{OidVector,"18446744071562067968","2147483648"},{OidVector,"-18446744073709551615","1"},
		{OidVector,"",""},{OidVector," \t\n\r\v\f",""},
	}{
		d:=types[tc.id]
		v:=vectorUnitInput(t,d,tc.input)
		out,err:=d.Output(context.Background(),v)
		if err!=nil||out!=tc.output {t.Fatal(tc,out,err)}
		literal,err:=d.SQLLiteral(context.Background(),v)
		if err!=nil||literal!="'"+tc.output+"'" {t.Fatal(tc,literal,err)}
		if !SameDatum(v,vectorUnitInput(t,d,out)) {t.Fatal("canonical reinput",tc)}
	}
	for _,id:=range []ID{Int2Vector,OidVector}{
		for _,space:=range []byte{' ','\t','\n','\r','\v','\f'}{
			v:=vectorUnitInput(t,types[id],string(space)+"1 "+string(space)+"2")
			out,err:=types[id].Output(context.Background(),v)
			if out!="1 2"||err!=nil{t.Fatal(id,space,out,err)}
		}
	}
	for _,tc:=range []struct{id ID;input,suffix,code,name string}{
		{Int2Vector,"1\t2","1\t2","22P02","smallint"},{Int2Vector,"1-2","1-2","22P02","smallint"},
		{Int2Vector,"1,2","1,2","22P02","smallint"},{Int2Vector,"0x10","0x10","22P02","smallint"},
		{Int2Vector,"1_0","1_0","22P02","smallint"},{Int2Vector,"NULL","NULL","22P02","smallint"},
		{Int2Vector,"{1,2}","{1,2}","22P02","smallint"},{Int2Vector,"1 x 2","x 2","22P02","smallint"},
		{Int2Vector,"1 32768 2","32768 2","22003","smallint"},{Int2Vector,"1 -32769 2","-32769 2","22003","smallint"},
		{Int2Vector,"32768x","32768x","22003","smallint"},{Int2Vector,"999999999999999999999999x","999999999999999999999999x","22003","smallint"},
		{Int2Vector,"\u00a01","\u00a01","22P02","smallint"},{Int2Vector,"+","+","22P02","smallint"},
		{OidVector,"1x","x","22P02","oid"},{OidVector,"1,2",",2","22P02","oid"},{OidVector,"1 NULL","NULL","22P02","oid"},
		{OidVector,"1 4294967296 2","4294967296 2","22003","oid"},{OidVector,"1 -2147483649 2","-2147483649 2","22003","oid"},
		{OidVector,"18446744071562067967","18446744071562067967","22003","oid"},
		{OidVector,"18446744073709551616x","18446744073709551616x","22003","oid"},
		{OidVector,"-18446744073709551616","-18446744073709551616","22003","oid"},
		{OidVector,"+","+","22P02","oid"},{OidVector,"{1,2}","{1,2}","22P02","oid"},{OidVector,"\u00a01","\u00a01","22P02","oid"},
	}{
		_,err:=types[tc.id].Input(context.Background(),tc.input,-1)
		message:="invalid input syntax for type "+tc.name+": \""+tc.suffix+"\""
		if tc.code=="22003"{message="value \""+tc.suffix+"\" is out of range for type "+tc.name}
		want:=&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:tc.code,Message:message}
		var got *sqlstate.Error
		if !errors.As(err,&got)||!reflect.DeepEqual(got,want){t.Fatalf("%v: got %#v want %#v",tc,got,want)}
	}
}

func TestCatalogVectorRepresentation(t *testing.T) {
	types:=testTypes()
	for _,tc:=range []struct{id,element,array ID;name string}{{Int2Vector,Int2,1006,"int2vector"},{OidVector,Oid,1013,"oidvector"}}{
		d:=types[tc.id]
		if !d.IsArray()||!d.IsCatalogVector()||!d.Concrete()||d.Element()!=types[tc.element]||d.AssociatedArray()!=tc.array||d.DiagnosticName()!=tc.name {t.Fatal("vector identity",tc,d)}
		if types[tc.array].Concrete(){t.Fatal("vector companion unexpectedly executable")}
		facts,ok:=d.PGTypeMetadata()
		if !ok||facts.Element!=tc.element||facts.Length!=-1||facts.ByValue||facts.Alignment!="i"||facts.Storage!="p"||facts.Category!="A"||facts.Subscript!=6179 {t.Fatal("vector facts",facts,ok)}
		empty:=vectorUnitInput(t,d,"")
		if empty.IsNull()||empty.ArrayRank()!=1||empty.ArrayDimension(0)!=(ArrayDimension{Lower:0,Length:0})||!empty.ValidResult(d.Usage()){t.Fatal("empty header",empty)}
		null:=Null(d)
		if !null.IsNull()||null.ArrayRank()!=0||SameDatum(empty,null){t.Fatal("NULL/empty conflation")}
		if literal,err:=d.SQLLiteral(context.Background(),null);err!=nil||literal!="NULL"{t.Fatal(literal,err)}
		v:=vectorUnitInput(t,d,"1 2 3")
		for _,index:=range []int32{-1,0,1,2,3}{
			element,err:=ArrayFetch(context.Background(),v,[]ArraySubscriptValue{{Upper:index,HasUpper:true}})
			if err!=nil {t.Fatal(err)}
			if index<0||index>2 {if !element.IsNull(){t.Fatal("out of bounds",index,element)}} else if !SameDatum(element,v.ArrayElement(int(index))){t.Fatal("zero based fetch",index)}
		}
		for _,dims:=range [][]ArrayDimension{nil,{{Lower:1,Length:3}},{{Lower:0,Length:1},{Lower:0,Length:3}}}{
			if _,err:=ArrayValue(d,dims,v.array.elements);err==nil {t.Fatal("invalid vector shape admitted",dims)}
		}
		if _,err:=OwnedCatalogVectorValue(d,[]Value{Null(d.Element())});err==nil{t.Fatal("NULL vector member admitted")}
		if _,err:=OwnedCatalogVectorValue(d,[]Value{vectorUnitInput(t,testTypes()[tc.element],"1")});err==nil{t.Fatal("foreign element generation admitted")}
		if _,err:=d.BinaryInputWithEnvironment(context.Background(),nil,-1,InputEnvironment{});err==nil{t.Fatal("unimplemented binary vector input admitted")}
		canceled,cancel:=context.WithCancel(context.Background());cancel()
		if _,err:=d.Input(canceled,"1",-1);!errors.Is(err,context.Canceled){t.Fatal(err)}
		if _,err:=d.Output(canceled,v);!errors.Is(err,context.Canceled){t.Fatal(err)}
	}
}

func TestCatalogVectorGeneration(t *testing.T) {
	old,next:=testTypes(),testTypes()
	for _,id:=range []ID{Int2Vector,OidVector}{
		rebound,err:=old[id].RebindCatalogVector(next[id].Element())
		if err!=nil||rebound.ID()!=id||rebound.Element()!=next[id].Element()||!rebound.IsCatalogVector(){t.Fatal("vector provider reownership",rebound,err)}
		if _,err:=old[id].RebindCatalogVector(next[Int4]);err==nil{t.Fatal("wrong vector element admitted")}
		for _,input:=range []string{"","1 2 3"}{
			v:=vectorUnitInput(t,old[id],input)
			same,err:=v.MapArray(old[id],func(v Value,_ *Descriptor)(Value,error){return v,nil})
			if err!=nil||same.array!=v.array{t.Fatal("identity mapping copied payload",err)}
			mapped,err:=v.Retag(next[id])
			if err!=nil||mapped.Type()!=next[id]||mapped.ArrayRank()!=1||mapped.ArrayDimension(0)!=v.ArrayDimension(0){t.Fatal("generation header",mapped,err)}
			for i:=range mapped.ArrayLen(){if mapped.ArrayElement(i).Type()!=next[id].Element(){t.Fatal("foreign reowned element")}}
			back,err:=mapped.Retag(old[id]);if err!=nil||!SameDatum(v,back){t.Fatal("generation roundtrip",err)}
		}
		v:=vectorUnitInput(t,old[id],"1 2")
		borrowed:=[]Value{v.ArrayElement(0),v.ArrayElement(1)}
		frozen,err:=CatalogVectorValue(old[id],borrowed);if err!=nil{t.Fatal(err)}
		borrowed[0]=borrowed[1]
		if !SameDatum(frozen.ArrayElement(0),v.ArrayElement(0)){t.Fatal("borrowed elements alias")}
		owned:=[]Value{v.ArrayElement(0),v.ArrayElement(1)}
		transferred,err:=OwnedCatalogVectorValue(old[id],owned)
		if err!=nil||&transferred.array.elements[0]!=&owned[0]{t.Fatal("ownership transfer copied",err)}
		invalid:=Value{typ:old[id],array:&arrayPayload{}}
		if invalid.ValidResult(old[id].Usage()){t.Fatal("invalid result shape")}
		if _,err:=invalid.Retag(old[id]);err==nil{t.Fatal("invalid Retag shape")}
		array:=old[old[id].Element().AssociatedArray()]
		if _,err:=PlanCoercion(array.Usage(),old[id].Usage(),Explicit);err==nil{t.Fatal("generic array-to-vector cast")}
	}
}

func TestCatalogVectorComparison(t *testing.T) {
	types:=testTypes()
	for _,tc:=range []struct{id ID;left,right string;sign int}{
		{Int2Vector,"99","1 2",1},{OidVector,"99","1 2",-1},
		{Int2Vector,"-1 9","0 0",-1},{OidVector,"2147483648","4294967295",-1},
		{Int2Vector,"1 9","2 0",-1},{OidVector,"1 9","2 0",-1},
		{Int2Vector,"","0",-1},{OidVector,"","0",-1},
		{Int2Vector,"+01 002","1 2",0},{OidVector,"+01 002","1 2",0},
	}{
		d:=types[tc.id];a,b:=vectorUnitInput(t,d,tc.left),vectorUnitInput(t,d,tc.right)
		p,err:=BindOrdering(d.Usage());if err!=nil{t.Fatal(err)}
		sign,err:=p.Compare(context.Background(),a,b,InputEnvironment{})
		if err!=nil||sign!=tc.sign{t.Fatal(tc,sign,err)}
		equal,err:=p.EqualValues(context.Background(),a,b,InputEnvironment{})
		if err!=nil||equal!=(tc.sign==0){t.Fatal(tc,equal,err)}
		canceled,cancel:=context.WithCancel(context.Background());cancel()
		if _,err:=p.Compare(canceled,a,b,InputEnvironment{});!errors.Is(err,context.Canceled){t.Fatal(err)}
	}
}

func TestCatalogVectorCatalogConstructors(t *testing.T) {
	types:=testTypes()
	key:=[]int16{1,2,3};options:=[]int16{3,0};classes:=[]uint32{1978,1978};collations:=[]uint32{0,0}
	for _,tc:=range []struct{values []int16;want string}{{key,"1 2 3"},{options,"3 0"},{[]int16{0},"0"},{nil,""}}{
		v,err:=CatalogInt2Vector(types[Int2Vector],tc.values);if err!=nil{t.Fatal(err)}
		text,err:=types[Int2Vector].Output(context.Background(),v);if err!=nil||text!=tc.want{t.Fatal(text,tc.want,err)}
	}
	for _,tc:=range []struct{values []uint32;want string}{{classes,"1978 1978"},{collations,"0 0"},{[]uint32{21,26},"21 26"},{nil,""}}{
		v,err:=CatalogOIDVector(types[OidVector],tc.values);if err!=nil{t.Fatal(err)}
		text,err:=types[OidVector].Output(context.Background(),v);if err!=nil||text!=tc.want{t.Fatal(text,tc.want,err)}
		if len(tc.values)>0{tc.values[0]=0;if textAgain,_:=types[OidVector].Output(context.Background(),v);textAgain!=tc.want{t.Fatal("numeric constructor aliases input")}}
	}
}

func TestCatalogVectorArrayCoercion(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		source, target ID
		relabel bool
	}{
		{Int2Vector, 1005, true},
		{OidVector, 1028, true},
		{OidVector, 1007, true},
		{Int2Vector, 1007, false},
		{Int2Vector, 1016, false},
		{OidVector, 1016, false},
		{Int2Vector, 1009, false},
	} {
		source, target := types[tc.source], types[tc.target]
		plan, err := PlanCoercion(source.Usage(), target.Usage(), Explicit)
		if err != nil { t.Fatal(tc, err) }
		empty := vectorUnitInput(t, source, "")
		got, err := plan.Apply(ctx, empty, InputEnvironment{}, OutputEnvironment{})
		if err != nil || got.Type() != target || got.IsNull() || got.ArrayLen() != 0 { t.Fatal(tc, got, err) }
		if tc.relabel {
			if got.ArrayRank() != 1 || got.ArrayDimension(0) != (ArrayDimension{Lower:0, Length:0}) || got.array != empty.array { t.Fatal("binary lowering lost immutable empty header", tc, got) }
		} else if got.ArrayRank() != 0 || got.array == empty.array {
			t.Fatal("element conversion retained special empty header", tc, got)
		}
		if empty.ArrayRank() != 1 || empty.ArrayDimension(0) != (ArrayDimension{Lower:0, Length:0}) { t.Fatal("conversion mutated source header", tc) }
		nonnull := vectorUnitInput(t, source, "1 2")
		got, err = plan.Apply(ctx, nonnull, InputEnvironment{}, OutputEnvironment{})
		if err != nil || got.Type() != target || got.ArrayLen() != 2 || got.ArrayRank() != 1 || got.ArrayDimension(0) != (ArrayDimension{Lower:0, Length:2}) { t.Fatal("nonempty conversion bounds", tc, got, err) }
		for i := range got.ArrayLen() {
			if got.ArrayElement(i).Type() != target.Element() { t.Fatal("converted element generation", tc, i) }
		}
		got, err = plan.Apply(ctx, Null(source), InputEnvironment{}, OutputEnvironment{})
		if err != nil || got.Type() != target || !got.IsNull() { t.Fatal("NULL conversion", tc, got, err) }
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := plan.Apply(canceled, empty, InputEnvironment{}, OutputEnvironment{}); !errors.Is(err, context.Canceled) { t.Fatal("empty conversion bypassed cancellation", tc, err) }
		foreign := vectorUnitInput(t, testTypes()[tc.source], "")
		if _, err := plan.Apply(ctx, foreign, InputEnvironment{}, OutputEnvironment{}); err == nil { t.Fatal("empty conversion bypassed source generation", tc) }
		if _, err := plan.Apply(ctx, Value{typ:source}, InputEnvironment{}, OutputEnvironment{}); err == nil { t.Fatal("empty conversion bypassed payload validation", tc) }
	}
}
