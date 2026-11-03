package xml

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func scopeUnitXSD(database bool,forest bool,ns string)string{
	h:="<xsd:schema\n    xmlns:xsd=\"http://www.w3.org/2001/XMLSchema\"";if ns!=""{h+="\n    targetNamespace=\""+ns+"\"\n    elementFormDefault=\"qualified\""};h+=">\n\n"
	if database{return h+"<xsd:complexType name=\"CatalogType.d\">\n  <xsd:all>\n    <xsd:element name=\"s\" type=\"SchemaType.d.s\"/>\n    <xsd:element name=\"z\" type=\"SchemaType.d.z\"/>\n  </xsd:all>\n</xsd:complexType>\n\n<xsd:element name=\"d\" type=\"CatalogType.d\"/>\n\n</xsd:schema>"}
	group,typ,extra:="all","TableType","";if forest{group="sequence";typ="RowType";extra=` minOccurs="0" maxOccurs="unbounded"`}
	return h+"<xsd:complexType name=\"SchemaType.d.s\">\n  <xsd:"+group+">\n    <xsd:element name=\"t\" type=\""+typ+".d.s.t\""+extra+"/>\n    <xsd:element name=\"empty\" type=\""+typ+".d.s.empty\""+extra+"/>\n  </xsd:"+group+">\n</xsd:complexType>\n\n<xsd:element name=\"s\" type=\"SchemaType.d.s\"/>\n\n</xsd:schema>"
}

func TestXMLMapScopeConstruction(t *testing.T){
	ctx:=context.Background()
	for _,kind:=range []MappingScopeKind{SchemaScope,DatabaseScope}{for _,forest:=range []bool{false,true}{for _,nulls:=range []bool{false,true}{for _,ns:=range []string{"",`urn:"&scope`}{for _,combined:=range []bool{false,true}{
		name:="s";if kind==DatabaseScope{name="d"};options:=RowMappingOptions{Forest:forest,Nulls:nulls,TargetNamespace:ns}
		var token *MappingSchemaResult;schema:=""
		if combined{spec:=MappingScopeSchema{Kind:kind,Database:"d",Namespace:"s",Members:[]string{"t","empty"},Options:options};if kind==DatabaseScope{spec.Members=[]string{"s","z"}};checked,err:=ConstructScopeMappingSchema(ctx,spec);if err!=nil{t.Fatal(err)};schema=scopeUnitXSD(kind==DatabaseScope,forest,ns);if checked.Result().Original()!=schema{t.Fatal("released incomplete XSD",checked.Result().Original(),schema)};token=&checked}
		m,err:=NewScopeMapping(ctx,kind,name,options,token);if err!=nil{t.Fatal(err)}
		if kind==DatabaseScope{if err:=m.BeginSchema(ctx,"s");err!=nil{t.Fatal(err)}}
		if err:=m.BeginTable(ctx,"t",[]string{"a:b","n"});err!=nil{t.Fatal(err)}
		if err:=m.AppendRow(ctx,[]MappingField{{Characters:"1"},{Null:true}});err!=nil{t.Fatal(err)}
		if err:=m.EndTable(ctx);err!=nil{t.Fatal(err)};if err:=m.BeginTable(ctx,"empty",nil);err!=nil{t.Fatal(err)};if err:=m.EndTable(ctx);err!=nil{t.Fatal(err)}
		if kind==DatabaseScope{if err:=m.EndSchema(ctx);err!=nil{t.Fatal(err)};if err:=m.BeginSchema(ctx,"z");err!=nil{t.Fatal(err)};if err:=m.EndSchema(ctx);err!=nil{t.Fatal(err)}}
		fields:="  <a_x003A_b>1</a_x003A_b>\n";if nulls{fields+="  <n xsi:nil=\"true\"/>\n"}
		body:="<t>\n\n<row>\n"+fields+"</row>\n\n</t>\n\n<empty>\n\n</empty>\n\n";if forest{body="<t>\n"+fields+"</t>\n\n\n\n"}
		if kind==DatabaseScope{body="<s>\n\n"+body+"</s>\n\n<z>\n\n</z>\n\n"}
		attrs:=` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`;if ns!=""{attrs+=` xmlns="`+ns+`"`};if combined{if ns==""{attrs+=` xsi:noNamespaceSchemaLocation="#"`}else{attrs+=` xsi:schemaLocation="`+ns+` #"`};body=schema+"\n\n"+body}
		want:="<"+name+attrs+">\n\n"+body+"</"+name+">\n";result,err:=m.Finish(ctx);if err!=nil||!result.Valid()||result.RequestedMode()!=Content||result.EffectiveMode()!=Content||result.Original()!=want{t.Fatal("scope characters/provenance",result.Original(),want,err)}
		if _,err:=m.Finish(ctx);err==nil{t.Fatal("republished scope")}
	}}}}}
	zero:=MappingSchemaResult{};if m,err:=NewScopeMapping(ctx,SchemaScope,"s",RowMappingOptions{},&zero);err==nil||m!=nil{t.Fatal("zero schema token admitted")}
	forged:=MappingSchemaResult{result:Result{original:"<unrelated/>",requested:Content,effective:Content,valid:true}}
	if m,err:=NewScopeMapping(ctx,SchemaScope,"s",RowMappingOptions{},&forged);err==nil||m!=nil||forged.Result().Valid(){t.Fatal("unrelated accepted result forged schema authority")}
	punctuation,err:=ConstructScopeMappingSchema(ctx,MappingScopeSchema{Kind:SchemaScope,Database:"d",Namespace:"s.dot",Members:[]string{"xml.t"}})
	punctuationWant:="<xsd:schema\n    xmlns:xsd=\"http://www.w3.org/2001/XMLSchema\">\n\n<xsd:complexType name=\"SchemaType.d.s_x002E_dot\">\n  <xsd:all>\n    <xsd:element name=\"_x0078_ml.t\" type=\"TableType.d.s_x002E_dot._x0078_ml_x002E_t\"/>\n  </xsd:all>\n</xsd:complexType>\n\n<xsd:element name=\"s.dot\" type=\"SchemaType.d.s_x002E_dot\"/>\n\n</xsd:schema>"
	if err!=nil||punctuation.Result().Original()!=punctuationWant{t.Fatal("scope name mapped twice or multipart separators lost",punctuation.Result().Original(),err)}
	for _,invalid:=range []func(*RowMapping)error{
		func(m *RowMapping)error{return m.EndTable(ctx)},func(m *RowMapping)error{return m.BeginSchema(ctx,"s")},func(m *RowMapping)error{return m.AppendRow(ctx,nil)},
		func(m *RowMapping)error{if err:=m.BeginTable(ctx,"t",[]string{"a"});err!=nil{return err};return m.AppendRow(ctx,nil)},
		func(m *RowMapping)error{if err:=m.BeginTable(ctx,"t",nil);err!=nil{return err};return m.BeginTable(ctx,"again",nil)},
	}{m,err:=NewScopeMapping(ctx,SchemaScope,"s",RowMappingOptions{},nil);if err!=nil{t.Fatal(err)};if err:=invalid(m);err==nil{t.Fatal("invalid transition admitted")};if result,err:=m.Finish(ctx);err==nil||result.Valid()||result.Original()!=""{t.Fatal("poisoned scope published",result,err)}}
}

