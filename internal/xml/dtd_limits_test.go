package xml

import (
 "context"
 "errors"
 "fmt"
 "math"
 "strings"
 "testing"
)

func dtdChain(n int,binary bool)string {
 var b strings.Builder;b.WriteString(`<!DOCTYPE a [<!ENTITY e0 "x">`)
 for i:=1;i<=n;i++{value:=fmt.Sprintf("&e%d;",i-1);if binary{value+=value};fmt.Fprintf(&b,`<!ENTITY e%d "%s">`,i,value)}
 fmt.Fprintf(&b,`]><a>&e%d;</a>`,n);return b.String()
}
func TestXMLDTDLimits(t *testing.T) {
 dtdAccountingLimits(t)
 // Actual pinned boundaries, not native-output-derived limits. In particular,
 // outer source padding cannot change replacement-local density checks.
 for _,collect:=range []bool{false,true} {
  for _,tc:=range []struct{n int;binary bool;accept bool}{{3,true,true},{4,true,false},{5,true,false},{16,false,true},{17,false,false},{38,false,false}} {
   input:=dtdChain(tc.n,tc.binary)
   for _,padding:=range []int{0,100,1000,10000} {
    if padding!=0&&!tc.binary{continue};s:=strings.Replace(input,"<a>","<a>"+strings.Repeat("x",padding),1)
    r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect)
    if tc.accept{if e!=nil||!r.valid||collect&&d==nil{t.Fatal(tc,r,e)};continue}
    var pe *ParseError;if !errors.As(e,&pe)||r.valid||d!=nil{t.Fatal(tc,r,e)}
    var blocks []string
    if tc.binary {
     for i:=3;i<tc.n;i++{local:=fmt.Sprintf("&e%d;&e%d;",i,i);caret:=len(fmt.Sprintf("&e%d;",i));blocks=append(blocks,"line 1: Detected an entity reference loop\n"+local+"\n"+strings.Repeat(" ",caret)+"^")}
    }else if tc.n==17{blocks=append(blocks,"line 1: Detected an entity reference loop\n&e16;\n     ^")}else{
     for i:=18;i<38;i++{blocks=append(blocks,fmt.Sprintf("line 1: Detected an entity reference loop\n&e%d;\n     ^",i))}
    }
    end:=len(s)-4;source:=s[end-80:end];blocks=append(blocks,"line 1: Detected an entity reference loop\n"+source+"\n"+strings.Repeat(" ",79)+"^")
    if pe.Detail()!=strings.Join(blocks,"\n"){t.Fatalf("chain %+v/%d/%v:\n%s\nwant:\n%s",tc,padding,collect,pe.Detail(),strings.Join(blocks,"\n"))}
   }
  }
  for _,n:=range []int{127,128,129} {
   s:=`<!DOCTYPE a [<!ELEMENT a `+strings.Repeat("(",n)+"b"+strings.Repeat(",b)",n)+`>]><a/>`
   r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect)
   if n<=128{if e!=nil||!r.valid{t.Fatal(n,e)};continue}
   var pe *ParseError;if !errors.As(e,&pe)||d!=nil||r.valid{t.Fatal(n,e)}
   var want []string;for _,m:=range []string{"xmlParseElementChildrenContentDecl : depth 129 too deep, use XML_PARSE_HUGE","expected '>'","internal error: xmlParseInternalSubset: error detected in Markup declaration","DOCTYPE improperly terminated","Start tag expected, '<' not found"}{want=append(want,"line 1: "+m+"\n"+strings.Repeat("(",80)+"\n"+strings.Repeat(" ",79)+"^")}
   if pe.Detail()!=strings.Join(want,"\n"){t.Fatal(pe.Detail())}
  }
  for _,n:=range []int{49999,50000,50001} {
   s:=`<!DOCTYPE a SYSTEM "`+strings.Repeat("x",n)+`"><a/>`;r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect)
   if n<=50000{if e!=nil||!r.valid{t.Fatal(n,e)};continue}
   var pe *ParseError;if !errors.As(e,&pe)||d!=nil||r.valid{t.Fatal(n,e)}
   var want []string;for _,m:=range []string{"Name too long: SystemLiteral","SYSTEM or PUBLIC, the URI is missing","DOCTYPE improperly terminated"}{want=append(want,"line 1: "+m+"\n"+strings.Repeat("x",80)+"\n"+strings.Repeat(" ",79)+"^")};want=append(want,"line 1: Start tag expected, '<' not found\n"+strings.Repeat("x",79)+"\"\n"+strings.Repeat(" ",79)+"^")
   if pe.Detail()!=strings.Join(want,"\n"){t.Fatal(pe.Detail())}
  }
  for _,n:=range []int{9999999,10000000,10000001} {
   s:=`<!DOCTYPE a [<!ENTITY e "`+strings.Repeat("x",n)+`">]><a/>`;r,d,e:=parse(context.Background(),s,Options{Mode:Document},collect)
   if n<=10000000{if e!=nil||!r.valid{t.Fatal(n,e)};continue}
   var pe *ParseError;if !errors.As(e,&pe)||d!=nil||r.valid{t.Fatal(n,e)}
   want:="line 1: entity value too long\n"+strings.Repeat("x",80)+"\n"+strings.Repeat(" ",79)+"^\nline 1: xmlParseEntityDecl: entity e not terminated\n"+strings.Repeat("x",80)+"\n"+strings.Repeat(" ",79)+"^"
   if pe.Detail()!=want{t.Fatal(pe.Detail())}
  }
 }
 // Exact product comparisons and overflow rejection need no enormous buffers.
 if productLess(math.MaxUint64,3,math.MaxUint64,3)||!productLess(math.MaxUint64-1,3,math.MaxUint64,3)||!productLess(math.MaxUint64,3,math.MaxUint64,10){t.Fatal("checked density comparison")}
 p:=parser{ctx:context.Background(),s:"x",line:1,column:1,entityCount:math.MaxUint64}
 if p.addEntityCount(1)||!p.failed()||p.events[0].Condition!="XML_ERR_ENTITY_LOOP"{t.Fatal("entity counter overflow")}
 if appendSize(math.MaxInt,1){t.Fatal("append size overflow")}
}
