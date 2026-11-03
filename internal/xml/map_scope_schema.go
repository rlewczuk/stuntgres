package xml

import (
	"context"
	"fmt"
)

type MappingScopeSchema struct {
	Kind MappingScopeKind
	Database, Namespace string
	Members []string
	Types []MappingSchemaType
	Options RowMappingOptions
	BinaryHex bool
}

// ConstructScopeMappingSchema deliberately emits only the scope's own complex
// type. Its table/schema references are not recursive schema definitions.
func ConstructScopeMappingSchema(ctx context.Context,s MappingScopeSchema)(MappingSchemaResult,error){
	if err:=ctx.Err();err!=nil{return MappingSchemaResult{},err}
	if s.Database==""||(s.Kind!=SchemaScope&&s.Kind!=DatabaseScope)||s.Kind==SchemaScope&&s.Namespace==""{return MappingSchemaResult{},fmt.Errorf("incomplete XML mapping scope identity")}
	m:=RowMapping{}
	m.append(ctx,"<xsd:schema\n    xmlns:xsd=\"http://www.w3.org/2001/XMLSchema\"")
	if s.Options.TargetNamespace!=""{m.append(ctx,"\n    targetNamespace=\"",s.Options.TargetNamespace,"\"\n    elementFormDefault=\"qualified\"")}
	m.append(ctx,">\n\n")
	seen:=make(map[MappingTypeName]bool,len(s.Types))
	for i,t:=range s.Types{
		if i%256==0{if err:=ctx.Err();err!=nil{return MappingSchemaResult{},err}}
		if seen[t.Name]{return MappingSchemaResult{},fmt.Errorf("duplicate XML mapping schema type")};seen[t.Name]=true
		if err:=m.schemaType(ctx,t,s.BinaryHex);err!=nil{return MappingSchemaResult{},err}
	}
	root:=s.Database;prefix:="CatalogType";components:=[]string{s.Database};group:="all"
	if s.Kind==SchemaScope{root=s.Namespace;prefix="SchemaType";components=append(components,s.Namespace);if s.Options.Forest{group="sequence"}}
	top,err:=mappingMultipart(ctx,prefix,components...);if err!=nil{return MappingSchemaResult{},err}
	mapped,err:=MapName(ctx,root,true);if err!=nil{return MappingSchemaResult{},err}
	m.append(ctx,`<xsd:complexType name="`,top,`">`,"\n  <xsd:",group,">\n")
	for i,member:=range s.Members{
		if i%256==0{if err:=ctx.Err();err!=nil{return MappingSchemaResult{},err}}
		if member==""{return MappingSchemaResult{},fmt.Errorf("empty XML mapping scope member")}
		name,err:=MapName(ctx,member,true);if err!=nil{return MappingSchemaResult{},err}
		var typ string
		if s.Kind==DatabaseScope{typ,err=mappingMultipart(ctx,"SchemaType",s.Database,member)}else{
			prefix:="TableType";if s.Options.Forest{prefix="RowType"};typ,err=mappingMultipart(ctx,prefix,s.Database,s.Namespace,member)
		}
		if err!=nil{return MappingSchemaResult{},err}
		m.append(ctx,`    <xsd:element name="`,name,`" type="`,typ,`"`)
		if s.Kind==SchemaScope&&s.Options.Forest{m.append(ctx,` minOccurs="0" maxOccurs="unbounded"`)}
		m.append(ctx,"/>\n")
	}
	m.append(ctx,"  </xsd:",group,">\n</xsd:complexType>\n\n",`<xsd:element name="`,mapped,`" type="`,top,`"/>`,"\n\n</xsd:schema>")
	if m.err!=nil{return MappingSchemaResult{},m.err};if err:=ctx.Err();err!=nil{return MappingSchemaResult{},err}
	return MappingSchemaResult{result:Result{original:m.buffer.String(),requested:Content,effective:Content,valid:true},checked:true},nil
}
