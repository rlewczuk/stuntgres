package xml

import (
 "context"
 "errors"
 "fmt"
)

// Table owns one invocation's document, namespace resolver and XPath frames.
// Neither accepted SQL providers nor immutable Results retain this state.
type Table struct {
 frame *xpathFrame
 rows []*xpathNode
 columns []*xpathExpr
 next int
 row *xpathExpr
 selected bool
}

func documentFrame(ctx context.Context, value Result) (*xpathFrame, []Diagnostic, error) {
 if !value.Valid() { return nil, nil, fmt.Errorf("invalid XML value") }
 input := value.original
 declaration, detail, err := readContentDeclaration(ctx, input)
 if err != nil { return nil, nil, err }
 if detail == "" && declaration.Present { input = input[declaration.End:] }
 _, document, events, err := parseConfigured(ctx, input, Options{Mode: Document}, parsePolicy{collect:true, strictNamespaces:true, strictValidity:true, preserveEntities:true, omitDefaultAttributes:true, documentContext:true})
 if err != nil {
  var pe *ParseError
  if errors.As(err, &pe) { err = &XPathContextError{Parse:pe} }
  return nil, events, err
 }
 frame, err := newXPathTree(ctx, document)
 return frame, events, err
}

func StartTable(ctx context.Context, value Result) (*Table, []Diagnostic, error) {
 frame, events, err := documentFrame(ctx, value)
 if err != nil { return nil, events, err }
 frame.bindings = make(map[string]string)
 return &Table{frame:frame}, events, nil
}
func (t *Table) SetNamespace(prefix, uri string) error {
 if !t.frame.poll() { return t.frame.p.err }
 t.frame.bindings[prefix] = uri
 return nil
}
func (t *Table) SetRowFilter(path string) error {
 if path == "" { return &XPathError{Stage:"row-empty"} }
 expression, err := compileXPath(t.frame.p.ctx, path)
 if err != nil { return err }
 t.row = expression
 return nil
}
func (t *Table) SetColumnFilter(path string) error {
 if path == "" { return &XPathError{Stage:"column-empty"} }
 expression, err := compileXPath(t.frame.p.ctx, path)
 if err != nil { return err }
 t.columns = append(t.columns, expression)
 return nil
}
func (t *Table) Next() (bool, error) {
 if !t.frame.poll() { return false, t.frame.p.err }
 if !t.selected {
  result := t.frame.eval(t.row)
  if t.frame.p.err != nil { return false, t.frame.p.err }
  if result.kind == xpathNodes { t.rows = result.nodes }
  t.selected = true
 }
 if t.next == len(t.rows) { return false, nil }
 t.frame.node = t.rows[t.next]
 t.next++
 return true, nil
}

// TableValue distinguishes a missing node-set from a present empty scalar.
type TableValue struct {
 Missing bool
 Text string
 Boolean bool
}
func (t *Table) Column(index int, xmlOutput bool) (TableValue, error) {
 f := t.frame
 value := f.eval(t.columns[index])
 if f.p.err != nil { return TableValue{}, f.p.err }
 out := TableValue{Boolean:value.kind == xpathBoolean}
 if value.kind == xpathNodes {
  if len(value.nodes) == 0 { out.Missing = true; return out, nil }
  if !xmlOutput && len(value.nodes) > 1 { return out, &TableCardinalityError{} }
  if !xmlOutput { out.Text = f.stringValue(value.nodes[0]); return out, f.p.err }
  w := serializer{p:parser{ctx:f.p.ctx}, utf8:true}
  for _, n := range value.nodes {
   if !w.poll() { break }
   t.serializeNode(&w, n)
  }
  out.Text = w.b.String()
  return out, w.p.err
 }
 out.Text = f.string(value)
 if xmlOutput {
  w := serializer{p:parser{ctx:f.p.ctx}, utf8:true}
  w.escape(out.Text, false)
  out.Text = w.b.String()
  return out, w.p.err
 }
 return out, f.p.err
}

type TableCardinalityError struct{}
func (*TableCardinalityError) Error() string { return "more than one value returned by column XPath expression" }

func (t *Table) serializeNode(w *serializer, n *xpathNode) {
 switch n.kind {
 case xpathRoot:
  for _, child := range t.frame.tree.document.nodes {
   if !w.poll() { break }
   w.node(child, 0, false, nil)
   w.put("\n")
  }
 case xpathElement:
  // Copies preserve local declarations and reconcile only bindings referenced
  // by names in the selected subtree, never unrelated ancestor declarations.
  if n.parent==nil || n.parent.kind!=xpathElement || len(n.namespaces)<=1 {
   w.node(n.source,0,false,nil)
  } else {
   namespaces,changed:=tableNamespaces(w,n.source)
   if changed { copy:=*n.source;copy.namespaces=namespaces;w.node(&copy,0,false,nil) } else { w.node(n.source,0,false,nil) }
  }
 case xpathAttribute:
  w.escape(n.value, true)
 case xpathNamespace:
  w.put(n.value)
 case xpathText:
  if n.source.kind==cdataNode {
   // Projection may coalesce adjacent CDATA. Serialize that value without
   // changing the invocation tree's original first section or its siblings.
   source:=*n.source
   source.value=n.value
   w.node(&source,0,false,nil)
  } else {
   w.escape(n.value, false)
  }
 case xpathComment:
  w.put("<!--"); w.put(n.value); w.put("-->")
 case xpathPI:
  w.put("<?"); w.put(n.name)
  if n.value != "" { w.put(" "); w.put(n.value) }
  w.put("?>")
 }
}