func TestXMLMapScopeCancellation(t *testing.T){
	healthy:=context.Background()
	for _,operation:=range []string{"names","members","types","embedded","empty","nulls","zero-width","growth","publication"}{
		for checkpoint:=1;;checkpoint++{
			base,cancel:=context.WithCancel(healthy);ctx:=&mappingCancelContext{Context:base,cancel:cancel,remaining:checkpoint}
			var result Result;var err error
			if operation=="members"||operation=="types"{
				spec:=MappingScopeSchema{Kind:SchemaScope,Database:"d",Namespace:"s"};if operation=="members"{spec.Members=[]string{"a","b","c"}}else{spec.Types=make([]MappingSchemaType,3);for i:=range spec.Types{spec.Types[i].Name=MappingTypeName{Kind:MappingUDT,Database:"d",Namespace:"s",Local:strings.Repeat("t",i+1)}}}
				token,failure:=ConstructScopeMappingSchema(ctx,spec);err=failure;result=token.Result()
			}else{
				var schema *MappingSchemaResult;if operation=="embedded"{token,failure:=ConstructScopeMappingSchema(healthy,MappingScopeSchema{Kind:SchemaScope,Database:"d",Namespace:"s",Members:[]string{strings.Repeat("x",5000)}});if failure!=nil{t.Fatal(failure)};schema=&token}
				construct:=healthy;if operation=="embedded"{construct=ctx}
				m,failure:=NewScopeMapping(construct,DatabaseScope,"d",RowMappingOptions{},schema);err=failure
				if err==nil{
					if operation=="publication"{err=m.BeginSchema(healthy,"s");if err==nil{err=m.EndSchema(healthy)}}else{
						err=m.BeginSchema(ctx,"s")
						if err==nil&&operation!="empty"{
							names:=[]string{"a"};fields:=[]MappingField{{Null:true}}
							if operation=="names"{names=make([]string,257);fields=make([]MappingField,257);for i:=range names{names[i]="a"}}
							if operation=="nulls"{names=make([]string,1024);fields=make([]MappingField,1024);for i:=range names{names[i]="a";fields[i].Null=true}}
							if operation=="zero-width"{names=nil;fields=nil}
							if operation=="growth"{fields=[]MappingField{{Characters:strings.Repeat("x",24000)}}}
							nameContext:=healthy;if operation=="names"{nameContext=ctx}
							err=m.BeginTable(nameContext,"t",names);if err==nil{err=m.AppendRow(ctx,fields)};if err==nil{err=m.EndTable(ctx)}
						}
						if err==nil{err=m.EndSchema(ctx)}
					}
					if err==nil{result,err=m.Finish(ctx)}else{result,_=m.Finish(healthy)}
				}
			}
			effective:=base.Err();cancel()
			if err==nil{if !result.Valid()||effective!=nil||checkpoint<2{t.Fatal("checkpoint sweep",operation,checkpoint)};break}
			if !errors.Is(err,context.Canceled)||!errors.Is(effective,context.Canceled)||result.Valid()||result.Original()!=""{t.Fatal("partial canceled scope",operation,checkpoint,result,err)}
		}
	}
	if appendSize(int(^uint(0)>>1),1){t.Fatal("scope growth overflow admitted")}
}
