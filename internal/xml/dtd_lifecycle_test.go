package xml

import (
 "context"
 "errors"
 "fmt"
 "reflect"
 "strings"
 "sync"
 "testing"
 "unsafe"
)

func TestXMLDTDCancellation(t *testing.T) {
 TestXMLDTDPICancellation(t)
 TestXMLDTDEntityStringCancellation(t)
 TestXMLDTDGeneralEntityCopyCancellation(t)
 long:=strings.Repeat("x",40000)
 defaults:=strings.Repeat(" x ",14000)
 var many strings.Builder
 for i:=range 64{fmt.Fprintf(&many,` x%d CDATA "v"`,i)}
 cases:=[]struct{phase,input string;collectOnly bool}{
  {"dtd",`<!DOCTYPE a [<!ELEMENT a EMPTY>`+strings.Repeat(" ",40000)+`]><a/>`,false},
  {"entityValue",`<!DOCTYPE a [<!ENTITY e "`+long+`">]><a>&e;</a>`,false},
  // A separator fatal disables callbacks, not the remaining value scan.
  {"entityValue",`<!DOCTYPE a [<!ENTITYe "`+long+`">]><a/>`,false},
  {"expandGeneral",`<!DOCTYPE a [<!ENTITY e "<b>`+long+`</b>"><!ENTITY outer "&e;">]><a>&outer;</a>`,false},
  {"parameterReference",`<!DOCTYPE a [<!ENTITY % p '<!ENTITY e "`+long+`">'>%p;]><a/>`,false},
  {"decodeParameterValue",`<!DOCTYPE a [<!ENTITY % q "`+long+`"><!ENTITY % p '<!ENTITY &#37; r "&#37;q;">'>%p;]><a/>`,false},
  {"decodeParameterValue",`<!DOCTYPE a [<!ENTITY % p "&#37;;`+long+`"><!ENTITY % r '<!ENTITY &#37; q "&#37;p;">'>%r;]><a/>`,false},
  {"decodeAttributeEntity",`<!DOCTYPE a [<!ENTITY e "`+long+`"><!ATTLIST a x CDATA "&e;">]><a/>`,false},
  {"attributeListDeclaration",`<!DOCTYPE a [<!ATTLIST a x CDATA "`+long+`">]><a/>`,false},
  {"normalizeAttributeScalar",`<!DOCTYPE a [<!ATTLIST a x NMTOKENS #IMPLIED>]><a x="`+defaults+`"/>`,false},
  {"normalizeTokens",`<!DOCTYPE a [<!ATTLIST a x NMTOKENS "`+defaults+`">]><a/>`,false},
  {"applyDefaults",`<!DOCTYPE a [<!ATTLIST a`+many.String()+`>]><a/>`,false},
  {"normalizeLines",`<!DOCTYPE a [<!ENTITY e "<b>`+long+`</b>">]><a>&e;</a>`,true},
  {"appendNode",`<!DOCTYPE a [<!ENTITY e "<b/><b/><b/><b/><b/>">]><a>&e;</a>`,true},
  {"collectValue",`<!DOCTYPE a [<!ENTITY e "&#13;`+long+`">]><a>&e;</a>`,true},
  {"cloneNode",`<!DOCTYPE a [<!ENTITY e "<b/><b/><b/><b/><b/>">]><a>&e;&e;</a>`,true},
  {"finishNode",`<!DOCTYPE a [<!ENTITY e "`+long+`">]><a>x&e;y</a>`,true},
 }
 for _,tc:=range cases{for _,collect:=range []bool{false,true}{if tc.collectOnly&&!collect{continue};for _,cause:=range []error{context.Canceled,context.DeadlineExceeded}{
  positions:=[]int{2,4}
  if tc.phase=="collectValue"||tc.phase=="finishNode"||tc.phase=="decodeParameterValue"||tc.phase=="decodeAttributeEntity"||tc.phase=="normalizeTokens"||tc.phase=="applyDefaults"{positions=append(positions,8,12)}
  for _,at:=range positions{
  c:=&phaseContext{Context:context.Background(),phase:tc.phase,after:at,cause:cause}
  r,d,e:=parse(c,tc.input,Options{Mode:Document},collect)
  if !c.admitted||!errors.Is(e,cause)||r.valid||r.original!=""||d!=nil{t.Fatalf("%s/%v/%d admitted %v seen %d: %+v %v",tc.phase,collect,at,c.admitted,c.seen,r,e)}
  if _,ok:=e.(interface{SQLState()string});ok{t.Fatal("context acquired SQLSTATE")}
  accept(t,"<healthy/>",Document)
 }}}}
 c:=&phaseContext{Context:context.Background(),phase:"applyDefaults",final:true,cause:context.Canceled}
 r,d,e:=parseExpanded(c,`<!DOCTYPE a [<!ATTLIST a x CDATA "v">]><a/>`,Options{Mode:Document})
 if !c.admitted||!errors.Is(e,context.Canceled)||r.valid||d!=nil{t.Fatal(r,d,e)}
}
func TestXMLDTDOwnershipConcurrency(t *testing.T) {
 input:=`<?xml version="1.1"?><!DOCTYPE a [<!ENTITY e "<b>ok</b>"><!ATTLIST a x CDATA "v">]><a>&e;&e;</a>`
 retained,tree,e:=parseExpanded(context.Background(),input,Options{Mode:Document});if e!=nil{t.Fatal(e)}
 if unsafe.StringData(retained.original)!=unsafe.StringData(input){t.Fatal("original spelling copied")}
 root:=dtdRoot(t,tree);if root.children[0]==root.children[1]||root.children[0].children[0]==root.children[1].children[0]{t.Fatal("mutable entity occurrence alias")}
 joined:=dtdRoot(t,dtdTree(t,`<!DOCTYPE a [<!ENTITY e "ok">]><a>x&e;y&e;z</a>`))
 if len(joined.children)!=1||joined.children[0].value!="xokyokz"||joined.children[0].textParts!=nil{t.Fatal("retained collector scratch")}
 root.children[0].children[0].value="changed";root.attributes[0].value="changed"
 if root.children[1].children[0].value!="ok"||retained.Original()!=input{t.Fatal("tree/result ownership")}
 warning:=retained.Diagnostics();warning[0].Message="changed"
 if retained.Diagnostics()[0].Message!="Unsupported version '1.1'"{t.Fatal("borrowed diagnostics")}
 // The public retained value has no parser, tree, map, context or input frame.
 typ:=reflect.TypeOf(retained);if typ.NumField()!=6{t.Fatal("Result ownership shape",typ)}
 growthText:=strings.Repeat("a",1000)
 growthInput:=`<!DOCTYPE a [<!ENTITY % p "`+growthText+`"><!ENTITY % r '<!ENTITY &#37; q "&#37;p;">`+strings.Repeat(" ",1000)+`<!ENTITY e "&#37;q;">'>%r;]><a>&e;</a>`
 growthFailure:=`<!DOCTYPE a [<!ENTITY % p "`+strings.Repeat("a",1401)+`"><!ENTITY % r '<!ENTITY &#37; q "&#37;p;">'>%r;]><a/>`
 lookupFailure:=`<!DOCTYPE a [<!ENTITY e "&#38;missing;tail&#38;;">]><a x="&e;"/>`
 nullableFailure:=`<!DOCTYPE a [<!ENTITY ext SYSTEM "x" NDATA n><!ENTITY e "&#38;ext;">]><a x="&e;"/>`
 var wg sync.WaitGroup;failures:=make(chan error,24)
 for worker:=range 24{wg.Add(1);go func(){defer wg.Done();for iteration:=range 8{
  s:=fmt.Sprintf(`<!DOCTYPE a [<!ENTITY e "%d-%d"><!ATTLIST a x CDATA "%d">]><a>&e;</a>`,worker,iteration,worker)
  r,d,e:=parseExpanded(context.Background(),s,Options{Mode:Mode(iteration%2)});if e!=nil||r.Original()!=s||d==nil{failures<-errors.New("independent declaration parse");return}
  var a *node;for _,n:=range d.nodes{if n.kind==elementNode{a=n}}
  r,d,e=parseExpanded(context.Background(),growthInput,Options{Mode:Mode(iteration%2)})
  if e!=nil||d==nil||r.Original()!=growthInput||len(r.Diagnostics())!=0{failures<-errors.New("independent semantic copy capacity");return}
  var expanded *node;for _,n:=range d.nodes{if n.kind==elementNode{expanded=n}}
  if expanded==nil||len(expanded.children)!=1||expanded.children[0].kind!=textNode||expanded.children[0].value!=growthText{failures<-errors.New("copy growth processed-value isolation");return}
  r,d,e=parseExpanded(context.Background(),growthFailure,Options{Mode:Mode(iteration%2)})
  var pe *ParseError
  if !errors.As(e,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil||len(pe.Diagnostics)!=1||pe.Diagnostics[0].Condition!="XML_ERR_ENTITY_LOOP"{failures<-errors.New("copy growth failure publication");return}
  r,d,e=parseExpanded(context.Background(),lookupFailure,Options{Mode:Mode(iteration%2)})
  if !errors.As(e,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil||len(pe.Diagnostics)!=2||pe.Diagnostics[0].Condition!="XML_ERR_UNDECLARED_ENTITY"||pe.Diagnostics[1].Condition!="XML_ERR_NAME_REQUIRED"{failures<-errors.New("lookup continuation failure isolation");return}
  r,d,e=parseExpanded(context.Background(),nullableFailure,Options{Mode:Mode(iteration%2)})
  if !errors.As(e,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil||len(pe.Diagnostics)!=1||pe.Diagnostics[0].Condition!="XML_ERR_UNPARSED_ENTITY"{failures<-errors.New("notation string-content failure isolation");return}
  r,d,e=parseExpanded(context.Background(),`<a>&ext;</a>`,Options{Mode:Mode(iteration%2)})
  if !errors.As(e,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil||len(pe.Diagnostics)!=1||pe.Diagnostics[0].Condition!="XML_ERR_UNDECLARED_ENTITY"{failures<-errors.New("notation content survived operation");return}
  if a==nil||a.children[0].value!=fmt.Sprintf("%d-%d",worker,iteration)||a.attributes[0].value!=fmt.Sprint(worker){failures<-errors.New("entity/default leakage");return}
  r,d,e=parseExpanded(context.Background(),`<a>&e;</a>`,Options{Mode:Document});if e==nil||r.valid||d!=nil{failures<-errors.New("entity declaration survived operation");return}
  r,d,e=parseExpanded(context.Background(),`<a/>`,Options{Mode:Document});if e!=nil||len(d.nodes[0].attributes)!=0||len(r.Diagnostics())!=0{failures<-errors.New("default/diagnostic leakage");return}
  c:=&phaseContext{Context:context.Background(),phase:"applyDefaults",after:1,cause:context.Canceled};r,d,e=parseExpanded(c,s,Options{Mode:Document});if !errors.Is(e,context.Canceled)||r.valid||d!=nil{failures<-errors.New("canceled operation publication");return}
  out,w,e:=OutputText(context.Background(),retained);if e!=nil||w!=(OutputWarning{})||out!=input||retained.Diagnostics()[0].Message!="Unsupported version '1.1'"{failures<-errors.New("retained output changed");return}
 }}()}
 wg.Wait();close(failures);for e:=range failures{t.Error(e)}
}
