package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func TestXMLDTDProcessingInstructions(t *testing.T) {
 separators:=strings.Repeat(" ",40000)
 for _,tc:=range []struct{pi,want string}{
  {"<?go?>",""},
  {"<?go "+separators+"?>",""},
  {"<?go\r\n\t"+separators+"data\r\nx?>","data\nx"},
 }{for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
  input:="<!DOCTYPE a><a>"+tc.pi+"</a>"
  r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
  if err!=nil||!r.valid||r.Original()!=input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("mode %v collect %t: %+v %v",mode,collect,r,err)}
  if !collect{if d!=nil{t.Fatal("validation retained a tree")};continue}
  root:=dtdRoot(t,d)
  if len(root.children)!=1||root.children[0].kind!=piNode||root.children[0].name!="go"||root.children[0].value!=tc.want{t.Fatalf("mode %v: children %+v want go/%q",mode,root.children,tc.want)}
 }}}
 // A PI also establishes non-text entity shape without a collector; repeated
 // collecting occurrences retain the parsed target rather than its delimiter.
 input:=`<!DOCTYPE a [<!ENTITY e "<?go `+separators+`?>">]><a>&e;&e;</a>`
 root:=dtdRoot(t,dtdTree(t,input))
 if len(root.children)!=2||root.children[0]==root.children[1]{t.Fatalf("PI occurrences %+v",root.children)}
 for _,n:=range root.children{if n.kind!=piNode||n.name!="go"||n.value!=""{t.Fatalf("PI %+v",n)}}
}

func TestXMLDTDPICancellation(t *testing.T) {
 input:="<!DOCTYPE a><a><?go "+strings.Repeat(" ",40000)+"?></a>"
 for _,cause:=range []error{context.Canceled,context.DeadlineExceeded}{for _,collect:=range []bool{false,true}{
  // Polls during the actual separator scan, independently of PI payload work.
  for _,at:=range []int{2,4}{
   c:=&phaseContext{Context:context.Background(),phase:"spaces",after:at,cause:cause}
   r,d,err:=parse(c,input,Options{Mode:Document},collect)
   if !c.admitted||!errors.Is(err,cause)||r.valid||r.original!=""||d!=nil{t.Fatalf("separator collect %t poll %d admitted %t: %+v %v",collect,at,c.admitted,r,err)}
  }
  // Completion follows the full long separator scan. Validation-only parsing
  // must not enter collector-only normalization; collection remains cancellable.
  for _,at:=range []int{1,2}{
   c:=&phaseContext{Context:context.Background(),phase:"collectMarkup",after:at,cause:cause}
   r,d,err:=parse(c,input,Options{Mode:Document},collect)
   if !collect{
    if c.admitted||err!=nil||!r.valid||d!=nil{t.Fatalf("validation entered PI collector: admitted %t %+v %v",c.admitted,r,err)}
   }else if !c.admitted||!errors.Is(err,cause)||r.valid||r.original!=""||d!=nil{t.Fatalf("completion poll %d admitted %t: %+v %v",at,c.admitted,r,err)}
  }
  c:=&phaseContext{Context:context.Background(),phase:"pi",final:true,cause:cause}
  r,d,err:=parse(c,input,Options{Mode:Document},collect)
  if !c.admitted||!errors.Is(err,cause)||r.valid||r.original!=""||d!=nil{t.Fatalf("after PI collect %t admitted %t: %+v %v",collect,c.admitted,r,err)}
  accept(t,"<healthy/>",Document)
 }}
}
