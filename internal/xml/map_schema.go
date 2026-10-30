package xml

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type MappingTypeKind uint8
const (
	MappingUDT MappingTypeKind = iota
	MappingDomain
	MappingChar
	MappingVarchar
	MappingNumeric
	MappingSmallint
	MappingInteger
	MappingBigint
	MappingReal
	MappingDouble
	MappingBoolean
	MappingTime
	MappingTimeTZ
	MappingTimestamp
	MappingTimestampTZ
	MappingDate
	MappingXML
	MappingText
	MappingBytea
)

type MappingTypeName struct {
	Kind MappingTypeKind
	Database, Namespace, Local string
	Typmod int32
}
type MappingSchemaType struct { Name, Base MappingTypeName }
type MappingSchemaColumn struct { Name string; Type MappingTypeName }
type MappingSchema struct {
	Options RowMappingOptions
	Database, Namespace string
	Columns []MappingSchemaColumn
	Types []MappingSchemaType
	BinaryHex bool
}

func mappingComponent(ctx context.Context, text string)(string,error){
	mapped,err:=MapName(ctx,text,true);if err!=nil{return "",err}
	if !strings.Contains(mapped,"."){return mapped,nil}
	m:=RowMapping{}
	for len(mapped)>0{n:=strings.IndexByte(mapped,'.');if n<0{m.append(ctx,mapped);break};m.append(ctx,mapped[:n],"_x002E_");mapped=mapped[n+1:];if m.err!=nil{return "",m.err}}
	return m.buffer.String(),m.err
}
func mappingMultipart(ctx context.Context, prefix string, components ...string)(string,error){
	m:=RowMapping{};m.append(ctx,prefix)
	for _,component:=range components{mapped,err:=mappingComponent(ctx,component);if err!=nil{return "",err};m.append(ctx,".",mapped)}
	return m.buffer.String(),m.err
}
func mappingTypeName(ctx context.Context,n MappingTypeName)(string,error){
	var name string
	switch n.Kind{
	case MappingChar:name="CHAR";if n.Typmod>=0{name+="_"+strconv.Itoa(int(n.Typmod)-4)}
	case MappingVarchar:name="VARCHAR";if n.Typmod>=0{name+="_"+strconv.Itoa(int(n.Typmod)-4)}
	case MappingNumeric:name="NUMERIC";if n.Typmod>=0{name+="_"+strconv.Itoa(int((n.Typmod-4)>>16)&65535)+"_"+strconv.Itoa(int(n.Typmod-4)&65535)}
	case MappingSmallint:name="SMALLINT"
	case MappingInteger:name="INTEGER"
	case MappingBigint:name="BIGINT"
	case MappingReal:name="REAL"
	case MappingDouble:name="DOUBLE"
	case MappingBoolean:name="BOOLEAN"
	case MappingTime:name="TIME"
	case MappingTimeTZ:name="TIME_WTZ"
	case MappingTimestamp:name="TIMESTAMP"
	case MappingTimestampTZ:name="TIMESTAMP_WTZ"
	case MappingDate:name="DATE"
	case MappingXML:name="XML"
	case MappingUDT,MappingDomain,MappingText,MappingBytea:
		if n.Database==""||n.Namespace==""||n.Local==""{return "",fmt.Errorf("incomplete XML mapping type identity")}
		prefix:="UDT";if n.Kind==MappingDomain{prefix="Domain"};return mappingMultipart(ctx,prefix,n.Database,n.Namespace,n.Local)
	default:return "",fmt.Errorf("invalid XML mapping type kind")
	}
	if n.Kind>=MappingTime&&n.Kind<=MappingTimestampTZ&&n.Typmod>=0{name+="_"+strconv.Itoa(int(n.Typmod))}
	return name,ctx.Err()
}

// MappingSchemaResult can only be minted by checked schema construction.
// Result exposes immutable characters for typed adoption, not minting authority.
type MappingSchemaResult struct { result Result }
func (s MappingSchemaResult) Result()Result{return s.result}

