package core

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/xml"
)

func (h *statementHost) MapXMLRows(op functions.XMLMappingOperation,args [5]sqltype.Value,result *sqltype.Descriptor,output sqltype.OutputEnvironment)(sqltype.Value,error){
	s:=h.scope();ctx:=s.Op.Context()
	if err:=ctx.Err();err!=nil{return sqltype.Value{},err}
	if op.Name()==""{return sqltype.Value{},fmt.Errorf("invalid XML mapping operation")}
	start:=1;if op==functions.XMLCursorRows{start=2}
	nulls,err:=args[start].Boolean();if err!=nil{return sqltype.Value{},err};forest,err:=args[start+1].Boolean();if err!=nil{return sqltype.Value{},err};ns,err:=args[start+2].Text();if err!=nil{return sqltype.Value{},err}
	options:=xml.RowMappingOptions{Relation:op.Relation(),Nulls:nulls,Forest:forest,TargetNamespace:ns}
	read:=xmlMappingRead(s)
	var relation *catalog.Relation
	var text string
	if op.Relation(){
		oid,err:=args[0].ClassOID();if err!=nil{return sqltype.Value{},err}
		relation,_=read.Relation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.OID(oid)})
		if relation!=nil{read,relation,err=s.lockXMLMappingRelation(read,relation);if err!=nil{return sqltype.Value{},err}}
		if relation!=nil{options.TableName=relation.Name()}else if op.Schema(){return sqltype.Value{},SQLError("XX000",fmt.Sprintf("could not open relation with OID %d",oid))}
		spelling,err:=args[0].Type().OutputWithEnvironment(ctx,args[0],output);if err!=nil{return sqltype.Value{},err};text="SELECT * FROM "+spelling
		if err:=xmlMappingTableEligibility(relation,text,op.Schema());err!=nil{return sqltype.Value{},err}
	}else{text,err=args[0].Text();if err!=nil{return sqltype.Value{},err}}
	if op.Cursor(){count:=int64(0);if op==functions.XMLCursorRows{count,err=args[1].Integer();if err!=nil{return sqltype.Value{},err}};return h.xmlCursorMapping(op,text,count,options,result,output,read)}
	var schemaResult *xml.MappingSchemaResult
	if op.Schema(){
		var schemaValue sqltype.Value
		build:=func(columns []catalog.Column)error{schema,err:=xmlMappingSchema(ctx,read,columns,options,relation,output.XMLBinaryHex);if err!=nil{return err};token,err:=xml.ConstructMappingSchema(ctx,schema);if err!=nil{return err};schemaResult=&token;if !op.Combined(){schemaValue,err=sqltype.XMLMappingSchema(ctx,result,token)};return err}
		if relation!=nil{err=build(relation.Columns)}else{err=s.xmlMappingQuery(text,true,func(rows exec.RowCursor)error{return build(rows.Schema())})}
		if err!=nil{return sqltype.Value{},err}
		if !op.Combined(){return schemaValue,nil}
	}
	var value sqltype.Value
	err=s.xmlMappingQuery(text,false,func(rows exec.RowCursor)error{var err error;value,err=consumeXMLMapping(s,rows,options,schemaResult,result,output,text);return err})
	if err!=nil{return sqltype.Value{},err};return value,nil
}

func consumeXMLMapping(s *StatementScope,rows exec.RowCursor,options xml.RowMappingOptions,schema *xml.MappingSchemaResult,result *sqltype.Descriptor,output sqltype.OutputEnvironment,sql string)(sqltype.Value,error){
	ctx:=s.Op.Context();columns:=rows.Schema();names:=make([]string,len(columns));for i,col:=range columns{names[i]=col.Name}
	mapper,err:=sqltype.NewXMLRowMapping(ctx,result,options,names,schema);if err!=nil{return sqltype.Value{},err}
	row:=make([]sqltype.Value,len(columns));defer clear(row)
	for {
		present,err:=rows.Next(ctx,row);if err!=nil{if sql!=""{err=exec.SPIStatementDiagnostic(err,sql)};return sqltype.Value{},err};if !present{break}
		env:=output;if typed,ok:=rows.(exec.RowResult);ok{env=typed.OutputEnvironment();env.XMLBinaryHex,env.Zone,env.XMLNotice=output.XMLBinaryHex,output.Zone,output.XMLNotice}
		if err:=mapper.Append(ctx,row,env);err!=nil{return sqltype.Value{},err}
	}
	return mapper.Finish(ctx)
}
