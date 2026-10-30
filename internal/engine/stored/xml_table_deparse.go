package stored

import (
 "context"
 "fmt"
 "strings"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/regobj"
 "stuntgres/internal/parser"
)

func (v *QueryRenderer) renderXMLTableSQL(ctx context.Context, view catalog.Read, raw *parser.XMLTable, ns analyze.ViewNamespace, name ruleRangeName) (viewRenderFrom,error) {
 root:=viewFunctionFromAt(analyze.ViewFromRoot(v.query.Root),ns.Ordinal,true)
 if root==nil || len(root.Calls)!=1 {return viewRenderFrom{},fmt.Errorf("stuntgres: missing frozen XMLTABLE source")}
 table,ok:=root.Calls[0].(functions.XMLTable)
 if !ok {return viewRenderFrom{},fmt.Errorf("stuntgres: invalid frozen XMLTABLE source")}
 parts:=make([]string,len(raw.Expressions()))
 for i,child:=range raw.Expressions() {
  text,err:=v.ExpressionSQL(ctx,view,child,analyze.ViewLiteralPath{Clause:analyze.ViewFunctionLiteral,Source:root.ViewOrdinal,Target:i})
  if err!=nil {return viewRenderFrom{},err};parts[i]=text
 }
 var out strings.Builder
 out.WriteString("XMLTABLE(")
 next:=0
 if len(raw.Namespaces)>0 {
  out.WriteString("XMLNAMESPACES(")
  for i,ns:=range raw.Namespaces {
   if i>0 {out.WriteString(", ")}
   if ns.Default {out.WriteString("DEFAULT ("+parts[next]+")")} else {out.WriteString("("+parts[next]+") AS "+catalog.QuoteIdentifier(ns.Name.Text))}
   next++
  }
  out.WriteString("), ")
 }
 // c_expr operands must remain parenthesized after ordinary deparsing.
 out.WriteString("("+parts[next]+") PASSING ("+parts[next+1]+") COLUMNS ");next+=2
 for i,column:=range raw.Columns {
  if i>0 {out.WriteString(", ")}
  out.WriteString(catalog.QuoteIdentifier(column.Name.Text))
  if column.Ordinality {out.WriteString(" FOR ORDINALITY");continue}
  declaration,err:=regobj.TypeDeclaration(view,table.ResultUsage().Type.CompositeMember(i).Usage)
  if err!=nil {return viewRenderFrom{},err}
  out.WriteString(" "+declaration)
  for _,option:=range column.Options {
   out.WriteString(" "+strings.ToUpper(option.Kind))
   if option.Value!=nil {out.WriteString(" ("+parts[next]+")");next++}
  }
 }
 out.WriteString(")")
 text:=out.String()
 if raw.Lateral || v.Presentation.Deparse && root.Correlated {text="LATERAL "+text}
 from:=viewRenderFrom{text:text,relation:text,root:root}
 for i,column:=range root.Schema() {from.columns=append(from.columns,viewRenderColumn{name:column.Name,slot:i})}
 if v.logicalSourceNames() {from=v.sourceAlias(from,name.name,name.columns,name.explicit || name.name!="xmltable" || len(name.columns)!=0);from.relation=text;return from,nil}
 if v.Presentation.Deparse {
  // Slot qualification must name an alias actually emitted in FROM. The
  // common helper also lets an enclosing written alias replace this one.
  return v.presentationAlias(from,ns,v.namespaceAlias(ns),nil,v.soleSourceUnqualified(raw,root)),nil
 }
 aliased:=v.aliasFrom(from,ns,v.namespaceAlias(ns));aliased.relation=text
 return aliased,nil
}

// Layout is paired with the actual accepted provider before presentation.
// Raw TypeName spelling is deliberately excluded: checked Fields/shape own types.
func validateXMLTableLayout(raw *parser.XMLTable, table functions.XMLTable) error {
 if err:=table.ValidateStoredProvider();err!=nil {return err}
 accepted:=table.Raw
 mismatch:=func()error{return fmt.Errorf("stuntgres: mismatched frozen XMLTABLE source layout")}
 if raw==nil || raw.Pos!=accepted.Pos || raw.Lateral!=accepted.Lateral || len(raw.Namespaces)!=len(accepted.Namespaces) || len(raw.Columns)!=len(accepted.Columns) {return mismatch()}
 for i,ns:=range raw.Namespaces {
  other:=accepted.Namespaces[i]
  if ns.Name!=other.Name || ns.Pos!=other.Pos || ns.Default!=other.Default {return mismatch()}
 }
 for i,column:=range raw.Columns {
  other:=accepted.Columns[i]
  if column.Name!=other.Name || column.Pos!=other.Pos || column.Ordinality!=other.Ordinality || len(column.Options)!=len(other.Options) {return mismatch()}
  for j,option:=range column.Options {
   expected:=other.Options[j]
   if option.Kind!=expected.Kind || option.Pos!=expected.Pos || (option.Value==nil)!=(expected.Value==nil) {return mismatch()}
  }
 }
 return nil
}
