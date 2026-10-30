package core

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/xml"
)

func xmlMappingRead(s *StatementScope)catalog.Read{
	e:=s.Engine();e.Mu.Lock();defer e.Mu.Unlock()
	changes:=s.Catalog;if changes==nil{changes=s.Owner.Catalog};return e.PrivateCatalogLocked(changes)
}
func xmlMappingType(ctx context.Context,read catalog.Read,d *sqltype.Descriptor,typmod int32)(xml.MappingTypeName,error){
	if err:=ctx.Err();err!=nil{return xml.MappingTypeName{},err}
	if d==nil{return xml.MappingTypeName{},fmt.Errorf("missing XML mapping type")}
	n:=xml.MappingTypeName{Typmod:typmod}
	if d.IsDomain(){n.Kind=xml.MappingDomain}else{switch d.ID(){
	case sqltype.BPChar:n.Kind=xml.MappingChar
	case sqltype.VarChar:n.Kind=xml.MappingVarchar
	case sqltype.Numeric:n.Kind=xml.MappingNumeric
	case sqltype.Int2:n.Kind=xml.MappingSmallint
	case sqltype.Int4:n.Kind=xml.MappingInteger
	case sqltype.Int8:n.Kind=xml.MappingBigint
	case sqltype.Float4:n.Kind=xml.MappingReal
	case sqltype.Float8:n.Kind=xml.MappingDouble
	case sqltype.Bool:n.Kind=xml.MappingBoolean
	case sqltype.Time:n.Kind=xml.MappingTime
	case sqltype.Timetz:n.Kind=xml.MappingTimeTZ
	case sqltype.Timestamp:n.Kind=xml.MappingTimestamp
	case sqltype.Timestamptz:n.Kind=xml.MappingTimestampTZ
	case sqltype.Date:n.Kind=xml.MappingDate
	case sqltype.XML:n.Kind=xml.MappingXML
	case sqltype.Text:n.Kind=xml.MappingText
	case sqltype.Bytea:n.Kind=xml.MappingBytea
	}}
	if n.Kind==xml.MappingUDT||n.Kind==xml.MappingDomain||n.Kind==xml.MappingText||n.Kind==xml.MappingBytea{
		t,ok:=read.Type(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(d.ID())});if !ok{return n,fmt.Errorf("missing XML mapping catalog type %d",d.ID())}
		namespace,ok:=read.Namespace(t.Namespace());if !ok{return n,fmt.Errorf("missing XML mapping type namespace")}
		n.Database=read.MustDatabase(catalog.CurrentDatabaseAddress).Name();n.Namespace=namespace.Name();n.Local=t.Name()
	}
	return n,nil
}
func xmlMappingSchema(ctx context.Context,read catalog.Read,columns []catalog.Column,options xml.RowMappingOptions,relation *catalog.Relation,binaryHex bool)(xml.MappingSchema,error){
	schema:=xml.MappingSchema{Options:options,BinaryHex:binaryHex,Database:read.MustDatabase(catalog.CurrentDatabaseAddress).Name(),Columns:make([]xml.MappingSchemaColumn,len(columns))}
	if relation!=nil{namespace,ok:=read.Namespace(relation.Namespace());if !ok{return schema,fmt.Errorf("missing XML mapping relation namespace")};schema.Namespace=namespace.Name()}
	types:=make([]*sqltype.Descriptor,0,len(columns));seen:=make(map[sqltype.ID]bool,len(columns))
	for i,col:=range columns{
		name,err:=xmlMappingType(ctx,read,col.Usage.Type,-1);if err!=nil{return schema,err};schema.Columns[i]=xml.MappingSchemaColumn{Name:col.Name,Type:name}
		if !seen[col.Usage.Type.ID()]{seen[col.Usage.Type.ID()]=true;types=append(types,col.Usage.Type)}
	}
	for _,d:=range types{base:=d.BaseType();if !seen[base.ID()]{seen[base.ID()]=true;types=append(types,base)}}
	schema.Types=make([]xml.MappingSchemaType,len(types))
	for i,d:=range types{
		name,err:=xmlMappingType(ctx,read,d,-1);if err!=nil{return schema,err};schema.Types[i].Name=name
		if d.IsDomain(){typmod:=int32(-1);base:=d;for base.IsDomain(){usage:=base.DomainBase();if typmod<0&&usage.Typmod>=0{typmod=usage.Typmod};base=usage.Type};schema.Types[i].Base,err=xmlMappingType(ctx,read,base,typmod);if err!=nil{return schema,err}}
	}
	return schema,nil
}