func ConstructMappingSchema(ctx context.Context,s MappingSchema)(MappingSchemaResult,error){
	if err:=ctx.Err();err!=nil{return MappingSchemaResult{},err}
	if s.Options.Relation&&(s.Options.TableName==""||s.Database==""||s.Namespace==""){return MappingSchemaResult{},fmt.Errorf("incomplete XML mapping relation identity")}
	seen:=make(map[MappingTypeName]bool,len(s.Types))
	for i,t:=range s.Types{if i%256==0{if err:=ctx.Err();err!=nil{return MappingSchemaResult{},err}};if seen[t.Name]{return MappingSchemaResult{},fmt.Errorf("duplicate XML mapping schema type")};seen[t.Name]=true}
	for i,col:=range s.Columns{if i%256==0{if err:=ctx.Err();err!=nil{return MappingSchemaResult{},err}};if !seen[col.Type]{return MappingSchemaResult{},fmt.Errorf("missing XML mapping column type")}}
	m:=RowMapping{}
	m.append(ctx,"<xsd:schema\n    xmlns:xsd=\"http://www.w3.org/2001/XMLSchema\"")
	if s.Options.TargetNamespace!=""{m.append(ctx,"\n    targetNamespace=\"",s.Options.TargetNamespace,"\"\n    elementFormDefault=\"qualified\"")}
	m.append(ctx,">\n\n")
	for i,t:=range s.Types{if i%256==0{if err:=ctx.Err();err!=nil{return MappingSchemaResult{},err}};if err:=m.schemaType(ctx,t,s.BinaryHex);err!=nil{return MappingSchemaResult{},err}}
	root:="table";row,table:="RowType","TableType"
	if s.Options.Relation{
		var err error
		root,err=MapName(ctx,s.Options.TableName,true);if err!=nil{return MappingSchemaResult{},err}
		row,err=mappingMultipart(ctx,"RowType",s.Database,s.Namespace,s.Options.TableName);if err!=nil{return MappingSchemaResult{},err}
		table,err=mappingMultipart(ctx,"TableType",s.Database,s.Namespace,s.Options.TableName);if err!=nil{return MappingSchemaResult{},err}
	}else if s.Options.Forest{root="row"}
	m.append(ctx,`<xsd:complexType name="`,row,`">`,"\n  <xsd:sequence>\n")
	for i,col:=range s.Columns{
		if i%256==0{if err:=ctx.Err();err!=nil{return MappingSchemaResult{},err}}
		name,err:=MapName(ctx,col.Name,true);if err!=nil{return MappingSchemaResult{},err};typ,err:=mappingTypeName(ctx,col.Type);if err!=nil{return MappingSchemaResult{},err}
		attr:=`minOccurs="0"`;if s.Options.Nulls{attr=`nillable="true"`}
		m.append(ctx,`    <xsd:element name="`,name,`" type="`,typ,`" `,attr,`></xsd:element>`,"\n")
	}
	m.append(ctx,"  </xsd:sequence>\n</xsd:complexType>\n\n")
	top:=row
	if !s.Options.Forest{top=table;m.append(ctx,`<xsd:complexType name="`,table,`">`,"\n  <xsd:sequence>\n",`    <xsd:element name="row" type="`,row,`" minOccurs="0" maxOccurs="unbounded"/>`,"\n  </xsd:sequence>\n</xsd:complexType>\n\n")}
	m.append(ctx,`<xsd:element name="`,root,`" type="`,top,`"/>`,"\n\n</xsd:schema>")
	if m.err!=nil{return MappingSchemaResult{},m.err};if err:=ctx.Err();err!=nil{return MappingSchemaResult{},err}
	return MappingSchemaResult{result:Result{original:m.buffer.String(),requested:Content,effective:Content,valid:true}},nil
}

