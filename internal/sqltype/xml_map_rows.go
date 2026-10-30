package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/xml"
)

// XMLRowMapping borrows one typed row at a time. It retains neither values nor
// output environments; its checked XML owner retains only generated characters.
type XMLRowMapping struct {
	builder *xml.RowMapping
	fields []xml.MappingField
	result *Descriptor
	err error
}
func NewXMLRowMapping(ctx context.Context,d *Descriptor,options xml.RowMappingOptions,names []string,schema *xml.MappingSchemaResult)(*XMLRowMapping,error){
	if d==nil||d.ID()!=XML||!d.Concrete(){return nil,fmt.Errorf("invalid XML mapping result descriptor")}
	builder,err:=xml.NewRowMapping(ctx,options,names,schema);if err!=nil{return nil,xmlError(err)}
	return &XMLRowMapping{builder:builder,fields:make([]xml.MappingField,len(names)),result:d},nil
}
func (m *XMLRowMapping) Append(ctx context.Context,row []Value,output OutputEnvironment)(failure error){
	if m.err!=nil{return m.err}
	defer func(){if failure!=nil{m.err=failure}}()
	if len(row)!=len(m.fields){return fmt.Errorf("XML mapping row width mismatch")}
	defer clear(m.fields)
	for i,value:=range row{
		if i%256==0{if err:=ctx.Err();err!=nil{return err}}
		m.fields[i].Null=value.IsNull()
		if value.IsNull(){continue}
		text,err:=XMLValueCharacters(ctx,value,false,output);if err!=nil{return err};m.fields[i].Characters=text
	}
	return xmlError(m.builder.AppendRow(ctx,m.fields))
}
func (m *XMLRowMapping) Finish(ctx context.Context)(Value,error){
	if m.err!=nil{m.fields=nil;return Value{},m.err}
	result,err:=m.builder.Finish(ctx);m.fields=nil
	return xmlConstructed(m.result,result,err)
}
func XMLMappingSchema(ctx context.Context,d *Descriptor,schema xml.MappingSchemaResult)(Value,error){
	if err:=ctx.Err();err!=nil{return Value{},err};return xmlConstructed(d,schema.Result(),nil)
}
