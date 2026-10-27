package xml

import (
 "context"
 "errors"
 "math"
 "reflect"
 "strings"
 "testing"
)

func TestXMLDTDStringPE(t *testing.T) {
 TestXMLDTDStringPESyntax(t)
 TestXMLDTDStringNameLimits(t)
 TestXMLDTDStringGELookup(t)
 TestXMLDTDStringCopyGrowth(t)
 TestXMLDTDStringExternalContent(t)
 // Independently captured before this remedy: PostgreSQL18.6/180006 UTF8/C,
 // Debian libxml2 2.9.14+dfsg-1.3~deb12u6, pg_input_error_info in both modes.
 // The whole Detail is observed; structured frame fields are source-derived.
 want:=Diagnostic{Condition:"XML_ERR_UNDECLARED_ENTITY",Domain:ParserDomain,Severity:Fatal,Line:1,Column:25,Offset:24,Message:"PEReference: %missing; not found",SourceContext:`<!ENTITY % q "%missing;">`,Caret:"                        ^",Source:EntitySource,EntityName:"p"}
 detail:="line 1: PEReference: %missing; not found\n<!ENTITY % q \"%missing;\">\n                        ^"
 for _,standalone:=range []string{"",` standalone="no"`,` standalone="yes"`}{for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
  input:=`<?xml version="1.0"`+standalone+`?><!DOCTYPE a [<!ENTITY % p '<!ENTITY &#37; q "&#37;missing;">'>%p;]><a/>`
  r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
  if standalone==` standalone="yes"`{
   var pe *ParseError
   if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("standalone yes mode %v collect %t: %+v tree %+v err %v",mode,collect,r,d,err)}
   primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
   if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=detail||!reflect.DeepEqual(pe.Diagnostics,[]Diagnostic{want}){t.Fatalf("mode %v collect %t: %+v Detail %q diagnostics %+v",mode,collect,pe,pe.Detail(),pe.Diagnostics)}
  }else{
   if err!=nil||!r.valid||r.Original()!=input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("standalone %q mode %v collect %t: %+v %v",standalone,mode,collect,r,err)}
   if collect{if root:=dtdRoot(t,d);len(root.children)!=0{t.Fatalf("unexpected replacement nodes: %+v",root.children)}}else if d!=nil{t.Fatal("validation retained a tree")}
  }
 }}}
 dtdStringPEBookkeeping(t)
}

func dtdStringPEBookkeeping(t *testing.T) {
 t.Helper()
 // Source-derived parser.c xmlParseStringPEReference + its string-decoder
 // caller: increment once, report, check nil, set hasPErefs, check nil again.
 // Pinned parser.c235-280 checks BOTH fatal and warning undeclared codes.
 // Replacing an old fatal with a suppressed warning does not exempt counts
 // above 10000; the warning itself must then produce the single loop event.
 for _,tc:=range []struct{name string;yes,external,pe bool;count uint64;prior Condition;conditions []Condition;ok bool}{
  {name:"prior PE",pe:true,ok:true},
  {name:"external subset",external:true,ok:true},
  {name:"standalone override",yes:true,pe:true,conditions:[]Condition{"XML_ERR_UNDECLARED_ENTITY"},ok:true},
  {name:"no leniency",conditions:[]Condition{"XML_ERR_UNDECLARED_ENTITY"},ok:true},
  {name:"fatal at count threshold",yes:true,pe:true,count:9999,conditions:[]Condition{"XML_ERR_UNDECLARED_ENTITY"},ok:true},
  {name:"warning at count threshold",pe:true,count:9999,ok:true},
  {name:"warning above count threshold",pe:true,count:10000,conditions:[]Condition{"XML_ERR_ENTITY_LOOP"}},
  {name:"warning replaces prior undeclared above threshold",pe:true,count:10000,prior:"XML_ERR_UNDECLARED_ENTITY",conditions:[]Condition{"XML_ERR_ENTITY_LOOP"}},
  {name:"fatal count check before continuation",yes:true,pe:true,count:10000,conditions:[]Condition{"XML_ERR_UNDECLARED_ENTITY","XML_ERR_ENTITY_LOOP"}},
 }{t.Run(tc.name,func(t *testing.T){
  p:=parser{ctx:context.Background(),s:"%missing;",i:9,line:1,column:10,dtdState:&dtdState{external:tc.external,peReferences:tc.pe},entityCount:tc.count,lastEntityError:tc.prior}
  if tc.yes{p.declaration.Standalone=Yes}
  value,size,lt,ok:=p.decodeEntityString("%missing;",substituteParameter,1,true,nil)
  if p.err!=nil||ok!=tc.ok||value!=""||size!=0||lt||p.entityCount!=tc.count+1||!p.dtdState.peReferences{t.Fatalf("value %q size %d lt %t ok %t count %d PE %t err %v",value,size,lt,ok,p.entityCount,p.dtdState.peReferences,p.err)}
  var events []Diagnostic;var blocks []string
  for _,condition:=range tc.conditions{
   message:="PEReference: %missing; not found";if condition=="XML_ERR_ENTITY_LOOP"{message="Detected an entity reference loop"}
   events=append(events,Diagnostic{Condition:condition,Domain:ParserDomain,Severity:Fatal,Line:1,Column:10,Offset:9,Message:message,SourceContext:"%missing;",Caret:"         ^",Source:OriginalSource})
   blocks=append(blocks,"line 1: "+message+"\n%missing;\n         ^")
  }
  last:=Condition("XML_WAR_UNDECLARED_ENTITY");if len(tc.conditions)!=0{last=tc.conditions[len(tc.conditions)-1]}
  if !reflect.DeepEqual(p.events,events)||p.lastEntityError!=last||p.failed()!=(len(events)!=0)||p.parseError().Detail()!=strings.Join(blocks,"\n"){t.Fatalf("events %+v last %s failed %t",p.events,p.lastEntityError,p.failed())}
 })}
}

func TestXMLDTDEntityStringMalformedUTF8(t *testing.T) {
 // Raw invalid bytes were independently delivered to actual PG xml_recv by
 // binary COPY, not coerced through an invalid UTF8 SQL text literal. Both
 // requested modes yielded this sole decoder block. Here ordinary Text input
 // exercises the shared parser's existing failure-only single-byte contract.
 prefix:=`<!DOCTYPE a [<!ENTITY e "&#65;`
 input:=prefix+strings.Repeat("\x80",5000)+`">]><a/>`
 source:=prefix+strings.Repeat("\x80",50)
 message:="Input is not proper UTF-8, indicate encoding !\nBytes: 0x80 0x80 0x80 0x80"
 detail:="line 1: "+message+"\n"+source+"\n                              ^"
 diagnostic:=Diagnostic{Condition:"XML_ERR_INVALID_CHAR",Domain:ParserDomain,Severity:Fatal,Line:1,Column:31,Offset:30,Message:message,SourceContext:source,Caret:"                              ^",Source:OriginalSource}
 for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
  // A background context deliberately requires natural failed-parse completion,
  // not cancellation as an escape from a zero-progress decoder loop.
  r,d,err:=parse(context.Background(),input,Options{Mode:mode,InputKind:Text},collect)
  var pe *ParseError
  if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("mode %v collect %t: %+v tree %+v err %v",mode,collect,r,d,err)}
  primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
  if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=detail||!reflect.DeepEqual(pe.Diagnostics,[]Diagnostic{diagnostic}){t.Fatalf("mode %v collect %t: %+v Detail %q diagnostics %+v",mode,collect,pe,pe.Detail(),pe.Diagnostics)}
  accept(t,"<healthy/>",Document)
 }}
}