func (m *RowMapping) schemaType(ctx context.Context,t MappingSchemaType,hex bool)error{
	name,err:=mappingTypeName(ctx,t.Name);if err!=nil{return err}
	if t.Name.Kind==MappingXML{m.append(ctx,"<xsd:complexType mixed=\"true\">\n  <xsd:sequence>\n    <xsd:any name=\"element\" minOccurs=\"0\" maxOccurs=\"unbounded\" processContents=\"skip\"/>\n  </xsd:sequence>\n</xsd:complexType>\n\n");return m.err}
	m.append(ctx,`<xsd:simpleType name="`,name,`">`,"\n")
	switch t.Name.Kind{
	case MappingChar,MappingVarchar,MappingText:
		m.append(ctx,"  <xsd:restriction base=\"xsd:string\">\n")
		if t.Name.Typmod>=0{m.append(ctx,`    <xsd:maxLength value="`,strconv.Itoa(int(t.Name.Typmod)-4),`"/>`,"\n")}
		m.append(ctx,"  </xsd:restriction>\n")
	case MappingBytea:base:="base64Binary";if hex{base="hexBinary"};m.append(ctx,`  <xsd:restriction base="xsd:`,base,`">`,"\n  </xsd:restriction>\n")
	case MappingNumeric:
		if t.Name.Typmod>=0{m.append(ctx,"  <xsd:restriction base=\"xsd:decimal\">\n",`    <xsd:totalDigits value="`,strconv.Itoa(int((t.Name.Typmod-4)>>16)&65535),`"/>`,"\n",`    <xsd:fractionDigits value="`,strconv.Itoa(int(t.Name.Typmod-4)&65535),`"/>`,"\n  </xsd:restriction>\n")}
	case MappingSmallint,MappingInteger,MappingBigint:
		base,hi,lo:="int","2147483647","-2147483648";if t.Name.Kind==MappingSmallint{base,hi,lo="short","32767","-32768"};if t.Name.Kind==MappingBigint{base,hi,lo="long","9223372036854775807","-9223372036854775808"}
		m.append(ctx,`  <xsd:restriction base="xsd:`,base,`">`,"\n",`    <xsd:maxInclusive value="`,hi,`"/>`,"\n",`    <xsd:minInclusive value="`,lo,`"/>`,"\n  </xsd:restriction>\n")
	case MappingReal,MappingDouble,MappingBoolean:
		base:="float";if t.Name.Kind==MappingDouble{base="double"};if t.Name.Kind==MappingBoolean{base="boolean"};m.append(ctx,`  <xsd:restriction base="xsd:`,base,`"></xsd:restriction>`,"\n")
	case MappingTime,MappingTimeTZ,MappingTimestamp,MappingTimestampTZ,MappingDate:
		base,pattern:="time",`\p{Nd}{2}:\p{Nd}{2}:\p{Nd}{2}`
		if t.Name.Kind==MappingDate{base,pattern="date",`\p{Nd}{4}-\p{Nd}{2}-\p{Nd}{2}`}else{
			if t.Name.Kind==MappingTimestamp||t.Name.Kind==MappingTimestampTZ{base="dateTime";pattern=`\p{Nd}{4}-\p{Nd}{2}-\p{Nd}{2}T`+pattern}
			if t.Name.Typmod<0{pattern+=`(.\p{Nd}+)?`}else if t.Name.Typmod>0{pattern+=`.\p{Nd}{`+strconv.Itoa(int(t.Name.Typmod)-4)+`}`}
			if t.Name.Kind==MappingTimeTZ||t.Name.Kind==MappingTimestampTZ{pattern+=`(\+|-)\p{Nd}{2}:\p{Nd}{2}`}
		}
		m.append(ctx,`  <xsd:restriction base="xsd:`,base,`">`,"\n",`    <xsd:pattern value="`,pattern,`"/>`,"\n  </xsd:restriction>\n")
	case MappingDomain:
		base,err:=mappingTypeName(ctx,t.Base);if err!=nil{return err};m.append(ctx,`  <xsd:restriction base="`,base,`"/>`,"\n")
	}
	m.append(ctx,"</xsd:simpleType>\n\n");return m.err
}
