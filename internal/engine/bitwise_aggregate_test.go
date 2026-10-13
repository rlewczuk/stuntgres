package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
)

func bitwiseAggregateUnitProvider(t *testing.T,d *sqltype.Descriptor,operation aggregateOperation) aggregateProvider {
	t.Helper()
	if d.ID()==sqltype.Bit { return bitStringAggregateProvider{d.Usage(),operation} }
	operator,err := bitwiseAggregateOperator(operation)
	if err!=nil { t.Fatal(err) }
	bound,err := sqltype.BindBitwise(operator,d,d)
	if err!=nil { t.Fatal(err) }
	return integralBitwiseAggregateProvider{d.Usage(),operation,bound}
}

func TestBitwiseAggregateIntegralProvider(t *testing.T) {
	ctx,e := context.Background(),New()
	for _, tc := range []struct{ id sqltype.ID; minimum,maximum int64 }{{sqltype.Int2,-32768,32767},{sqltype.Int4,-2147483648,2147483647},{sqltype.Int8,-9223372036854775808,9223372036854775807}} {
		d,_ := e.CatalogRead().LookupType(tc.id)
		for index,op := range []aggregateOperation{aggregateBitAnd,aggregateBitOr,aggregateBitXor} {
			p := bitwiseAggregateUnitProvider(t,d,op)
			for _, values := range [][]int64{nil,{7},{12,10,3},{tc.minimum,tc.maximum},{tc.minimum}} {
				s,err := p.Init(ctx)
				if err!=nil { t.Fatal(err) }
				isolated,err := p.Init(ctx)
				if err!=nil || s==isolated { t.Fatal("shared state",err) }
				for _, n := range values {
					v,err := sqltype.Integer(d,n)
					if err!=nil { t.Fatal(err) }
					if err=p.Advance(ctx,s,v,sqltype.Value{}); err!=nil { t.Fatal(err) }
				}
				v,err := p.Finalize(ctx,s)
				if err!=nil || v.Type()!=d { t.Fatal(v,err) }
				if len(values)==0 {
					if !v.IsNull() { t.Fatal("empty must be NULL",v) }
				} else {
					want := values[0]
					if len(values)==3 { want=[]int64{0,15,5}[index] }
					if len(values)==2 { want=[]int64{0,-1,-1}[index] }
					got,err := v.Integer()
					if err!=nil || got!=want { t.Fatal(tc.id,op,got,want,err) }
				}
				v,err=p.Finalize(ctx,isolated)
				if err!=nil || !v.IsNull() || v.Type()!=d { t.Fatal("state leaked",v,err) }
			}
			v,_ := sqltype.Integer(d,12)
			s,_ := p.Init(ctx)
			if err:=p.Advance(ctx,s,v,sqltype.Value{}); err!=nil { t.Fatal(err) }
			state:=s.(*integralBitwiseAggregateState)
			other,_ := sqltype.Integer(d,10)
			if allocations:=testing.AllocsPerRun(100,func() {
				state.value=v
				if err:=p.Advance(ctx,s,other,sqltype.Value{}); err!=nil { panic(err) }
			}); allocations!=0 { t.Fatalf("integral transition allocated %v",allocations) }
		}
	}
}