func TestXMLDTDEntityStringUTF8Chunks(t *testing.T) {
 // A+4095x+é+20y was independently observed through the same-parser INDENT
 // projection. Additional 3/4-byte alignments and tokenized attributes follow
 // XML scalar/reference semantics, not candidate-derived output.
 for _,scalar:=range []string{"é","€","𐀀"}{for padding:=4092;padding<=4095;padding++{
  body:=strings.Repeat("x",padding)+scalar+strings.Repeat("y",20)
  want:="A"+body
  input:=`<!DOCTYPE a [<!ENTITY e "&#65;`+body+`"><!ATTLIST a x NMTOKENS #IMPLIED>]><a x="&e;">&e;</a>`
  for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
   r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
   if err!=nil||!r.valid||r.Original()!=input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("scalar %q padding %d mode %v collect %t: %+v %v",scalar,padding,mode,collect,r,err)}
   if !collect{if d!=nil{t.Fatal("validation retained a tree")};continue}
   root:=dtdRoot(t,d)
   if len(root.children)!=1||root.children[0].kind!=textNode||root.children[0].value!=want||len(root.attributes)!=1||root.attributes[0].name!="x"||root.attributes[0].value!=want{t.Fatalf("scalar %q padding %d: children %+v attributes %+v",scalar,padding,root.children,root.attributes)}
  }}
 }}
}

func TestXMLDTDEntityStringCancellation(t *testing.T) {
 long:=strings.Repeat("a",200000)
 for _,tc:=range []struct{input,phase string;polls []int}{
  {`<!DOCTYPE a [<!ENTITY e "&#65;`+strings.Repeat("\x80",50000)+`">]><a/>`,"decodeEntityString",[]int{24,32}},
  {`<!DOCTYPE a [<!ENTITY e "&#65;`+strings.Repeat("xxx𐀀",8000)+`">]><a/>`,"decodeEntityString",[]int{24,32}},
  // These polls belong to actual nested Name scanning/residual decoding, not
  // the earlier declaration scan that initially generates the percent/amp.
  {`<!DOCTYPE a [<!ENTITY % p "&#37;`+long+`;"><!ENTITY % r '<!ENTITY &#37; q "&#37;p;">'>%r;]><a/>`,"decodeParameterValue",[]int{24,32}},
  {`<!DOCTYPE a [<!ENTITY e "&#38;`+long+`;">]><a x="&e;"/>`,"decodeAttributeEntity",[]int{24,32}},
  {`<!DOCTYPE a [<!ENTITY e "&#38;`+long+`;"><!ATTLIST a x CDATA "&e;">]><a/>`,"decodeAttributeEntity",[]int{24,32}},
  {`<!DOCTYPE a [<!ENTITY e "&#38;missing;`+long+`&#38;;">]><a x="&e;"/>`,"decodeAttributeEntity",[]int{24,32}},
  {`<!DOCTYPE a [<!ENTITY e "&#38;missing;`+long+`&#38;;"><!ATTLIST a x CDATA "&e;">]><a/>`,"decodeAttributeEntity",[]int{24,32}},
  {`<!DOCTYPE a [<!ENTITY % p "`+long+`"><!ENTITY % r '<!ENTITY &#37; q "&#37;p;">'>%r;]><a/>`,"growEntityString",[]int{2,4}},
  {`<!DOCTYPE a [<!ENTITY ext SYSTEM "x"><!ENTITY e "&#38;ext;">]><a x="&e;"/>`,"reserveEntityString",[]int{1,2}},
  {`<!DOCTYPE a [<!ENTITY ext SYSTEM "x"><!ENTITY e "&#38;ext;"><!ATTLIST a x CDATA "&e;">]><a/>`,"reserveEntityString",[]int{1,2}},
  {`<!DOCTYPE a [<!ENTITY ext SYSTEM "x" NDATA `+strings.Repeat("n",40000)+`><!ENTITY e "&#38;ext;">]><a x="&e;"/>`,"inspectEntity",[]int{8,12}},
  {`<!DOCTYPE a [<!ENTITY ext SYSTEM "x" NDATA `+strings.Repeat("n",40000)+`><!ENTITY e "&#38;ext;"><!ATTLIST a x CDATA "&e;">]><a/>`,"inspectEntity",[]int{8,12}},
  {`<!DOCTYPE a [<!ENTITY e "&`+long+`;">]><a/>`,"validateEntityValueReferences",[]int{2,4}},
 }{
  for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{for _,cause:=range []error{context.Canceled,context.DeadlineExceeded}{for _,at:=range tc.polls{
   c:=&phaseContext{Context:context.Background(),phase:tc.phase,after:at,cause:cause}
   r,d,err:=parse(c,tc.input,Options{Mode:mode},collect)
   if !c.admitted||c.seen<at||!errors.Is(err,cause)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("%s mode %v collect %t poll %d admitted %t seen %d: %+v tree %+v err %v",tc.phase,mode,collect,at,c.admitted,c.seen,r,d,err)}
   if _,ok:=err.(interface{SQLState()string});ok{t.Fatal("context error acquired SQLSTATE")}
   accept(t,"<healthy/>",Document)
  }}}}
 }
}

