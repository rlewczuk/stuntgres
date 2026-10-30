package stored

import (
 "fmt"
 "strings"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/parser"
 "stuntgres/internal/xml"
)

func xmlConstructRecipe(raw parser.XMLConstructExpr,children []string,captured xmlCapture)(string,error){
 if len(children)!=len(raw.Children())||len(captured.Names)!=len(raw.Named){return "",fmt.Errorf("invalid XML constructor rendering operands")}
 var out strings.Builder;out.WriteString(strings.ToUpper(raw.Kind.String()));out.WriteByte('(')
 named:=func() {
  for i:=range raw.Named{if i>0{out.WriteString(", ")};out.WriteString(children[i]);out.WriteString(" AS ");out.WriteString(catalog.QuoteIdentifier(xml.UnmapName(captured.Names[i])))}
 }
 switch raw.Kind {
 case parser.XMLConcat:out.WriteString(strings.Join(children,", "))
 case parser.XMLElement:
  out.WriteString("NAME ");out.WriteString(catalog.QuoteIdentifier(xml.UnmapName(captured.Name)))
  if len(raw.Named)>0{out.WriteString(", XMLATTRIBUTES(");named();out.WriteByte(')')}
  for _,text:=range children[len(raw.Named):]{out.WriteString(", ");out.WriteString(text)}
 case parser.XMLForest:named()
 case parser.XMLPI:out.WriteString("NAME ");out.WriteString(catalog.QuoteIdentifier(xml.UnmapName(captured.Name)));if len(children)>0{out.WriteString(", ");out.WriteString(children[0])}
 case parser.XMLRoot:
  out.WriteString(children[0]);out.WriteString(", VERSION ");if raw.Version!=nil{out.WriteString(children[1])}else{out.WriteString("NO VALUE")}
  switch raw.Standalone{case 1:out.WriteString(", STANDALONE NO VALUE");case 2:out.WriteString(", STANDALONE YES");case 3:out.WriteString(", STANDALONE NO")}
 default:return "",fmt.Errorf("invalid XML constructor rendering kind")
 }
 out.WriteByte(')');return out.String(),nil
}
