package xml

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unsafe"
)

func TestXMLMapRowsConstruction(t *testing.T){
	ctx:=context.Background()
	for _,tc:=range []struct{options RowMappingOptions;names []string;fields []MappingField;want string}{
		{RowMappingOptions{Nulls:true},[]string{"a","b"},[]MappingField{{Characters:"1"},{Null:true}},"<table xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\">\n\n<row>\n  <a>1</a>\n  <b xsi:nil=\"true\"/>\n</row>\n\n</table>\n"},
		{RowMappingOptions{Forest:true,TargetNamespace:`a"b`},[]string{"xmlName"},[]MappingField{{Characters:"\x01"}},"<row xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xmlns=\"a\"b\">\n  <_x0078_mlName>\x01</_x0078_mlName>\n</row>\n\n"},
		{RowMappingOptions{Relation:true,TableName:"a:b",Forest:true},nil,[]MappingField{},"<a_x003A_b xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\">\n</a_x003A_b>\n\n"},
	}{m,err:=NewRowMapping(ctx,tc.options,tc.names,nil);if err!=nil{t.Fatal(err)};if err:=m.AppendRow(ctx,tc.fields);err!=nil{t.Fatal(err)};r,err:=m.Finish(ctx);if err!=nil||!r.Valid()||r.RequestedMode()!=Content||r.EffectiveMode()!=Content||r.Original()!=tc.want{t.Fatal(r,err,tc.want)};backing:=unsafe.StringData(r.Original());clear(tc.fields);if r.Original()!=tc.want||unsafe.StringData(r.Original())!=backing{t.Fatal("row scratch retained")};if _,err:=m.Finish(ctx);err==nil{t.Fatal("mapper finished twice")}}
	for _,forest:=range []bool{false,true}{m,err:=NewRowMapping(ctx,RowMappingOptions{Forest:forest},nil,nil);if err!=nil{t.Fatal(err)};r,err:=m.Finish(ctx);want:="";if !forest{want="<table xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\">\n\n</table>\n"};if err!=nil||!r.Valid()||r.Original()!=want{t.Fatal(r,err)}}
	if m,err:=NewRowMapping(ctx,RowMappingOptions{Relation:true},nil,nil);err==nil||m!=nil{t.Fatal("empty relation identity admitted")}
	zero:=MappingSchemaResult{};if m,err:=NewRowMapping(ctx,RowMappingOptions{},nil,&zero);err==nil||m!=nil{t.Fatal("zero schema authority admitted")}
	m,err:=NewRowMapping(ctx,RowMappingOptions{},[]string{"a"},nil);if err!=nil{t.Fatal(err)};if err:=m.AppendRow(ctx,nil);err==nil{t.Fatal("width mismatch admitted")};if r,err:=m.Finish(ctx);err==nil||r.Valid()||r.Original()!=""{t.Fatal("failed mapper published",r,err)}
	integer:=MappingTypeName{Kind:MappingInteger,Typmod:-1}
	schema:=MappingSchema{Columns:[]MappingSchemaColumn{{Name:"a",Type:integer}},Types:[]MappingSchemaType{{Name:integer}}}
	token,err:=ConstructMappingSchema(ctx,schema);if err!=nil{t.Fatal(err)}
	wantSchema:="<xsd:schema\n    xmlns:xsd=\"http://www.w3.org/2001/XMLSchema\">\n\n<xsd:simpleType name=\"INTEGER\">\n  <xsd:restriction base=\"xsd:int\">\n    <xsd:maxInclusive value=\"2147483647\"/>\n    <xsd:minInclusive value=\"-2147483648\"/>\n  </xsd:restriction>\n</xsd:simpleType>\n\n<xsd:complexType name=\"RowType\">\n  <xsd:sequence>\n    <xsd:element name=\"a\" type=\"INTEGER\" minOccurs=\"0\"></xsd:element>\n  </xsd:sequence>\n</xsd:complexType>\n\n<xsd:complexType name=\"TableType\">\n  <xsd:sequence>\n    <xsd:element name=\"row\" type=\"RowType\" minOccurs=\"0\" maxOccurs=\"unbounded\"/>\n  </xsd:sequence>\n</xsd:complexType>\n\n<xsd:element name=\"table\" type=\"TableType\"/>\n\n</xsd:schema>"
	if token.Result().Original()!=wantSchema||!token.Result().Valid(){t.Fatal("schema template",token.Result().Original())}
	m,err=NewRowMapping(ctx,RowMappingOptions{},[]string{"a"},&token);if err!=nil{t.Fatal(err)};if err=m.AppendRow(ctx,[]MappingField{{Characters:"1"}});err!=nil{t.Fatal(err)};r,err:=m.Finish(ctx);want:="<table xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xsi:noNamespaceSchemaLocation=\"#\">\n\n"+wantSchema+"\n\n<row>\n  <a>1</a>\n</row>\n\n</table>\n";if err!=nil||r.Original()!=want{t.Fatal("combined template",r.Original(),err)}
	schema.Types=nil;if result,err:=ConstructMappingSchema(ctx,schema);err==nil||result.Result().Valid(){t.Fatal("unbound schema type admitted")}
	if got,err:=mappingMultipart(ctx,"RowType","a.b","xml","_x");err!=nil||got!="RowType.a_x002E_b._x0078_ml._x005F_x"{t.Fatal("multipart escaping",got,err)}
}