func TestXMLDTDStringPESyntax(t *testing.T) {
 // Independent PostgreSQL18.6/180006 UTF8/C, Debian libxml2 captures precede
 // this remedy. String syntax errors keep the active r input, not a fictitious
 // p input. Mixed errors prove residual-pointer continuation. Input-based PE
 // contrasts deliberately retain their distinct identities and recovery.
 type fault struct{condition Condition;message string}
 nameError:=fault{"XML_ERR_NAME_REQUIRED","xmlParseStringPEReference: no name"}
 semiError:=fault{"XML_ERR_ENTITYREF_SEMICOL_MISSING","EntityRef: expecting ';'"}
 const frame=`<!ENTITY % q "%p;">`
 nested:=func(value string)string{return `<!DOCTYPE a [<!ENTITY % p "`+value+`"><!ENTITY % r '<!ENTITY &#37; q "&#37;p;">'>%r;]><a/>`}
 for _,tc:=range []struct{name,input,context,entity string;source DiagnosticSource;offset int;events []fault;q bool}{
  {"string missing name",nested("&#37;;"),frame,"r",EntitySource,18,[]fault{nameError},false},
  {"string missing semicolon",nested("&#37;missing"),frame,"r",EntitySource,18,[]fault{semiError},false},
  {"string name then semicolon",nested("&#37;;tail&#37;missing"),frame,"r",EntitySource,18,[]fault{nameError,semiError},false},
  {"string semicolon then name",nested("&#37;p&#37;;"),frame,"r",EntitySource,18,[]fault{semiError,nameError},false},
  {"legal nested",nested("ok"),"","",OriginalSource,0,nil,true},
  {"input missing name",`<!DOCTYPE a [%;]><a/>`,"","",OriginalSource,14,[]fault{
   {"XML_ERR_PEREF_NO_NAME","PEReference: no name"},
   {"XML_ERR_INTERNAL_ERROR","internal error: xmlParseInternalSubset: error detected in Markup declaration"},
   {"XML_ERR_DOCTYPE_NOT_FINISHED","DOCTYPE improperly terminated"},
   {"XML_ERR_DOCUMENT_EMPTY","Start tag expected, '<' not found"},
  },false},
  {"input missing semicolon",`<!DOCTYPE a [%missing]><a/>`,"","",OriginalSource,21,[]fault{{"XML_ERR_PEREF_SEMICOL_MISSING","PEReference: expecting ';'"}},false},
  {"legal input",`<!DOCTYPE a [<!ENTITY % p "">%p;]><a/>`,"","",OriginalSource,0,nil,false},
 }{t.Run(tc.name,func(t *testing.T){
  for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
   r,d,err:=parse(context.Background(),tc.input,Options{Mode:mode},collect)
   if len(tc.events)==0{
    if err!=nil||!r.valid||r.Original()!=tc.input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("mode %v collect %t: %+v %v",mode,collect,r,err)}
    if collect{
     if d==nil{t.Fatal("missing transient document")}
     root:=dtdRoot(t,d);if len(root.children)!=0||len(root.attributes)!=0{t.Fatalf("unexpected body/default effects: %+v",root)}
     if tc.q{
      found:=false
      for _,declaration:=range d.nodes[0].doctype.declarations{if declaration.kind=="ENTITY"&&declaration.name=="q"{found=true;if !declaration.parameter||declaration.external||declaration.unparsed||declaration.value!="ok"{t.Fatalf("nested q identity %+v",declaration)}}}
      if !found{t.Fatal("missing eagerly expanded q")}
     }
    }else if d!=nil{t.Fatal("validation retained a tree")}
   }else{
    var pe *ParseError
    if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("mode %v collect %t: %+v tree %+v err %v",mode,collect,r,d,err)}
    source:=tc.context;if tc.source==OriginalSource{source=tc.input}
    var diagnostics []Diagnostic;var blocks []string
    for _,event:=range tc.events{
     caret:=strings.Repeat(" ",tc.offset)+"^"
     diagnostics=append(diagnostics,Diagnostic{Condition:event.condition,Domain:ParserDomain,Severity:Fatal,Line:1,Column:tc.offset+1,Offset:tc.offset,SourceBaseOffset:0,Message:event.message,SourceContext:source,Caret:caret,Source:tc.source,EntityName:tc.entity})
     blocks=append(blocks,"line 1: "+event.message+"\n"+source+"\n"+caret)
    }
    primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
    if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=strings.Join(blocks,"\n")||!reflect.DeepEqual(pe.Diagnostics,diagnostics){t.Fatalf("mode %v collect %t: %+v Detail %q diagnostics %+v",mode,collect,pe,pe.Detail(),pe.Diagnostics)}
   }
   accept(t,"<healthy/>",mode)
  }}
 })}
 // Matching-source pointer witnesses: malformed syntax consumes the percent
 // and parsed Name, performs no lookup/count/PE-seen update, then the caller
 // checks nil and continues. Decoder success does not clear illformedness.
 for _,tc:=range []struct{input,want string;events []fault}{
  {"%;",";",[]fault{nameError}},
  {"%missing","",[]fault{semiError}},
  {"%;tail%missing",";tail",[]fault{nameError,semiError}},
  {"%p%;",";",[]fault{semiError,nameError}},
 }{for _,emit:=range []bool{false,true}{
  p:=parser{ctx:context.Background(),s:frame,i:18,line:1,column:19,source:EntitySource,entityName:"r",inDTD:true,dtdState:&dtdState{}}
  value,size,lt,ok:=p.decodeEntityString(tc.input,substituteParameter,1,emit,nil)
  want:=tc.want;if !emit{want=""}
  if p.err!=nil||!ok||!p.failed()||p.stopped||value!=want||size!=uint64(len(tc.want))||lt||p.entityCount!=0||p.dtdState.peReferences||p.lastEntityError!=tc.events[len(tc.events)-1].condition{t.Fatalf("%q emit %t: value %q size %d lt %t ok %t count %d PE %t err %v events %+v",tc.input,emit,value,size,lt,ok,p.entityCount,p.dtdState.peReferences,p.err,p.events)}
  var diagnostics []Diagnostic
  for _,event:=range tc.events{diagnostics=append(diagnostics,Diagnostic{Condition:event.condition,Domain:ParserDomain,Severity:Fatal,Line:1,Column:19,Offset:18,SourceBaseOffset:0,Message:event.message,SourceContext:frame,Caret:"                  ^",Source:EntitySource,EntityName:"r"})}
  if !reflect.DeepEqual(p.events,diagnostics){t.Fatalf("%q emit %t: diagnostics %+v",tc.input,emit,p.events)}
 }}
}

