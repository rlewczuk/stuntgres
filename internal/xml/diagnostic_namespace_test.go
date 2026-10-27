package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func TestXMLCoreNamespaceAdmission(t *testing.T) {
 // Independent PostgreSQL18.6 UTF8/C captures615 (DOCUMENT3–43,
 // CONTENT44–84) and supplemental620 precede these literals.
 // Acceptance and complete Detail are
 // observed; structured conditions/columns/offsets follow matching Debian
 // libxml2 parser.c3920–4140,9290–9440, not SQL-exposed libxml metadata.
 type witness struct {
  name,input,duplicate string
  offset int
 }
 cases:=[]witness{
  {"xml_wrong_twice",`<a xmlns:xml="u" xmlns:xml="v"/>`, "",0},
  {"prefixed_empty_before",`<a xmlns:p="" xmlns:p="u"/>`, "",0},
  {"prefixed_empty_after",`<a xmlns:p="u" xmlns:p=""/>`, "",0},
  {"prefixed_empty_twice",`<a xmlns:p='' xmlns:p=''/>`, "",0},
  {"xml_right_wrong",`<a xmlns:xml="http://www.w3.org/XML/1998/namespace" xmlns:xml="v"/>`, "",0},
  {"xml_empty_wrong",`<a xmlns:xml="" xmlns:xml="v"/>`, "",0},
  {"xml_ref_right",`<a xmlns:xml="http://www.w3.org/XML/1998/namespac&#101;" xmlns:xml="v"/>`, "",0},
  {"xmlns_prefix_twice",`<a xmlns:xmlns="u" xmlns:xmlns="v"/>`, "",0},
  {"prefix_xml_before",`<a xmlns:p="http://www.w3.org/XML/1998/namespace" xmlns:p="u"/>`, "",0},
  {"prefix_xml_after",`<a xmlns:p="u" xmlns:p="http://www.w3.org/XML/1998/namespace"/>`, "",0},
  {"prefix_xml_ref",`<a xmlns:p="http://www.w3.org/XML/1998/namespac&#x65;" xmlns:p="u"/>`, "",0},
  {"prefix_xmlns_before",`<a xmlns:p="http://www.w3.org/2000/xmlns/" xmlns:p="u"/>`, "",0},
  {"prefix_xmlns_after",`<a xmlns:p="u" xmlns:p="http://www.w3.org/2000/xmlns/"/>`, "",0},
  {"prefix_xmlns_ref",`<a xmlns:p="http://www.w3.org/2000/xmlns&#47;" xmlns:p="u"/>`, "",0},
  {"default_xml_before",`<a xmlns="http://www.w3.org/XML/1998/namespace" xmlns="u"/>`, "",0},
  {"default_xml_after",`<a xmlns="u" xmlns="http://www.w3.org/XML/1998/namespace"/>`, "",0},
  {"default_xml_ref",`<a xmlns="http://www.w3.org/XML/1998/namespac&#101;" xmlns="u"/>`, "",0},
  {"default_xmlns_before",`<a xmlns="http://www.w3.org/2000/xmlns/" xmlns="u"/>`, "",0},
  {"default_xmlns_after",`<a xmlns="u" xmlns="http://www.w3.org/2000/xmlns/"/>`, "",0},
  {"default_xmlns_ref",`<a xmlns="http://www.w3.org/2000/xmlns&#x2f;" xmlns="u"/>`, "",0},
  {"prefix_normal_duplicate",`<a xmlns:p="u" xmlns:p="v" y="3"/>`,"xmlns:p",26},
  {"default_normal_duplicate",`<a xmlns="u" xmlns="v" y="3"/>`,"xmlns",22},
  {"default_empty_duplicate",`<a xmlns="" xmlns="u"/>`,"xmlns",21},
  {"default_empty_twice",`<a xmlns='' xmlns=''/>`,"xmlns",20},
  {"prefix_space_duplicate",`<a xmlns:p=" " xmlns:p="u"/>`,"xmlns:p",26},
  {"prefix_literal_tab_duplicate","<a xmlns:p=\"\t\" xmlns:p=\"u\"/>","xmlns:p",26},
  {"prefix_literal_lf_duplicate","<a xmlns:p=\"\n\" xmlns:p=\"u\"/>","xmlns:p",26},
  {"prefix_literal_crlf_duplicate","<a xmlns:p=\"\r\n\" xmlns:p=\"u\"/>","xmlns:p",27},
  {"prefix_ref_tab_duplicate",`<a xmlns:p="&#9;" xmlns:p="u"/>`,"xmlns:p",29},
  {"prefix_ref_lf_duplicate",`<a xmlns:p="&#10;" xmlns:p="u"/>`,"xmlns:p",30},
  {"prefix_ref_cr_duplicate",`<a xmlns:p="&#13;" xmlns:p="u"/>`,"xmlns:p",30},
  {"prefix_xml_space_duplicate",`<a xmlns:p="http://www.w3.org/XML/1998/namespace " xmlns:p="u"/>`,"xmlns:p",62},
  {"prefix_xml_ref_space_duplicate",`<a xmlns:p="http://www.w3.org/XML/1998/namespace&#32;" xmlns:p="u"/>`,"xmlns:p",66},
  {"prefix_predefined_amp_duplicate",`<a xmlns:p="&amp;" xmlns:p="u"/>`,"xmlns:p",30},
  {"prefix_numeric_amp_duplicate",`<a xmlns:p="&#38;" xmlns:p="u"/>`,"xmlns:p",30},
  {"prefix_invalid_lt_duplicate",`<a xmlns:p="&lt;" xmlns:p="u"/>`,"xmlns:p",29},
  {"ordinary_duplicate",`<a x="1" x="2" y="3"/>`,"x",20},
  {"ordinary_prefixed_duplicate",`<a p:x="1" p:x="2" y="3"/>`,"p:x",24},
  {"ordinary_distinct_prefix",`<a p:x="1" q:x="2"/>`,"",0},
  {"default_xml_normal_second_duplicate",`<a xmlns="http://www.w3.org/XML/1998/namespace" xmlns="u" xmlns="v"/>`,"xmlns",67},
  {"prefix_empty_normal_second_duplicate",`<a xmlns:p="" xmlns:p="u" xmlns:p="v"/>`,"xmlns:p",37},
  // NonNULL constructed-empty values do not occupy prefixed namespace slots,
  // but their original reference error still rejects the whole parse (620).
  {"prefix_ref_zero_before",`<a xmlns:p="&#0;" xmlns:p="u"/>`,"",16},
  {"prefix_ref_zero_after",`<a xmlns:p="u" xmlns:p="&#0;"/>`,"",28},
  {"prefix_ref_undefined_before",`<a xmlns:p="&bad;" xmlns:p="u"/>`,"",17},
 }
 for _,mode:=range []Mode{Content,Document} {
  for _,tc:=range cases {
   t.Run(tc.name+map[Mode]string{Content:"/content",Document:"/document"}[mode],func(t *testing.T){
    for _,kind:=range []InputKind{Text,Binary} {
     r,err:=Parse(context.Background(),tc.input,Options{Mode:mode,InputKind:kind})
     if tc.offset==0 {
      if err!=nil||!r.valid||r.Original()!=tc.input||r.RequestedMode()!=mode||r.EffectiveMode()!=mode||r.Declaration()!=(Declaration{})||len(r.Diagnostics())!=0 {t.Fatalf("kind%d accepted result%+v error%v",kind,r,err)}
      text,w,err:=OutputText(context.Background(),r)
      if err!=nil||text!=tc.input||w!=(OutputWarning{}) {t.Fatalf("kind%d text%q error%v",kind,text,err)}
      binary,w,err:=OutputBinary(context.Background(),r,6)
      if err!=nil||binary!=tc.input||w!=(OutputWarning{}) {t.Fatalf("kind%d binary%q error%v",kind,binary,err)}
     }else{
      var e *ParseError
      if !errors.As(err,&e)||r.valid||r.original!=""||r.requested!=0||r.effective!=0||r.declaration!=(Declaration{})||len(r.diagnostics)!=0 {t.Fatalf("kind%d result%+v error%v",kind,r,err)}
      source,caret,line,column:=tc.input,strings.Repeat(" ",tc.offset)+"^",1,tc.offset+1
      switch tc.name {
      case "prefix_literal_tab_duplicate":caret=strings.Repeat(" ",12)+"\t"+strings.Repeat(" ",13)+"^"
      case "prefix_literal_lf_duplicate","prefix_literal_crlf_duplicate":source=`" xmlns:p="u"/>`;caret=strings.Repeat(" ",13)+"^";line=2;column=14
      }
      primary:="invalid XML content";if mode==Document {primary="invalid XML document"}
      condition:=Condition("XML_ERR_ATTRIBUTE_REDEFINED")
      message:="Attribute "+tc.duplicate+" redefined"
      switch tc.name {
      case "prefix_ref_zero_before","prefix_ref_zero_after":condition="XML_ERR_INVALID_CHAR";message="xmlParseCharRef: invalid xmlChar value 0"
      case "prefix_ref_undefined_before":condition="XML_ERR_UNDECLARED_ENTITY";message="Entity 'bad' not defined"
      }
      want:=Diagnostic{Condition:condition,Domain:ParserDomain,Severity:Fatal,Line:line,Column:column,Offset:tc.offset,Message:message,SourceContext:source,Caret:caret}
      // Build independent Detail, not the candidate's renderer.
      label:="line 1: ";if line==2 {label="line 2: "}
      detail:=label+message+"\n"+source+"\n"+caret
      if e.Error()!=primary||e.RequestedMode!=mode||e.EffectiveMode!=mode||e.Category!=GrammarError||e.SourceBaseOffset!=0||e.Declaration!=(Declaration{})||e.Detail()!=detail||len(e.Diagnostics)!=1 {t.Fatalf("kind%d error%+v detail%q want%q",kind,e,e.Detail(),detail)}
      if e.Diagnostics[0]!=want {t.Fatalf("kind%d diagnostic%+v want%+v",kind,e.Diagnostics[0],want)}
      if out,w,err:=OutputText(context.Background(),r);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed text%q error%v",kind,out,err)}
      if out,w,err:=OutputBinary(context.Background(),r,6);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed binary%q error%v",kind,out,err)}
     }
     healthy:="<healthy>é</healthy>"
     next,err:=Parse(context.Background(),healthy,Options{Mode:mode,InputKind:kind})
     if err!=nil||!next.valid||next.Original()!=healthy||len(next.Diagnostics())!=0 {t.Fatalf("kind%d healthy reuse%+v error%v",kind,next,err)}
    }
   })
  }
 }
}

