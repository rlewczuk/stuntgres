package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type parserUnitContext struct {
	stringUnitContext
	lookups int
	failure error
}
func (c *parserUnitContext) ResolveFTSParser(sqltype.Value) error { c.lookups++; return c.failure }

func parserUnitNode(t *testing.T,parse bool,order *[]int) FTSParser {
	t.Helper()
	types:=inspectCarrierTypes()
	members:=[]sqltype.CompositeMember{{Attribute:1,Name:"tokid",Usage:types[sqltype.Int4].Usage()},{Attribute:2,Name:"token",Usage:types[sqltype.Text].Usage()}}
	if !parse { members[1].Name="alias"; members=append(members,sqltype.CompositeMember{Attribute:3,Name:"description",Usage:types[sqltype.Text].Usage()}) }
	shape,err:=sqltype.RecordShape(members)
	if err!=nil { t.Fatal(err) }
	n:=FTSParser{Info:expr.Info{Usage:shape.Usage()},Parse:parse,Order:[2]int{0,1}}
	for i,text:=range []string{"default","Word"} {
		if i>=n.arity() { break }
		n.Args[i]=stringUnitArgument{Info:expr.Info{Usage:types[sqltype.Text].Usage()},value:sqltype.String(types[sqltype.Text],text),order:order,index:i}
	}
	return n
}

func TestFTSParserSource(t *testing.T) {
	for _,parse:=range []bool{false,true} {
		var order []int
		n:=parserUnitNode(t,parse,&order)
		c:=&parserUnitContext{stringUnitContext:stringUnitContext{Context:context.Background()}}
		if err:=n.Validate(); err!=nil { t.Fatal(err) }
		a,err:=n.OpenSource(c)
		if err!=nil { t.Fatal(err) }
		wantOrder:=[]int{0}
		if parse { wantOrder=[]int{0,1} }
		if !reflect.DeepEqual(order,wantOrder) || c.lookups!=1 { t.Fatal("operand/resolver demand",order,c.lookups) }
		b,err:=n.OpenSource(c)
		if err!=nil { t.Fatal(err) }
		wantCount:=23
		if parse { wantCount=1 }
		var retained sqltype.Value
		for i:=range wantCount {
			v,present,err:=a.Next(c.Context)
			if err!=nil || !present || v.Type()!=n.Usage.Type { t.Fatal("row",i,v,present,err) }
			if i==0 { retained=v }
		}
		v,present,err:=a.Next(c.Context)
		if err!=nil || present || !v.IsNull() || v.Type()!=n.Usage.Type { t.Fatal("typed EOF",v,present,err) }
		a.Close(); a.Close()
		if a.State!=nil { t.Fatal("closed cursor retained payload") }
		v,present,err=b.Next(c.Context)
		if err!=nil || !present || retained.Type()!=v.Type() { t.Fatal("independent source",err) }
		b.Close()
		left:=n.Args[0].(stringUnitArgument)
		left.value=sqltype.Null(left.Usage.Type)
		n.Args[0]=left
		c.lookups=0
		empty,err:=n.OpenSource(c)
		if err!=nil || c.lookups!=0 { t.Fatal("strict NULL demanded lookup",err,c.lookups) }
		v,present,err=empty.Next(c.Context)
		if err!=nil || present || !v.IsNull() || v.Type()!=n.Usage.Type { t.Fatal("strict typed EOF",err) }
		if parse {
			right:=n.Args[1].(stringUnitArgument)
			right.err=errors.New("later argument failure")
			n.Args[1]=right
			if _,err:=n.OpenSource(c); !errors.Is(err,right.err) { t.Fatal("NULL suppressed later operand",err) }
		}
		canceled,cancel:=context.WithCancel(context.Background()); cancel()
		c.Context=canceled
		if _,err:=n.OpenSource(c); !errors.Is(err,context.Canceled) { t.Fatal("cancellation",err) }
	}
	var order []int
	n:=parserUnitNode(t,true,&order)
	n.Args[0],n.Args[1]=n.Args[1],n.Args[0]
	n.Order=[2]int{1,0}
	c:=&parserUnitContext{stringUnitContext:stringUnitContext{Context:context.Background()}}
	cursor,err:=n.OpenSource(c)
	if err!=nil { t.Fatal(err) }
	cursor.Close()
	if !reflect.DeepEqual(order,[]int{1,0}) { t.Fatal("reordered named operands evaluated in formal order",order) }
	failure:=errors.New("resolver rejected")
	c.failure=failure
	if _,err:=n.OpenSource(c); !errors.Is(err,failure) { t.Fatal("resolver failure",err) }
	if _,err:=n.Rebuild(n.Info,nil); err==nil { t.Fatal("missing operands accepted") }
	if _,err:=n.Rebuild(n.Info,n.Children()); err!=nil { t.Fatal("rebuild",err) }
	rendered,err:=n.Render()
	if err!=nil || len(rendered.Args)!=2 || rendered.Args[0].Label!="txt" || rendered.Args[1].Label!="parser_name" { t.Fatal("written rendering",rendered,err) }
	for i := range n.arity() {
		got,err:=n.CaptureOperand(i)
		if err!=nil || !reflect.DeepEqual(got,n.Args[i]) { t.Fatal("written capture",i,err) }
	}
	members:=[]sqltype.CompositeMember{n.Usage.Type.CompositeMember(0),n.Usage.Type.CompositeMember(1)}
	shape,err:=sqltype.RecordShape(members)
	if err!=nil { t.Fatal(err) }
	info:=n.Info
	info.Usage=shape.Usage()
	remapped,err:=n.Rebuild(info,n.Children())
	if err!=nil || remapped.ResultUsage().Type!=shape { t.Fatal("record remap",err) }
	parserUnitConcurrent(t,n)
}

type parserUnitConstant struct { expr.Info; value sqltype.Value }
func (a parserUnitConstant) Eval(expr.Context) (sqltype.Value,error) { return a.value,nil }

func parserUnitConcurrent(t *testing.T,n FTSParser) {
	t.Helper()
	for i,arg := range n.Children() {
		a:=arg.(stringUnitArgument)
		n.Args[i]=parserUnitConstant{Info:a.Info,value:a.value}
	}
	opened:=make(chan struct{},2)
	release:=make(chan struct{})
	results:=make(chan error,2)
	for range 2 {
		go func() {
			c:=&parserUnitContext{stringUnitContext:stringUnitContext{Context:context.Background()}}
			cursor,err:=n.OpenSource(c)
			opened<-struct{}{}
			<-release
			if err==nil {
				value,present,failure:=cursor.Next(c.Context)
				err=failure
				if err==nil && (!present || value.CompositeLen()!=2) { err=errors.New("concurrent source lost its row") }
				if err==nil {
					text,failure:=value.CompositeField(1).Text()
					err=failure
					if err==nil && text!="Word" { err=errors.New("concurrent source changed document bytes") }
				}
			}
			cursor.Close()
			results<-err
		}()
	}
	<-opened
	<-opened
	close(release)
	for range 2 {
		if err:=<-results; err!=nil { t.Error(err) }
	}
}
