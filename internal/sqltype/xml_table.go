package sqltype

import (
 "context"
 "errors"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/xml"
)

// XMLTableInvocation is operation-local; only owned Values leave this adapter.
type XMLTableInvocation struct { table *xml.Table }
func StartXMLTable(ctx context.Context, document Value, env InputEnvironment) (*XMLTableInvocation, error) {
 result, err := document.XML()
 if err != nil { return nil, err }
 table, events, err := xml.StartTable(ctx, result)
 xmlWarnings(events, env)
 if err != nil { return nil, xmlInputError(err, env) }
 return &XMLTableInvocation{table:table}, nil
}
func (t *XMLTableInvocation) Namespace(prefix, uri string, env InputEnvironment) error { return xmlInputError(t.table.SetNamespace(prefix, uri), env) }
func (t *XMLTableInvocation) RowFilter(path string, env InputEnvironment) error { return xmlInputError(t.table.SetRowFilter(path), env) }
func (t *XMLTableInvocation) ColumnFilter(path string, env InputEnvironment) error { return xmlInputError(t.table.SetColumnFilter(path), env) }
func (t *XMLTableInvocation) Next(env InputEnvironment) (bool,error) { ok,err:=t.table.Next(); return ok,xmlInputError(err,env) }
func (t *XMLTableInvocation) Column(ctx context.Context, index int, usage Usage, env InputEnvironment) (Value,bool,error) {
 // Only the actual XML type, not a domain over XML, gets node-sequence output.
 result, err := t.table.Column(index, usage.Type.ID()==XML)
 if err != nil {
  var cardinality *xml.TableCardinalityError
  if errors.As(err,&cardinality) { return Value{},false,&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"21000",Message:err.Error()} }
  return Value{},false,xmlInputError(err,env)
 }
 if result.Missing { return Null(usage.Type),true,nil }
 text:=result.Text
 if result.Boolean && CommonTypeCategory(usage.Type)=='N' { if text=="true" { text="1" } else { text="0" } }
 // Every present result uses genuine target input, including session XML mode.
 value,err:=usage.Type.InputWithEnvironment(ctx,text,usage.Typmod,env)
 return value,false,err
}