type mappingCancelContext struct{context.Context;cancel context.CancelFunc;remaining int}
func (c *mappingCancelContext)Err()error{c.remaining--;if c.remaining==0{c.cancel()};return c.Context.Err()}

func TestXMLMapRowsCancellation(t *testing.T){
	healthy:=context.Background()
	for _,kind:=range []string{"nulls","empty","growth","zero-width"}{
		names:=make([]string,1024);fields:=make([]MappingField,len(names));for i:=range names{names[i]="f";fields[i].Null=kind=="nulls"}
		if kind=="empty"{names=names[:4];fields=fields[:4]}
		if kind=="growth"{names=[]string{"f"};fields=[]MappingField{{Characters:strings.Repeat("x",24000)}}}
		if kind=="zero-width"{names=nil;fields=nil}
		// Sweep actual checkpoints, including the terminal check after emission.
		for checkpoint:=1;;checkpoint++{
			m,err:=NewRowMapping(healthy,RowMappingOptions{},names,nil);if err!=nil{t.Fatal(err)}
			if kind=="growth"{if err:=m.AppendRow(healthy,fields);err!=nil{t.Fatal(err)}}
			underlying,cancel:=context.WithCancel(healthy);ctx:=&mappingCancelContext{Context:underlying,cancel:cancel,remaining:checkpoint}
			err=m.AppendRow(ctx,fields);var result Result;if err==nil{result,err=m.Finish(ctx)}else{result,_=m.Finish(healthy)}
			effective:=underlying.Err();cancel()
			if err==nil{if !result.Valid()||effective!=nil||checkpoint<2{t.Fatal("checkpoint sweep failed",kind,checkpoint)};break}
			if !errors.Is(err,context.Canceled)||!errors.Is(effective,context.Canceled)||result.Valid()||result.Original()!=""{t.Fatal("partial result after cancellation",kind,checkpoint,result,err)}
		}
	}
	ctx,cancel:=context.WithCancel(healthy);cancel();if m,err:=NewRowMapping(ctx,RowMappingOptions{},nil,nil);!errors.Is(err,context.Canceled)||m!=nil{t.Fatal(m,err)}
	integer:=MappingTypeName{Kind:MappingInteger,Typmod:-1};schema:=MappingSchema{Columns:[]MappingSchemaColumn{{Name:"a",Type:integer}},Types:[]MappingSchemaType{{Name:integer}}}
	if token,err:=ConstructMappingSchema(ctx,schema);!errors.Is(err,context.Canceled)||token.Result().Valid(){t.Fatal("canceled schema published",token,err)}
	schema.Columns=make([]MappingSchemaColumn,4096);for i:=range schema.Columns{schema.Columns[i]=MappingSchemaColumn{Name:"a",Type:integer}}
	underlying,stop:=context.WithCancel(healthy);defer stop()
	mid:=&mappingCancelContext{Context:underlying,cancel:stop,remaining:5}
	if token,err:=ConstructMappingSchema(mid,schema);!errors.Is(err,context.Canceled)||!errors.Is(underlying.Err(),context.Canceled)||token.Result().Valid(){t.Fatal("mid-schema publication",token,err)}
	for _,col:=range schema.Columns{if col!=(MappingSchemaColumn{Name:"a",Type:integer}){t.Fatal("schema input mutated")}}
	if token,err:=ConstructMappingSchema(healthy,schema);err!=nil||!token.Result().Valid(){t.Fatal("healthy schema reuse after cancellation",err)}
	if appendSize(int(^uint(0)>>1),1){t.Fatal("mapping size overflow admitted")}
}