func TestXMLDTDStringNameLimits(t *testing.T) {
 // Independent PostgreSQL18.6/180006 UTF8/C and pinned Debian libxml2 captures
 // precede production edits. Full Detail is observed; symbolic/frame fields
 // and accepted transient projections are matching-source assertions.
 type fault struct{condition Condition;message string}
 lengthError:=fault{"XML_ERR_NAME_TOO_LONG","Name too long: NCName"}
 peName:=fault{"XML_ERR_NAME_REQUIRED","xmlParseStringPEReference: no name"}
 geName:=fault{"XML_ERR_NAME_REQUIRED","xmlParseStringEntityRef: no name"}
 semiError:=fault{"XML_ERR_ENTITYREF_SEMICOL_MISSING","EntityRef: expecting ';'"}
 loop:=fault{"XML_ERR_ENTITY_LOOP","Detected an entity reference loop"}
 limit:=strings.Repeat("a",50000);over:=limit+"a"
 const frame=`<!ENTITY % q "%p;">`
 nested:=func(value string)string{return `<!DOCTYPE a [<!ENTITY % p "`+value+`"><!ENTITY % r '<!ENTITY &#37; q "&#37;p;">'>%r;]><a/>`}
 type example struct{
  name,input,context,entity string
  source DiagnosticSource
  offset int
  events []fault
  q,attribute,raw string
  hasQ,hasAttribute bool
 }
 cases:=[]example{
  {name:"PE overlength",input:nested("&#37;"+over+";"),context:frame,entity:"r",source:EntitySource,offset:18,events:[]fault{lengthError,peName,loop}},
  {name:"PE overlength residual",input:nested("&#37;"+over+";&#37;;"),context:frame,entity:"r",source:EntitySource,offset:18,events:[]fault{lengthError,peName,peName,loop}},
  {name:"PE exact limit unbound",input:nested("&#37;"+limit+";"),hasQ:true},
  {name:"PE exact limit bound",input:`<!DOCTYPE a [<!ENTITY % `+limit+` "ok"><!ENTITY % p "&#37;`+limit+`;"><!ENTITY % r '<!ENTITY &#37; q "&#37;p;">'>%r;]><a/>`,q:"ok",hasQ:true},
  {name:"GE attribute overlength",input:`<!DOCTYPE a [<!ENTITY e "&#38;`+over+`;">]><a x="&e;"/>`,context:strings.Repeat("a",66)+`;">]><a x="&e;`,offset:50045,events:[]fault{lengthError,geName}},
  {name:"GE attribute overlength residual",input:`<!DOCTYPE a [<!ENTITY e "&#38;`+over+`;&#38;;">]><a x="&e;"/>`,context:strings.Repeat("a",60)+`;&#38;;">]><a x="&e;`,offset:50051,events:[]fault{lengthError,geName,geName}},
  {name:"GE default overlength",input:`<!DOCTYPE a [<!ENTITY e "&#38;`+over+`;"><!ATTLIST a x CDATA "&e;">]><a/>`,context:strings.Repeat("a",53)+`;"><!ATTLIST a x CDATA "&e;`,offset:50058,events:[]fault{lengthError,geName}},
  {name:"GE default overlength residual",input:`<!DOCTYPE a [<!ENTITY e "&#38;`+over+`;&#38;;"><!ATTLIST a x CDATA "&e;">]><a/>`,context:strings.Repeat("a",47)+`;&#38;;"><!ATTLIST a x CDATA "&e;`,offset:50064,events:[]fault{lengthError,geName,geName}},
  {name:"GE attribute exact limit bound",input:`<!DOCTYPE a [<!ENTITY `+limit+` "ok"><!ENTITY e "&#38;`+limit+`;">]><a x="&e;"/>`,attribute:"ok",hasAttribute:true},
  {name:"GE default exact limit bound",input:`<!DOCTYPE a [<!ENTITY `+limit+` "ok"><!ENTITY e "&#38;`+limit+`;"><!ATTLIST a x CDATA "&e;">]><a/>`,attribute:"ok",hasAttribute:true},
  {name:"EntityValue precheck exact limit",input:`<!DOCTYPE a [<!ENTITY e "&`+limit+`;">]><a/>`,raw:"&"+limit+";"},
  {name:"EntityValue precheck overlength",input:`<!DOCTYPE a [<!ENTITY e "&`+over+`;">]><a/>`,context:strings.Repeat("a",78)+`;"`,offset:50029,events:[]fault{lengthError,{"XML_ERR_ENTITY_CHAR_ERROR","EntityValue: '&' forbidden except for entities references"}}},
  {name:"GE attribute missing name",input:`<!DOCTYPE a [<!ENTITY e "&#38;;">]><a x="&e;"/>`,offset:44,events:[]fault{geName}},
  {name:"GE attribute short residual",input:`<!DOCTYPE a [<!ENTITY e "&#38;;tail&#38;missing">]><a x="&e;"/>`,offset:60,events:[]fault{geName,semiError}},
  {name:"GE default missing name",input:`<!DOCTYPE a [<!ENTITY e "&#38;;"><!ATTLIST a x CDATA "&e;">]><a/>`,offset:57,events:[]fault{geName}},
  {name:"GE default short residual",input:`<!DOCTYPE a [<!ENTITY e "&#38;;tail&#38;missing"><!ATTLIST a x CDATA "&e;">]><a/>`,context:`<!DOCTYPE a [<!ENTITY e "&#38;;tail&#38;missing"><!ATTLIST a x CDATA "&e;">]><a/`,offset:73,events:[]fault{geName,semiError}},
 }
 for _,tc:=range cases{t.Run(tc.name,func(t *testing.T){
  for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
   r,d,err:=parse(context.Background(),tc.input,Options{Mode:mode},collect)
   if len(tc.events)==0{
    if err!=nil||!r.valid||r.Original()!=tc.input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("mode %v collect %t: %+v %v",mode,collect,r,err)}
    if collect{
     if d==nil{t.Fatal("missing transient document")}
     root:=dtdRoot(t,d)
     if len(root.children)!=0{t.Fatalf("unexpected root children %+v",root.children)}
     if tc.hasAttribute{if len(root.attributes)!=1||root.attributes[0].name!="x"||root.attributes[0].value!=tc.attribute{t.Fatalf("attribute projection %+v",root.attributes)}}else if len(root.attributes)!=0{t.Fatalf("unexpected attributes %+v",root.attributes)}
     if tc.hasQ||tc.raw!=""{
      found:=false
      for _,declaration:=range d.nodes[0].doctype.declarations{
       if tc.hasQ&&declaration.kind=="ENTITY"&&declaration.name=="q"{found=true;if !declaration.parameter||declaration.external||declaration.unparsed||declaration.value!=tc.q{t.Fatalf("q identity %+v",declaration)}}
       if tc.raw!=""&&declaration.kind=="ENTITY"&&declaration.name=="e"{found=true;if declaration.parameter||declaration.external||declaration.unparsed||declaration.value!=tc.raw{t.Fatalf("raw entity identity %+v",declaration)}}
      }
      if !found{t.Fatal("missing accepted entity declaration")}
     }
    }else if d!=nil{t.Fatal("validation retained a tree")}
   }else{
    var pe *ParseError
    if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("mode %v collect %t: %+v tree %+v err %v",mode,collect,r,d,err)}
    source:=tc.context;if source==""{source=tc.input}
    caret:=strings.Repeat(" ",min(tc.offset,79))+"^"
    var diagnostics []Diagnostic;var blocks []string
    for _,event:=range tc.events{
     diagnostics=append(diagnostics,Diagnostic{Condition:event.condition,Domain:ParserDomain,Severity:Fatal,Line:1,Column:tc.offset+1,Offset:tc.offset,SourceBaseOffset:0,Message:event.message,SourceContext:source,Caret:caret,Source:tc.source,EntityName:tc.entity})
     blocks=append(blocks,"line 1: "+event.message+"\n"+source+"\n"+caret)
    }
    primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
    if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=strings.Join(blocks,"\n")||!reflect.DeepEqual(pe.Diagnostics,diagnostics){t.Fatalf("mode %v collect %t: %+v Detail %q diagnostics %+v",mode,collect,pe,pe.Detail(),pe.Diagnostics)}
   }
   accept(t,"<healthy/>",mode)
  }}
 })}
 // Matching-source pointer/accounting witnesses: both reference wrappers
 // receive NULL with their pointer at Name begin, not the consumed end. Their
 // caller checks nil and resumes ordinary residual bytes without lookup/count.
 // A direct decoder has no enclosing PE-copy size gate: no synthetic LOOP.
 for _,what:=range []substitution{substituteGeneral,substituteParameter}{
  sigil:="&";nameError:=geName;if what==substituteParameter{sigil="%";nameError=peName}
  for _,residual:=range []bool{false,true}{for _,emit:=range []bool{false,true}{
   input:=sigil+over+";";want:=over+";";events:=[]fault{lengthError,nameError}
   if residual{input+=sigil+";";want+=";";events=append(events,nameError)}
   p:=parser{ctx:context.Background(),s:frame,i:18,line:1,column:19,source:EntitySource,entityName:"r",inDTD:true,dtdState:&dtdState{}}
   value,size,lt,ok:=p.decodeEntityString(input,what,1,emit,nil)
   emitted:=want;if !emit{emitted=""}
   if p.err!=nil||!ok||!p.failed()||p.stopped||value!=emitted||size!=uint64(len(want))||lt||p.entityCount!=0||p.dtdState.peReferences||p.i!=18||p.lastEntityError!=nameError.condition{t.Fatalf("what %v residual %t emit %t: len %d size %d lt %t ok %t count %d PE %t cursor %d err %v events %+v",what,residual,emit,len(value),size,lt,ok,p.entityCount,p.dtdState.peReferences,p.i,p.err,p.events)}
   var diagnostics []Diagnostic
   for _,event:=range events{diagnostics=append(diagnostics,Diagnostic{Condition:event.condition,Domain:ParserDomain,Severity:Fatal,Line:1,Column:19,Offset:18,SourceBaseOffset:0,Message:event.message,SourceContext:frame,Caret:"                  ^",Source:EntitySource,EntityName:"r"})}
   if !reflect.DeepEqual(p.events,diagnostics){t.Fatalf("what %v residual %t emit %t: diagnostics %+v",what,residual,emit,p.events)}
  }}
 }
}