func TestXMLCoreNamespaceCollisions(t *testing.T) {
 // Independent pinned PostgreSQL18.6 UTF8/C artifacts655:3–44,45–86
 // and supplemental662:1–16 ground all fifty cases.
 // Text acceptance, complete Detail and typed text output were observed in
 // both modes before these literals. Binary UTF8/output and structured
 // condition/offset/column fields are source-grounded provider expectations.
 cases:=[]struct{
  name,input,duplicates string
  offset int
 }{
  {"same_tag",`<a xmlns:p="u" xmlns:q="u" p:x="1" q:x="2" q:x="3"/>`,"",0},
  {"reverse_order",`<a xmlns:p="u" xmlns:q="u" q:x="1" q:x="2" p:x="3"/>`,"q:x",50},
  {"first_prefix_duplicate",`<a xmlns:p="u" xmlns:q="u" p:x="1" p:x="2" q:x="3"/>`,"p:x",50},
  {"inherited",`<r xmlns:p="u" xmlns:q="u"><a p:x="1" q:x="2" q:x="3"/></r>`,"",0},
  {"inherited_reverse",`<r xmlns:p="u" xmlns:q="u"><a q:x="1" q:x="2" p:x="3"/></r>`,"q:x",53},
  {"late_declarations",`<a p:x="1" q:x="2" q:x="3" xmlns:p="u" xmlns:q="u"/>`,"",0},
  {"different_uri",`<a xmlns:p="u" xmlns:q="v" p:x="1" q:x="2" q:x="3"/>`,"q:x",50},
  {"different_local",`<a xmlns:p="u" xmlns:q="u" p:y="1" q:x="2" q:x="3"/>`,"q:x",50},
  {"third_prefix",`<a xmlns:p="u" xmlns:q="u" xmlns:t="u" p:x="1" q:x="2" t:x="3" t:x="4"/>`,"",0},
  {"shadow_different",`<r xmlns:p="u" xmlns:q="u"><a xmlns:q="v" p:x="1" q:x="2" q:x="3"/></r>`,"q:x",65},
  {"shadow_same",`<r xmlns:p="u" xmlns:q="v"><a xmlns:q="u" p:x="1" q:x="2" q:x="3"/></r>`,"",0},
  {"late_shadow_same",`<r xmlns:p="u" xmlns:q="v"><a p:x="1" q:x="2" q:x="3" xmlns:q="u"/></r>`,"",0},
  {"empty_prefix_keeps_parent",`<r xmlns:p="u" xmlns:q="u"><a xmlns:q="" p:x="1" q:x="2" q:x="3"/></r>`,"",0},
  {"reserved_prefix_keeps_parent",`<r xmlns:p="u" xmlns:q="u"><a xmlns:q="http://www.w3.org/XML/1998/namespace" p:x="1" q:x="2" q:x="3"/></r>`,"",0},
  {"endtag_unwind_restore",`<r xmlns:p="u" xmlns:q="u"><b xmlns:q="v"></b><a p:x="1" q:x="2" q:x="3"/></r>`,"",0},
  {"emptytag_unwind_restore",`<r xmlns:p="u" xmlns:q="u"><b xmlns:q="v"/><a p:x="1" q:x="2" q:x="3"/></r>`,"",0},
  {"endtag_unwind_remove",`<r xmlns:p="u"><b xmlns:q="u"></b><a p:x="1" q:x="2" q:x="3"/></r>`,"q:x",60},
  {"emptytag_unwind_remove",`<r xmlns:p="u"><b xmlns:q="u"/><a p:x="1" q:x="2" q:x="3"/></r>`,"q:x",57},
  {"default_not_attributes",`<a xmlns="u" xmlns:p="u" x="1" p:x="2" p:x="3"/>`,"p:x",46},
  {"default_not_attributes_reverse",`<a xmlns="u" xmlns:p="u" p:x="1" x="2" x="3"/>`,"x",44},
  {"default_empty_control",`<r xmlns="u" xmlns:p="u"><a xmlns="" p:x="1" x="2" x="3"/></r>`,"x",56},
  {"unbound_both",`<a p:x="1" q:x="2" q:x="3"/>`,"q:x",26},
  {"unbound_one",`<a xmlns:p="u" p:x="1" q:x="2" q:x="3"/>`,"q:x",38},
  {"unprefixed_duplicate",`<a x="1" x="2"/>`,"x",14},
  {"prefixed_duplicate",`<a xmlns:p="u" p:x="1" p:x="2"/>`,"p:x",30},
  {"xml_builtin_duplicate",`<a xml:x="1" xml:x="2"/>`,"xml:x",22},
  {"xml_reserved_alias_skipped",`<a xmlns:p="http://www.w3.org/XML/1998/namespace" p:x="1" xml:x="2" xml:x="3"/>`,"xml:x",77},
  {"numeric_uri",`<a xmlns:p="u" xmlns:q="&#117;" p:x="1" q:x="2" q:x="3"/>`,"",0},
  {"predefined_numeric_amp",`<a xmlns:p="u&amp;v" xmlns:q="u&#38;v" p:x="1" q:x="2" q:x="3"/>`,"",0},
  {"predefined_numeric_lt",`<a xmlns:p="u&lt;v" xmlns:q="u&#60;v" p:x="1" q:x="2" q:x="3"/>`,"",0},
  {"literal_tab_space","<a xmlns:p=\"u v\" xmlns:q=\"u\tv\" p:x=\"1\" q:x=\"2\" q:x=\"3\"/>","",0},
  {"literal_lf_space","<a xmlns:p=\"u v\" xmlns:q=\"u\nv\" p:x=\"1\" q:x=\"2\" q:x=\"3\"/>","",0},
  {"literal_crlf_space","<a xmlns:p=\"u v\" xmlns:q=\"u\r\nv\" p:x=\"1\" q:x=\"2\" q:x=\"3\"/>","",0},
  {"numeric_tab_not_space",`<a xmlns:p="u v" xmlns:q="u&#9;v" p:x="1" q:x="2" q:x="3"/>`,"q:x",57},
  {"numeric_tab_equal",`<a xmlns:p="u&#9;v" xmlns:q="u&#x9;v" p:x="1" q:x="2" q:x="3"/>`,"",0},
  {"no_trim",`<a xmlns:p="u" xmlns:q="u " p:x="1" q:x="2" q:x="3"/>`,"q:x",51},
  {"no_collapse",`<a xmlns:p="u v" xmlns:q="u  v" p:x="1" q:x="2" q:x="3"/>`,"q:x",55},
  {"no_uri_percent_decode",`<a xmlns:p="u" xmlns:q="%75" p:x="1" q:x="2" q:x="3"/>`,"q:x",52},
  {"no_uri_casefold",`<a xmlns:p="http://EXAMPLE/x" xmlns:q="http://example/x" p:x="1" q:x="2" q:x="3"/>`,"q:x",80},
  {"ordered_multiple",`<a xmlns:p="u" xmlns:q="u" q:x="1" q:x="2" p:x="3" z="4" z="5"/>`,"q:x,z",62},
  {"normalized_reverse",`<a xmlns:p="u" xmlns:q="&#117;" q:x="1" q:x="2" p:x="3"/>`,"q:x",55},
  {"declaration_duplicate_first_binding",`<a xmlns:p="u" xmlns:p="v" xmlns:q="u" p:x="1" q:x="2" q:x="3"/>`,"xmlns:p",26},
  {"xml_wrong_mapping",`<a xmlns:xml="u" xmlns:p="u" p:x="1" xml:x="2" xml:x="3"/>`,"xml:x",56},
  {"xmlns_not_builtin",`<a xmlns:xmlns="u" xmlns:p="u" p:x="1" xmlns:x="2" xmlns:x="3"/>`,"xmlns:x",62},
  {"unicode_uri",`<a xmlns:p="é" xmlns:q="&#233;" p:x="1" q:x="2" q:x="3"/>`,"",0},
  {"numeric_lf_not_space",`<a xmlns:p="u v" xmlns:q="u&#10;v" p:x="1" q:x="2" q:x="3"/>`,"q:x",58},
  {"numeric_cr_not_space",`<a xmlns:p="u v" xmlns:q="u&#13;v" p:x="1" q:x="2" q:x="3"/>`,"q:x",58},
  {"amp_escape_not_recursive",`<a xmlns:p="u&amp;v" xmlns:q="u&amp;#38;v" p:x="1" q:x="2" q:x="3"/>`,"q:x",66},
  {"endtag_unwind_both",`<r><b xmlns:p="u" xmlns:q="u"></b><a p:x="1" q:x="2" q:x="3"/></r>`,"q:x",60},
  {"duplicate_binding_other",`<a xmlns:p="v" xmlns:p="u" xmlns:q="u" p:x="1" q:x="2" q:x="3"/>`,"xmlns:p,q:x",26},
 }
 for _,mode:=range []Mode{Content,Document} {
  for _,tc:=range cases {
   t.Run(tc.name+map[Mode]string{Content:"/content",Document:"/document"}[mode],func(t *testing.T){
    for _,kind:=range []InputKind{Text,Binary} {
     r,err:=Parse(context.Background(),tc.input,Options{Mode:mode,InputKind:kind})
     if tc.duplicates=="" {
      if err!=nil||!r.valid||r.Original()!=tc.input||r.RequestedMode()!=mode||r.EffectiveMode()!=mode||r.Declaration()!=(Declaration{})||len(r.Diagnostics())!=0 {t.Fatalf("kind%d accepted result%+v error%v",kind,r,err)}
      if out,w,err:=OutputText(context.Background(),r);err!=nil||out!=tc.input||w!=(OutputWarning{}) {t.Fatalf("kind%d text%q error%v",kind,out,err)}
      if out,w,err:=OutputBinary(context.Background(),r,6);err!=nil||out!=tc.input||w!=(OutputWarning{}) {t.Fatalf("kind%d binary%q error%v",kind,out,err)}
     }else{
      var e *ParseError
      if !errors.As(err,&e)||r.valid||r.original!=""||r.requested!=0||r.effective!=0||r.declaration!=(Declaration{})||len(r.diagnostics)!=0 {t.Fatalf("kind%d result%+v error%v",kind,r,err)}
      source,caret:=tc.input,strings.Repeat(" ",tc.offset)+"^"
      if tc.name=="no_uri_casefold" {source=`<a xmlns:p="http://EXAMPLE/x" xmlns:q="http://example/x" p:x="1" q:x="2" q:x="3"`;caret=strings.Repeat(" ",79)+"^"}
      primary:="invalid XML content";if mode==Document {primary="invalid XML document"}
      names:=strings.Split(tc.duplicates,",")
      detail:=make([]string,0,len(names))
      if len(e.Diagnostics)!=len(names) {t.Fatalf("kind%d diagnostics%+v",kind,e.Diagnostics)}
      for i,name:=range names {
       offset:=tc.offset
       if tc.name=="duplicate_binding_other"&&i==1 {offset=62;caret=strings.Repeat(" ",62)+"^"}
       message:="Attribute "+name+" redefined"
       want:=Diagnostic{Condition:"XML_ERR_ATTRIBUTE_REDEFINED",Domain:ParserDomain,Severity:Fatal,Line:1,Column:offset+1,Offset:offset,Message:message,SourceContext:source,Caret:caret}
       if e.Diagnostics[i]!=want {t.Fatalf("kind%d diagnostic%+v want%+v",kind,e.Diagnostics[i],want)}
       detail=append(detail,"line 1: "+message+"\n"+source+"\n"+caret)
      }
      if e.Error()!=primary||e.RequestedMode!=mode||e.EffectiveMode!=mode||e.Category!=GrammarError||e.SourceBaseOffset!=0||e.Declaration!=(Declaration{})||e.Detail()!=strings.Join(detail,"\n") {t.Fatalf("kind%d error%+v detail%q want%q",kind,e,e.Detail(),strings.Join(detail,"\n"))}
      if out,w,err:=OutputText(context.Background(),r);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed text%q error%v",kind,out,err)}
      if out,w,err:=OutputBinary(context.Background(),r,6);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed binary%q error%v",kind,out,err)}
     }
     healthy:="<healthy>é</healthy>"
     next,err:=Parse(context.Background(),healthy,Options{Mode:mode,InputKind:kind})
     if err!=nil||!next.valid||next.Original()!=healthy||len(next.Diagnostics())!=0 {t.Fatalf("kind%d healthy reuse%+v error%v",kind,next,err)}
    }
   })
  }
 }
}

