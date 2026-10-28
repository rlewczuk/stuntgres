package xml

// quoted follows xmlBufferWriteQuotedString: prefer double quotes, then single
// quotes; only embedded double quotes need escaping when both occur.
func (w *serializer) quoted(s string){
 double,single:=false,false
 for i:=range len(s){if i&4095==0&&!w.poll(){return};if s[i]=='"'{double=true};if s[i]=='\''{single=true}}
 if double&&!single{w.put("'");w.put(s);w.put("'");return}
 w.put("\"");start:=0;for i:=range len(s){if i&4095==0&&!w.poll(){return};if s[i]=='"'{w.put(s[start:i]);w.put("&quot;");start=i+1}};w.put(s[start:]);w.put("\"")
}
func (w *serializer) identifiers(public,system string,isPublic,hasSystem bool){if isPublic{w.put(" PUBLIC ");w.quoted(public);if hasSystem{w.put(" ");w.quoted(system)}}else{w.put(" SYSTEM ");w.quoted(system)}}
func (w *serializer) saveDTD(d *doctype){
 w.put("<!DOCTYPE ");w.put(d.name);if d.external{w.identifiers(d.publicID,d.systemID,d.public,true)}
 populated:=false;for _,decl:=range d.declarations{if !w.poll(){return};if decl.kind!="PI"&&decl.kind!="COMMENT"&&(decl.kind!="ATTLIST"||len(decl.attributes)>0){populated=true}}
 if !populated{w.put(">");return};w.put(" [\n")
 for _,decl:=range d.declarations{if !w.poll(){return};if decl.kind=="NOTATION"{w.put("<!NOTATION ");w.put(decl.name);w.identifiers(decl.publicID,decl.systemID,decl.public,decl.systemPresent);w.put(" >\n")}}
 for _,decl:=range d.declarations{if !w.poll(){return};switch decl.kind{
 case "NOTATION":continue
 case "COMMENT":w.put("<!--");w.put(decl.value);w.put("-->")
 case "PI":w.put("<?");w.put(decl.name);if decl.dataPresent{w.put(" ");w.put(decl.value)};w.put("?>")
 case "ELEMENT":w.put("<!ELEMENT ");w.put(decl.name);w.put(" ");if decl.model==nil{w.put(decl.elementType)}else{w.put("(");w.model(decl.model,0,true);w.put(")");w.occurrence(decl.model.occurrence)};w.put(">\n")
 case "ATTLIST":for _,a:=range decl.attributes{if !w.poll(){return};w.put("<!ATTLIST ");w.put(decl.name);w.put(" ");w.put(a.name);w.put(" ");if a.typ=="ENUMERATION"||a.typ=="NOTATION"{if a.typ=="NOTATION"{w.put("NOTATION ")};w.put("(");for i,m:=range a.members{if !w.poll(){return};if i>0{w.put(" | ")};w.put(m)};w.put(")")}else{w.put(a.typ)};if a.defaultKind!=""{w.put(" ");w.put(a.defaultKind)};if a.supplied{w.put(" ");w.quoted(a.value)};w.put(">\n")}
 case "ENTITY":
  w.put("<!ENTITY ");if decl.parameter{w.put("% ")};w.put(decl.name)
  if decl.external{w.identifiers(decl.publicID,decl.systemID,decl.public,decl.systemPresent);if decl.unparsed&&decl.notation!=""{w.put(" NDATA ");w.put(decl.notation)}}else{w.put(" ");w.quoted(decl.originalValue)};w.put(">\n")
 }};w.put("]>")
}
func (w *serializer) occurrence(c byte){if c!=0{var b [1]byte;b[0]=c;w.put(string(b[:]))}}
func (w *serializer) model(m *contentModel,parent byte,root bool){
 if m==nil||!w.poll(){return}
 if len(m.children)==0{w.put(m.name);if !root{w.occurrence(m.occurrence)};return}
 parens:=!root&&(m.kind!=parent||m.occurrence!=0)
 if parens{w.put("(")}
 for i,child:=range m.children{if !w.poll(){return};if i>0{if m.kind=='|'{w.put(" | ")}else{w.put(" , ")}};w.model(child,m.kind,false)}
 if parens{w.put(")")};if !root{w.occurrence(m.occurrence)}
}