func TestXMLDTDStringGELookup(t *testing.T) {
 // Actual pinned18.6/180006 UTF8/C captures precede this remedy. Missing
 // successfully parsed GE references continue, unlike a true accounting halt.
 for _,defaulted:=range []bool{false,true}{
  bad:=`<!DOCTYPE a [<!ENTITY e "&#38;missing;tail&#38;;">]><a x="&e;"/>`
  good:=`<!DOCTYPE a [<!ENTITY missing "ok"><!ENTITY e "&#38;missing;tail">]><a x="&e;"/>`
  source:=bad;offset:=61
  if defaulted{
   bad=`<!DOCTYPE a [<!ENTITY e "&#38;missing;tail&#38;;"><!ATTLIST a x CDATA "&e;">]><a/>`
   good=`<!DOCTYPE a [<!ENTITY missing "ok"><!ENTITY e "&#38;missing;tail"><!ATTLIST a x CDATA "&e;">]><a/>`
   source=`<!DOCTYPE a [<!ENTITY e "&#38;missing;tail&#38;;"><!ATTLIST a x CDATA "&e;">]><a`;offset=74
  }
  for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
   r,d,err:=parse(context.Background(),bad,Options{Mode:mode},collect)
   var pe *ParseError
   if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("default %t mode %v collect %t: %+v %+v %v",defaulted,mode,collect,r,d,err)}
   caret:=strings.Repeat(" ",offset)+"^"
   var diagnostics []Diagnostic;var blocks []string
   for _,event:=range []struct{condition Condition;message string}{{"XML_ERR_UNDECLARED_ENTITY","Entity 'missing' not defined"},{"XML_ERR_NAME_REQUIRED","xmlParseStringEntityRef: no name"}}{
    diagnostics=append(diagnostics,Diagnostic{Condition:event.condition,Domain:ParserDomain,Severity:Fatal,Line:1,Column:offset+1,Offset:offset,Message:event.message,SourceContext:source,Caret:caret,Source:OriginalSource})
    blocks=append(blocks,"line 1: "+event.message+"\n"+source+"\n"+caret)
   }
   primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
   if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=strings.Join(blocks,"\n")||!reflect.DeepEqual(pe.Diagnostics,diagnostics){t.Fatalf("default %t mode %v collect %t: %+v Detail %q diagnostics %+v",defaulted,mode,collect,pe,pe.Detail(),pe.Diagnostics)}
   r,d,err=parse(context.Background(),good,Options{Mode:mode},collect)
   if err!=nil||!r.valid||r.Original()!=good||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatal(r,err)}
   if collect{
    root:=dtdRoot(t,d)
    if len(root.children)!=0||len(root.attributes)!=1||root.attributes[0].name!="x"||root.attributes[0].value!="oktail"||root.attributes[0].defaulted!=defaulted{t.Fatalf("bound projection %+v",root)}
   }else if d!=nil{t.Fatal("validation retained a tree")}
   accept(t,"<healthy/>",mode)
  }}
 }
 // Source-derived lookup/caller nil checks: exactly one count increment,
 // no PE-seen mutation, residual pointer retained, and >10000 stops before
 // the later malformed reference for both fatal and suppressed-warning cases.
 for _,suppressed:=range []bool{false,true}{for _,count:=range []uint64{9999,10000}{for _,emit:=range []bool{false,true}{
  const frame=`<a x="&e;"/>`
  p:=parser{ctx:context.Background(),s:frame,i:9,line:1,column:10,inAttribute:true,dtdState:&dtdState{external:suppressed},entityCount:count}
  value,size,lt,ok:=p.decodeEntityString("&missing;tail&;",substituteGeneral,1,emit,nil)
  want:="tail;";wantSize:=uint64(5);conditions:=[]Condition{}
  if !suppressed{conditions=append(conditions,"XML_ERR_UNDECLARED_ENTITY")}
  if count==10000{want="";wantSize=0;conditions=append(conditions,"XML_ERR_ENTITY_LOOP")}else{conditions=append(conditions,"XML_ERR_NAME_REQUIRED")}
  if !emit{want=""}
  if p.err!=nil||ok!=(count==9999)||!p.failed()||p.stopped||value!=want||size!=wantSize||lt||p.entityCount!=count+1||p.dtdState.peReferences||p.i!=9{t.Fatalf("warning %t count %d emit %t: value %q size %d ok %t count %d err %v events %+v",suppressed,count,emit,value,size,ok,p.entityCount,p.err,p.events)}
  var diagnostics []Diagnostic
  for _,condition:=range conditions{
   message:="Entity 'missing' not defined";if condition=="XML_ERR_ENTITY_LOOP"{message="Detected an entity reference loop"}else if condition=="XML_ERR_NAME_REQUIRED"{message="xmlParseStringEntityRef: no name"}
   diagnostics=append(diagnostics,Diagnostic{Condition:condition,Domain:ParserDomain,Severity:Fatal,Line:1,Column:10,Offset:9,Message:message,SourceContext:frame,Caret:"         ^",Source:OriginalSource})
  }
  if !reflect.DeepEqual(p.events,diagnostics)||p.lastEntityError!=conditions[len(conditions)-1]{t.Fatal(p.events,p.lastEntityError)}
 }}}
}