func TestXMLCoreNamespaceURIGaps(t *testing.T) {
 // Independent pinned PostgreSQL18.6 UTF8/C artifact683:3–18,19–34
 // supplies all sixteen text-input witnesses and complete ordered Detail.
 // Binary UTF8 and structured fields follow matching Debian libxml2
 // parser.c2332–2435,3920–4155,7525–7625,9598–9636, not SQL-exposed metadata.
 type event struct {condition Condition;message string;offset int}
 zero:=func(offset int) event {return event{"XML_ERR_INVALID_CHAR","xmlParseCharRef: invalid xmlChar value 0",offset}}
 undefined:=func(offset int) event {return event{"XML_ERR_UNDECLARED_ENTITY","Entity 'bad' not defined",offset}}
 duplicate:=func(offset int) event {return event{"XML_ERR_ATTRIBUTE_REDEFINED","Attribute q:x redefined",offset}}
 cases:=[]struct{name,input string;events []event}{
  {"zero_interior",`<a xmlns:p="uv" xmlns:q="u&#0;v" p:x="1" q:x="2" q:x="3"/>`,[]event{zero(30)}},
  {"zero_different_uri",`<a xmlns:p="uw" xmlns:q="u&#0;v" p:x="1" q:x="2" q:x="3"/>`,[]event{zero(30),duplicate(56)}},
  {"zero_reverse_order",`<a xmlns:p="uv" xmlns:q="u&#0;v" q:x="1" q:x="2" p:x="3"/>`,[]event{zero(30),duplicate(56)}},
  {"zero_leading",`<a xmlns:p="uv" xmlns:q="&#0;uv" p:x="1" q:x="2" q:x="3"/>`,[]event{zero(29)}},
  {"zero_trailing",`<a xmlns:p="uv" xmlns:q="uv&#0;" p:x="1" q:x="2" q:x="3"/>`,[]event{zero(31)}},
  {"zero_multiple",`<a xmlns:p="uv" xmlns:q="u&#0;&#0;v" p:x="1" q:x="2" q:x="3"/>`,[]event{zero(30),zero(34)}},
  {"zero_leading_multiple",`<a xmlns:p="uv" xmlns:q="&#0;&#0;uv" p:x="1" q:x="2" q:x="3"/>`,[]event{zero(29),zero(33)}},
  {"undefined_interior",`<a xmlns:p="uv" xmlns:q="u&bad;v" p:x="1" q:x="2" q:x="3"/>`,[]event{undefined(31)}},
  {"undefined_different_uri",`<a xmlns:p="uw" xmlns:q="u&bad;v" p:x="1" q:x="2" q:x="3"/>`,[]event{undefined(31),duplicate(57)}},
  {"undefined_reverse_order",`<a xmlns:p="uv" xmlns:q="u&bad;v" q:x="1" q:x="2" p:x="3"/>`,[]event{undefined(31),duplicate(57)}},
  {"undefined_leading",`<a xmlns:p="uv" xmlns:q="&bad;uv" p:x="1" q:x="2" q:x="3"/>`,[]event{undefined(30)}},
  {"undefined_trailing",`<a xmlns:p="uv" xmlns:q="uv&bad;" p:x="1" q:x="2" q:x="3"/>`,[]event{undefined(32)}},
  {"undefined_multiple",`<a xmlns:p="uv" xmlns:q="u&bad;&bad;v" p:x="1" q:x="2" q:x="3"/>`,[]event{undefined(31),undefined(36)}},
  {"undefined_leading_multiple",`<a xmlns:p="uv" xmlns:q="&bad;&bad;uv" p:x="1" q:x="2" q:x="3"/>`,[]event{undefined(30),undefined(35)}},
  {"mixed_multiple",`<a xmlns:p="uv" xmlns:q="&#0;u&bad;&#0;v&bad;" p:x="1" q:x="2" q:x="3"/>`,[]event{zero(29),undefined(35),zero(39),undefined(45)}},
  {"transformed_then_gap",`<a xmlns:p="uv" xmlns:q="&#117;&#0;v" p:x="1" q:x="2" q:x="3"/>`,[]event{zero(35)}},
 }
 for _,mode:=range []Mode{Content,Document} {
  for _,tc:=range cases {
   t.Run(tc.name+map[Mode]string{Content:"/content",Document:"/document"}[mode],func(t *testing.T){
    for _,kind:=range []InputKind{Text,Binary} {
     r,err:=Parse(context.Background(),tc.input,Options{Mode:mode,InputKind:kind})
     var e *ParseError
     if !errors.As(err,&e)||r.valid||r.original!=""||r.requested!=0||r.effective!=0||r.declaration!=(Declaration{})||r.diagnostics!=nil {t.Fatalf("kind%d result%+v error%v",kind,r,err)}
     primary:="invalid XML content";if mode==Document {primary="invalid XML document"}
     if len(e.Diagnostics)!=len(tc.events) {t.Fatalf("kind%d diagnostics%+v want%+v",kind,e.Diagnostics,tc.events)}
     detail:=make([]string,0,len(tc.events))
     for i,event:=range tc.events {
      caret:=strings.Repeat(" ",event.offset)+"^"
      want:=Diagnostic{Condition:event.condition,Domain:ParserDomain,Severity:Fatal,Line:1,Column:event.offset+1,Offset:event.offset,Message:event.message,SourceContext:tc.input,Caret:caret}
      if e.Diagnostics[i]!=want {t.Fatalf("kind%d diagnostic%+v want%+v",kind,e.Diagnostics[i],want)}
      // Independent literal specification, never the candidate renderer.
      detail=append(detail,"line 1: "+event.message+"\n"+tc.input+"\n"+caret)
     }
     if e.Error()!=primary||e.RequestedMode!=mode||e.EffectiveMode!=mode||e.Category!=GrammarError||e.SourceBaseOffset!=0||e.Declaration!=(Declaration{})||e.Detail()!=strings.Join(detail,"\n") {t.Fatalf("kind%d error%+v detail%q want%q",kind,e,e.Detail(),strings.Join(detail,"\n"))}
     if out,w,err:=OutputText(context.Background(),r);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed text%q error%v",kind,out,err)}
     if out,w,err:=OutputBinary(context.Background(),r,6);err==nil||out!=""||w!=(OutputWarning{}) {t.Fatalf("kind%d failed binary%q error%v",kind,out,err)}
     healthy:=`<a xmlns:p="uv" xmlns:q="uv" p:x="1" q:x="2" q:x="3"/>`
     next,err:=Parse(context.Background(),healthy,Options{Mode:mode,InputKind:kind})
     if err!=nil||!next.valid||next.Original()!=healthy||next.RequestedMode()!=mode||next.EffectiveMode()!=mode||next.Declaration()!=(Declaration{})||len(next.Diagnostics())!=0 {t.Fatalf("kind%d healthy reuse%+v error%v",kind,next,err)}
     if out,w,err:=OutputText(context.Background(),next);err!=nil||out!=healthy||w!=(OutputWarning{}) {t.Fatalf("kind%d healthy text%q error%v",kind,out,err)}
     if out,w,err:=OutputBinary(context.Background(),next,6);err!=nil||out!=healthy||w!=(OutputWarning{}) {t.Fatalf("kind%d healthy binary%q error%v",kind,out,err)}
    }
   })
  }
 }
}