func TestBitwiseAggregateBitProvider(t *testing.T) {
	ctx,e := context.Background(),New()
	d,_ := e.CatalogRead().LookupType(sqltype.Bit)
	for index,op := range []aggregateOperation{aggregateBitAnd,aggregateBitOr,aggregateBitXor} {
		p := bitwiseAggregateUnitProvider(t,d,op)
		for _, tc := range []struct{ inputs []string; want []string }{
			{nil,nil}, {[]string{"00101"},[]string{"00101","00101","00101"}},
			{[]string{"",""},[]string{"","",""}},
			{[]string{"0","0"},[]string{"0","0","0"}},
			{[]string{"0","1"},[]string{"0","1","1"}},
			{[]string{"1","0"},[]string{"0","1","1"}},
			{[]string{"1","1"},[]string{"1","1","0"}},
			{[]string{"10101","11000","00111"},[]string{"00000","11111","01010"}},
			{[]string{"101010101","010101010"},[]string{"000000000","111111111","111111111"}},
			{[]string{strings.Repeat("0",64)+"1",strings.Repeat("1",64)+"0"},[]string{strings.Repeat("0",65),strings.Repeat("1",65),strings.Repeat("1",65)}},
		} {
			s,err := p.Init(ctx)
			if err!=nil { t.Fatal(err) }
			isolated,err := p.Init(ctx)
			if err!=nil || s==isolated { t.Fatal("shared state",err) }
			for _, input:=range tc.inputs {
				v,err:=sqltype.BitString(d,input)
				if err!=nil { t.Fatal(err) }
				if err=p.Advance(ctx,s,v,sqltype.Value{}); err!=nil { t.Fatal(err) }
			}
			v,err:=p.Finalize(ctx,s)
			if err!=nil || v.Type()!=d { t.Fatal(v,err) }
			if tc.want==nil {
				if !v.IsNull() { t.Fatal("empty group must be NULL") }
			} else {
				got,err:=v.Bits()
				if err!=nil || got!=tc.want[index] || v.IsNull() { t.Fatal(op,got,tc.want[index],err) }
				// Finalization seals owned scratch; a subsequent transition must
				// not mutate any already finalized value.
				before:=got
				operand,_:=sqltype.BitString(d,strings.Repeat("1",len(got)))
				if err=p.Advance(ctx,s,operand,sqltype.Value{}); err!=nil { t.Fatal(err) }
				if sealed,_:=v.Bits(); sealed!=before { t.Fatal("finalized payload aliased scratch") }
			}
			v,err=p.Finalize(ctx,isolated)
			if err!=nil || !v.IsNull() { t.Fatal("group state leaked",v,err) }
		}
		for _, first:=range []string{"","0","1"} {
			s,_:=p.Init(ctx)
			v,_:=sqltype.BitString(d,first)
			if err:=p.Advance(ctx,s,v,sqltype.Value{}); err!=nil { t.Fatal(err) }
			v,_=sqltype.BitString(d,"01")
			err:=p.Advance(ctx,s,v,sqltype.Value{})
			var diagnostic interface{ SQLState() string }
			if !errors.As(err,&diagnostic) || diagnostic.SQLState()!="22026" { t.Fatal("width accepted",err) }
			want:="cannot "+[]string{"AND","OR","XOR"}[index]+" bit strings of different sizes"
			if err.Error()!="stuntgres: "+want { t.Fatal(err,want) }
		}
	}
}

func TestBitwiseAggregateInvalidStatesAndCancellation(t *testing.T) {
	e,ctx:=New(),context.Background()
	for _, id:=range []sqltype.ID{sqltype.Int2,sqltype.Int4,sqltype.Int8,sqltype.Bit} {
		d,_:=e.CatalogRead().LookupType(id)
		other,_:=e.CatalogRead().LookupType(sqltype.Bool)
		for _, op:=range []aggregateOperation{aggregateBitAnd,aggregateBitOr,aggregateBitXor} {
			p:=bitwiseAggregateUnitProvider(t,d,op)
			s,_:=p.Init(ctx)
			value:=sqltype.Null(d)
			if err:=p.Advance(ctx,s,value,sqltype.Value{}); err==nil { t.Fatal("NULL reached strict transition") }
			if err:=p.Advance(ctx,s,sqltype.Boolean(other,true),sqltype.Value{}); err==nil { t.Fatal("foreign datum accepted") }
			for _, invalid:=range []any{nil,&countState{},(*integralBitwiseAggregateState)(nil),(*bitStringAggregateState)(nil)} {
				if _,err:=p.Finalize(ctx,invalid); err==nil { t.Fatal("invalid state accepted") }
				if err:=p.Advance(ctx,invalid,value,sqltype.Value{}); err==nil { t.Fatal("invalid state accepted") }
			}
			canceled,cancel:=context.WithCancel(ctx)
			cancel()
			if state,err:=p.Init(canceled); state!=nil || err!=context.Canceled { t.Fatal(state,err) }
			if err:=p.Advance(canceled,s,value,sqltype.Value{}); err!=context.Canceled { t.Fatal(err) }
			if _,err:=p.Finalize(canceled,s); err!=context.Canceled { t.Fatal(err) }
		}
	}
}
