package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/xml"
)

// XMLScopeMapping uses the same typed field conversion and checked buffer as
// XMLRowMapping. The scope owns no SQL values, environments or authority.
type XMLScopeMapping struct { row XMLRowMapping }

func NewXMLScopeMapping(ctx context.Context,d *Descriptor,kind xml.MappingScopeKind,name string,options xml.RowMappingOptions,schema *xml.MappingSchemaResult)(*XMLScopeMapping,error){
	if d==nil||d.ID()!=XML||!d.Concrete(){return nil,fmt.Errorf("invalid XML mapping result descriptor")}
	builder,err:=xml.NewScopeMapping(ctx,kind,name,options,schema);if err!=nil{return nil,xmlError(err)}
	return &XMLScopeMapping{row:XMLRowMapping{builder:builder,result:d}},nil
}
func (m *XMLScopeMapping) step(err error)error{if m.row.err!=nil{return m.row.err};m.row.err=xmlError(err);return m.row.err}
func (m *XMLScopeMapping) BeginSchema(ctx context.Context,name string)error{if m.row.err!=nil{return m.row.err};return m.step(m.row.builder.BeginSchema(ctx,name))}
func (m *XMLScopeMapping) BeginTable(ctx context.Context,name string,names []string)error{
	if m.row.err!=nil{return m.row.err}
	if err:=m.step(m.row.builder.BeginTable(ctx,name,names));err!=nil{return err}
	if cap(m.row.fields)<len(names){m.row.fields=make([]xml.MappingField,len(names))}else{m.row.fields=m.row.fields[:len(names)]}
	return nil
}
func (m *XMLScopeMapping) Append(ctx context.Context,row []Value,output OutputEnvironment)error{return m.row.Append(ctx,row,output)}
func (m *XMLScopeMapping) EndTable(ctx context.Context)error{if m.row.err!=nil{return m.row.err};clear(m.row.fields);m.row.fields=m.row.fields[:0];return m.step(m.row.builder.EndTable(ctx))}
func (m *XMLScopeMapping) EndSchema(ctx context.Context)error{if m.row.err!=nil{return m.row.err};return m.step(m.row.builder.EndSchema(ctx))}
func (m *XMLScopeMapping) Finish(ctx context.Context)(Value,error){
	if m.row.err!=nil{clear(m.row.fields);m.row.fields=nil;m.row.builder=nil;return Value{},m.row.err}
	return m.row.Finish(ctx)
}