func TestXMLDTDStringCopyGrowth(t *testing.T) {
 // Independent1409 diagnostic captures and corrected1410 processed-use
 // observations precede production edits. Reparsing a serialized DTD was a
 // disqualified observer; units exercise the original shared parser directly.
 a201:=strings.Repeat("A",201)
 for _,tc:=range []struct{name,written,active,wantPrefix,extra,context string;n,offset,column int;accept,general bool}{
  {name:"empty999",n:999,accept:true},
  {name:"empty1000",n:1000,accept:true},
  {name:"empty1400",n:1400,accept:true},
  {name:"empty1401",n:1401,offset:18,column:19},
  {name:"ordinary1 lower",written:"A",active:"A",wantPrefix:"A",n:1399,accept:true},
  {name:"ordinary1 upper",written:"A",active:"A",n:1400,offset:19,column:20},
  {name:"UTF8 lower",written:"é",active:"é",wantPrefix:"é",n:1398,accept:true},
  {name:"UTF8 upper",written:"é",active:"é",n:1399,offset:20,column:20},
  {name:"scalar1 lower",written:"&#38;#65;",active:"&#65;",wantPrefix:"A",n:1399,accept:true},
  {name:"scalar1 upper",written:"&#38;#65;",active:"&#65;",n:1400,offset:23,column:24},
  {name:"reference1 lower",written:"&#37;z;",active:"%z;",wantPrefix:"A",extra:`<!ENTITY % z "A">`,n:1399,accept:true},
  {name:"reference1 upper",written:"&#37;z;",active:"%z;",extra:`<!ENTITY % z "A">`,n:1400,offset:21,column:22},
  {name:"ordinary201 lower",written:a201,active:a201,wantPrefix:a201,n:2799,accept:true},
  {name:"ordinary201 upper",written:a201,active:a201,n:2800,offset:219,column:220,context:strings.Repeat("A",76)+`%p;"`},
  {name:"reference201 lower",written:"&#37;z;",active:"%z;",wantPrefix:a201,extra:`<!ENTITY % z "`+a201+`">`,n:1199,accept:true},
  {name:"reference201 upper",written:"&#37;z;",active:"%z;",extra:`<!ENTITY % z "`+a201+`">`,n:1200,offset:21,column:22},
  {name:"scalar201 lower",written:strings.Repeat("&#38;#65;",201),active:strings.Repeat("&#65;",201),wantPrefix:a201,n:1199,accept:true},
  {name:"scalar201 upper accepted",written:strings.Repeat("&#38;#65;",201),active:strings.Repeat("&#65;",201),wantPrefix:a201,n:1200,accept:true},
  {name:"predefined GE lower",wantPrefix:"&",n:1399,accept:true,general:true},
  {name:"predefined GE upper",n:1400,offset:24,column:25,general:true,context:`<!ATTLIST a x CDATA "&e;">`},
 }{t.Run(tc.name,func(t *testing.T){
  payload:=strings.Repeat("a",tc.n);want:=tc.wantPrefix+payload
  for _,use:=range []bool{false,true}{
   if use&&(!tc.accept||tc.general){continue}
   declaration:=`<!ENTITY &#37; q "`+tc.written+`&#37;p;">`
   body:=`<a/>`
   if use{declaration+=strings.Repeat(" ",1000)+`<!ENTITY e "&#37;q;">`;body=`<a>&e;</a>`}
   input:=`<!DOCTYPE a [<!ENTITY % p "`+payload+`">`+tc.extra+`<!ENTITY % r '`+declaration+`'>%r;]>`+body
   if tc.general{input=`<!DOCTYPE a [<!ENTITY p "`+payload+`"><!ENTITY e "&#38;amp;&#38;p;"><!ENTITY % r '<!ATTLIST a x CDATA "&#38;e;">'>%r;]><a/>`}
   for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
    r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
    if tc.accept{
     if err!=nil||!r.valid||r.Original()!=input||r.RequestedMode()!=mode||r.EffectiveMode()!=Document||len(r.Diagnostics())!=0{t.Fatalf("use %t mode %v collect %t: %+v %v",use,mode,collect,r,err)}
     if collect{
      root:=dtdRoot(t,d)
      if tc.general{
       if len(root.children)!=0||len(root.attributes)!=1||root.attributes[0].name!="x"||root.attributes[0].value!=want||!root.attributes[0].defaulted{t.Fatalf("general projection %+v",root)}
      }else{
       if len(root.attributes)!=0{t.Fatal(root.attributes)}
       if use{if len(root.children)!=1||root.children[0].kind!=textNode||root.children[0].value!=want{t.Fatalf("processed-use projection %+v",root.children)}}else if len(root.children)!=0{t.Fatal(root.children)}
       // Original unused-q private content is source-grounded; the distinct
       // padded q-use witness has independent exact scalar observations.
       found:=false
       for _,decl:=range d.nodes[0].doctype.declarations{if decl.kind=="ENTITY"&&decl.name=="q"{found=true;if !decl.parameter||decl.external||decl.unparsed||decl.value!=want{t.Fatalf("q projection %+v",decl)}}}
       if !found{t.Fatal("missing q")}
      }
     }else if d!=nil{t.Fatal("validation retained a tree")}
    }else{
     var pe *ParseError
     if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("mode %v collect %t: %+v %+v %v",mode,collect,r,d,err)}
     source:=tc.context;if source==""{source=`<!ENTITY % q "`+tc.active+`%p;">`}
     caret:=strings.Repeat(" ",min(tc.offset,79))+"^"
     diagnostic:=Diagnostic{Condition:"XML_ERR_ENTITY_LOOP",Domain:ParserDomain,Severity:Fatal,Line:1,Column:tc.column,Offset:tc.offset,Message:"Detected an entity reference loop",SourceContext:source,Caret:caret,Source:EntitySource,EntityName:"r"}
     primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
     if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!="line 1: Detected an entity reference loop\n"+source+"\n"+caret||!reflect.DeepEqual(pe.Diagnostics,[]Diagnostic{diagnostic}){t.Fatalf("mode %v collect %t: %+v Detail %q diagnostics %+v",mode,collect,pe,pe.Detail(),pe.Diagnostics)}
    }
    accept(t,"<healthy/>",mode)
   }}
  }
 })}
 // Matching-source capacity histories require no expansion buffers. Ordinary
 // contributions grow without a density check; recursive copies alone check
 // their crossed growth positions. The size predicate itself is unchanged.
 for _,tc:=range []struct{capacity,size,want uint64;recursive,ok bool}{
  {300,0,300,false,true},{700,200,700,true,true},
  {300,200,300,false,true},{300,201,700,false,true},
  {700,600,700,false,true},{700,601,1500,false,true},
  {1500,1401,3100,false,true},{300,1400,1500,true,true},
  {300,1401,1500,true,false},{700,1400,1500,true,true},
  {3100,3000,3100,true,true},{3100,3001,3100,true,false},
  {3100,3001,6300,false,true},
 }{
  p:=parser{ctx:context.Background(),s:`<!ENTITY % q "%p;">`,i:18,line:1,column:19,source:EntitySource,entityName:"r"}
  capacity,ok:=p.growEntityString(tc.capacity,0,tc.size,tc.recursive)
  if p.err!=nil||ok!=tc.ok||capacity!=tc.want||p.failed()!=!tc.ok{t.Fatalf("%+v: capacity %d ok %t err %v events %+v",tc,capacity,ok,p.err,p.events)}
  if tc.ok&&len(p.events)!=0||!tc.ok&&(len(p.events)!=1||p.events[0].Condition!="XML_ERR_ENTITY_LOOP"){t.Fatal(p.events)}
 }
 p:=parser{ctx:context.Background(),i:18}
 if p.entityCheck(1000,nil,0)||len(p.events)!=1||p.events[0].Condition!="XML_ERR_ENTITY_LOOP"{t.Fatal("changed size predicate",p.events)}
 p=parser{ctx:context.Background()}
 capacity:=uint64((math.MaxUint64-100)/2+1)
 grown,ok:=p.growEntityString(capacity,0,capacity-99,false)
 if ok||grown!=capacity||p.err==nil||len(p.events)!=0{t.Fatal("unchecked semantic capacity overflow",grown,ok,p.err,p.events)}
 if _,ok:=p.err.(interface{SQLState()string});ok{t.Fatal("size error acquired SQLSTATE")}
 // Retained-spelling reserve equality can leave999bytes in capacity1098.
 // Empty content must not trigger growth; the next actual copied byte checks
 //1000, not the already-passed999 threshold (independent1421 contrast).
 p=parser{ctx:context.Background(),i:24}
 grown,ok=p.growEntityString(1098,999,999,true)
 if !ok||grown!=1098||p.err!=nil||len(p.events)!=0{t.Fatal("empty recursive contribution changed reserve equality",grown,ok,p.err,p.events)}
 grown,ok=p.growEntityString(1098,999,1000,true)
 if ok||grown!=1098||p.err!=nil||len(p.events)!=1||p.events[0].Condition!="XML_ERR_ENTITY_LOOP"{t.Fatal("copy checked behind its starting endpoint",grown,ok,p.err,p.events)}
}

func TestXMLDTDStringExternalContent(t *testing.T) {
 // Independent pinned18.6/180006 UTF8/C captures1420/1421/1428 precede
 // production edits. Rendered sequences/windows are observed; symbolic source
 // coordinates and private content/count assertions are matching-source facts.
 type example struct{name,input string;events []Diagnostic}
 var cases []example
 diagnostic:=func(code Condition,message,source,entity string,kind DiagnosticSource,offset,column int)Diagnostic{
  return Diagnostic{Condition:code,Domain:ParserDomain,Severity:Fatal,Line:1,Column:column,Offset:offset,Message:message,SourceContext:source,Caret:strings.Repeat(" ",min(offset,79))+"^",Source:kind,EntityName:entity}
 }
 for _,kind:=range []struct{name,declaration,message string;code Condition;offsets [4]int}{
  {"parsed",`<!ENTITY ext SYSTEM "x">`,"Attribute references external entity 'ext'","XML_ERR_ENTITY_IS_EXTERNAL",[4]int{71,81,84,94}},
  {"unparsed named",`<!ENTITY ext SYSTEM "x" NDATA n>`,"Entity reference to unparsed entity ext","XML_ERR_UNPARSED_ENTITY",[4]int{79,89,92,102}},
  {"unparsed absent",`<!ENTITY ext SYSTEM "x" NDATA >`,"Entity reference to unparsed entity ext","XML_ERR_UNPARSED_ENTITY",[4]int{78,88,91,101}},
 }{for route:=range 2{for residual:=range 2{
  suffix:="";if residual!=0{suffix="tail&#38;;"}
  defaultDecl:="";body:=`<a x="&e;"/>`;label:="attribute"
  if route!=0{defaultDecl=`<!ATTLIST a x CDATA "&e;">`;body=`<a/>`;label="default"}
  input:=`<!DOCTYPE a [`+kind.declaration+`<!ENTITY e "&#38;ext;`+suffix+`">`+defaultDecl+`]>`+body
  offset:=kind.offsets[route*2+residual]
  // Literal oracle offsets plus its independently observed ASCII80byte window;
  // no candidate context-rendering helper supplies expected diagnostics.
  start:=max(0,offset-80);source:=input[start:min(len(input),start+80)]
  events:=[]Diagnostic{diagnostic(kind.code,kind.message,source,"",OriginalSource,offset,offset+1)}
  if residual!=0{events=append(events,diagnostic("XML_ERR_NAME_REQUIRED","xmlParseStringEntityRef: no name",source,"",OriginalSource,offset,offset+1));label+=" residual"}
  cases=append(cases,example{kind.name+" "+label,input,events})
 }}}
 const rFrame=`<!ATTLIST a x CDATA "&e;">`
 const gFrame=`<a x="&e;"/>`
 name:=strings.Repeat("X",201)
 externalMessage:="Attribute references external entity '"+name+"'"
 unparsedMessage:="Entity reference to unparsed entity "+name
 loop:=func()Diagnostic{return diagnostic("XML_ERR_ENTITY_LOOP","Detected an entity reference loop",rFrame,"r",EntitySource,24,25)}
 originalR:=strings.Repeat("X",21)+`;&#38;p;"><!ENTITY % r '<!ATTLIST a x CDATA "&#38;e;">'>%r;`
 for _,kind:=range []struct{name,notation string;n,offset int;unparsed bool}{
  {name:"parsed reserve",n:1599},
  {name:"absent notation reserve",notation:" NDATA ",n:1599,offset:2132,unparsed:true},
  {name:"named notation content",notation:" NDATA n",n:1399,offset:1933,unparsed:true},
 }{for upper:=range 2{
  n:=kind.n+upper
  input:=`<!DOCTYPE a [<!ENTITY `+name+` SYSTEM "x"`+kind.notation+`><!ENTITY p "`+strings.Repeat("a",n)+`"><!ENTITY e "&#38;`+name+`;&#38;p;"><!ENTITY % r '<!ATTLIST a x CDATA "&#38;e;">'>%r;]><a/>`
  message:=externalMessage;code:=Condition("XML_ERR_ENTITY_IS_EXTERNAL");var events []Diagnostic
  if kind.unparsed{
   message=unparsedMessage;code="XML_ERR_UNPARSED_ENTITY";offset:=kind.offset+upper
   original:=diagnostic(code,message,originalR,"",OriginalSource,offset,offset+1)
   events=append(events,original,original)
  }
  events=append(events,diagnostic(code,message,rFrame,"r",EntitySource,24,25))
  label:=kind.name+" lower";if upper!=0{events=append(events,loop());label=kind.name+" upper"}
  cases=append(cases,example{label,input,events})
 }}
 for upper:=range 2{
  n:=1599+upper
  input:=`<!DOCTYPE root [<!ENTITY `+name+` SYSTEM "x"><!ENTITY p "`+strings.Repeat("a",n)+`"><!ENTITY e "&#38;`+name+`;&#38;p;"><!ENTITY g '<a x="&e;"/>'>]><root>&g;</root>`
  events:=[]Diagnostic{diagnostic("XML_ERR_ENTITY_IS_EXTERNAL",externalMessage,gFrame,"g",EntitySource,9,10)}
  original:=strings.Repeat("X",33)+`;&#38;p;"><!ENTITY g '<a x="&e;"/>'>]><root>&g;`
  offset:=2116+upper;label:="explicit GE lower"
  if upper==0{events=append(events,diagnostic("XML_ERR_UNDECLARED_ENTITY","Entity 'g' failed to parse",original,"",OriginalSource,offset,offset+1))}else{
   events=append(events,diagnostic("XML_ERR_ENTITY_LOOP","Detected an entity reference loop",gFrame,"g",EntitySource,9,10),diagnostic("XML_ERR_ENTITY_LOOP","Detected an entity reference loop",original,"",OriginalSource,offset,offset+1));label="explicit GE upper"
  }
  cases=append(cases,example{label,input,events})
 }
 x:=strings.Repeat("X",398);y:=strings.Repeat("Y",597)
 for n:=range 2{
  input:=`<!DOCTYPE a [<!ENTITY `+x+` SYSTEM "x"><!ENTITY `+y+` SYSTEM "y"><!ENTITY p "`+strings.Repeat("a",n)+`"><!ENTITY e "&#38;`+x+`;&#38;`+y+`;&#38;p;"><!ENTITY % r '<!ATTLIST a x CDATA "&#38;e;">'>%r;]><a/>`
  events:=[]Diagnostic{diagnostic("XML_ERR_ENTITY_IS_EXTERNAL","Attribute references external entity '"+x+"'",rFrame,"r",EntitySource,24,25),diagnostic("XML_ERR_ENTITY_IS_EXTERNAL","Attribute references external entity '"+y+"'",rFrame,"r",EntitySource,24,25)}
  label:="reserve equality empty";if n!=0{events=append(events,loop());label="reserve equality next byte"}
  cases=append(cases,example{label,input,events})
 }
 for _,tc:=range cases{t.Run(tc.name,func(t *testing.T){
  var blocks []string;for _,event:=range tc.events{blocks=append(blocks,"line 1: "+event.Message+"\n"+event.SourceContext+"\n"+event.Caret)}
  for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
   r,d,err:=parse(context.Background(),tc.input,Options{Mode:mode},collect)
   var pe *ParseError
   if !errors.As(err,&pe)||!reflect.DeepEqual(r,Result{})||d!=nil{t.Fatalf("mode %v collect %t: %+v %+v %v",mode,collect,r,d,err)}
   primary:="invalid XML document";if mode==Content{primary="invalid XML content"}
   if pe.Category!=GrammarError||pe.RequestedMode!=mode||pe.EffectiveMode!=Document||pe.Error()!=primary||pe.Detail()!=strings.Join(blocks,"\n")||!reflect.DeepEqual(pe.Diagnostics,tc.events){t.Fatalf("mode %v collect %t: %+v Detail %q diagnostics %+v",mode,collect,pe,pe.Detail(),pe.Diagnostics)}
   accept(t,"<healthy/>",mode)
  }}
 })}
 // Matching-source private content/type/count distinctions: named UNPARSED
 // recursively contributes notation text and checked/2; NULL content retains
 // spelling. Internal empty content is nonNULL and must not retain spelling.
 for _,tc:=range []struct{e entity;want string;count,checked uint64;condition Condition}{
  {e:entity{name:"ext",external:true},want:"&ext;",count:1,condition:"XML_ERR_ENTITY_IS_EXTERNAL"},
  {e:entity{name:"ext",external:true,unparsed:true},want:"&ext;",count:1,condition:"XML_ERR_UNPARSED_ENTITY"},
  {e:entity{name:"ext",external:true,unparsed:true,notation:"n"},want:"n",count:2,checked:2,condition:"XML_ERR_UNPARSED_ENTITY"},
  {e:entity{name:"ext"},count:2,checked:2},
 }{for _,emit:=range []bool{false,true}{
  e:=tc.e
  p:=parser{ctx:context.Background(),s:rFrame,i:24,line:1,column:25,source:EntitySource,entityName:"r",inAttribute:true,dtdState:&dtdState{general:map[string]*entity{"ext":&e}}}
  value,size,lt,ok:=p.decodeEntityString("&ext;",substituteGeneral,1,emit,nil)
  want:=tc.want;if !emit{want=""}
  if !ok||p.err!=nil||value!=want||size!=uint64(len(tc.want))||lt||p.entityCount!=tc.count||e.checked!=tc.checked||e.value!=""||e.notation!=tc.e.notation||e.external!=tc.e.external||e.unparsed!=tc.e.unparsed||p.dtdState.peReferences||p.failed()!=(tc.condition!=""){t.Fatalf("entity %+v emit %t: value %q size %d ok %t count %d checked %d err %v events %+v",tc.e,emit,value,size,ok,p.entityCount,e.checked,p.err,p.events)}
  var events []Diagnostic
  if tc.condition!=""{message:="Attribute references external entity 'ext'";if tc.condition=="XML_ERR_UNPARSED_ENTITY"{message="Entity reference to unparsed entity ext"};events=append(events,diagnostic(tc.condition,message,rFrame,"r",EntitySource,24,25))}
  if !reflect.DeepEqual(p.events,events){t.Fatal(p.events,events)}
 }}
 // Successful ordinary external content is still empty, not retained spelling.
 for _,mode:=range []Mode{Document,Content}{for _,collect:=range []bool{false,true}{
  input:=`<!DOCTYPE a [<!ENTITY ext SYSTEM "x">]><a>x&ext;y</a>`
  r,d,err:=parse(context.Background(),input,Options{Mode:mode},collect)
  if err!=nil||r.Original()!=input||len(r.Diagnostics())!=0||r.EffectiveMode()!=Document{t.Fatal(r,err)}
  if collect{root:=dtdRoot(t,d);if len(root.children)!=1||root.children[0].kind!=textNode||root.children[0].value!="xy"{t.Fatal(root.children)}}else if d!=nil{t.Fatal("validation retained tree")}
 }}
 // Checked special-reserve arithmetic and equality, without giant buffers.
 for _,tc:=range []struct{capacity,size,name,wantCapacity,wantSize uint64}{
  {300,0,201,901,203},{300,0,398,1098,400},
  {1098,400,597,1098,999},{1098,400,598,2894,1000},
 }{
  p:=parser{ctx:context.Background()}
  capacity,size,ok:=p.reserveEntityString(tc.capacity,tc.size,tc.name)
  if !ok||p.err!=nil||capacity!=tc.wantCapacity||size!=tc.wantSize||len(p.events)!=0{t.Fatalf("%+v: capacity %d size %d ok %t err %v",tc,capacity,size,ok,p.err)}
 }
 for _,tc:=range []struct{capacity,size,name uint64}{
  {math.MaxUint64,math.MaxUint64-100,1},
  {(math.MaxUint64-301)/2+1,(math.MaxUint64-301)/2+1,201},
 }{
  p:=parser{ctx:context.Background()}
  capacity,size,ok:=p.reserveEntityString(tc.capacity,tc.size,tc.name)
  if ok||capacity!=tc.capacity||size!=tc.size||p.err==nil||len(p.events)!=0{t.Fatal("unchecked reserve arithmetic",tc,capacity,size,ok,p.err,p.events)}
  if _,ok:=p.err.(interface{SQLState()string});ok{t.Fatal("reserve error acquired SQLSTATE")}
 }
 p:=parser{ctx:context.Background()}
 _,_,ok:=p.reserveEntityString(300,math.MaxUint64-1,1)
 if ok||p.err!=nil||len(p.events)!=1||p.events[0].Condition!="XML_ERR_ENTITY_LOOP"{t.Fatal("unchecked retained-spelling size",ok,p.err,p.events)}
}
