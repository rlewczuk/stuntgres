package core

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/xml"
)

func (h *statementHost) xmlScopeMapping(c *exec.Context,op functions.XMLMappingOperation,args [5]sqltype.Value,result *sqltype.Descriptor,output sqltype.OutputEnvironment)(value sqltype.Value,failure error){
	s:=h.scope();ctx:=s.Op.Context()
	start:=1;kind:=xml.SchemaScope;if op.DatabaseScope(){start=0;kind=xml.DatabaseScope}
	nulls,err:=args[start].Boolean();if err!=nil{return value,err};forest,err:=args[start+1].Boolean();if err!=nil{return value,err};target,err:=args[start+2].Text();if err!=nil{return value,err}
	options:=xml.RowMappingOptions{Nulls:nulls,Forest:forest,TargetNamespace:target}
	auth,err:=h.xmlMappingScopeAuthority(c);if err!=nil{return value,err}
	var schemaName *string
	if op.SchemaScope(){name,err:=args[0].Text();if err!=nil{return value,err};schemaName=&name}
	scopes,err:=xmlMappingScopes(ctx,auth,schemaName);if err!=nil{return value,err}
	// Lock and readmit selected metadata against one coherent refreshed catalog
	// and role view. A producer/readmission error is never omission.
	for i:=range scopes{
		for j,rel:=range scopes[i].relations{
			if err:=ctx.Err();err!=nil{return value,err}
			read,current,err:=s.lockXMLMappingRelation(auth.Catalog,rel);if err!=nil{return value,err}
			fresh,err:=h.xmlMappingScopeAuthority(c);if err!=nil{return value,err}
			if fresh.Catalog!=read{return value,ids.ErrCatalogReplaced}
			if current.Namespace()!=scopes[i].namespace.Address()||!xmlMappingScopeRelation(current){return value,SQLError("XX000",fmt.Sprintf("could not open relation with OID %d",rel.Address().ObjectID))}
			ns,ok:=fresh.Catalog.Namespace(current.Namespace());if !ok{return value,SQLError("3F000",fmt.Sprintf(`schema "%s" does not exist`,scopes[i].namespace.Name()))}
			if err:=fresh.CanUse(fresh.View.Ref(ns.Address()));err!=nil{return value,err}
			allowed,err:=fresh.View.RelationPrivilege(ctx,fresh.State.CurrentUser.ID(),current,0,false,authorization.SelectPrivilege,0);if err!=nil{return value,err};if !allowed{return value,SQLError("42501","permission denied for table "+current.Name())}
			auth=fresh;scopes[i].namespace=ns;scopes[i].relations[j]=current
		}
	}
	name:=auth.Catalog.MustDatabase(catalog.CurrentDatabaseAddress).Name();if op.SchemaScope(){name=scopes[0].namespace.Name()}
	var token *xml.MappingSchemaResult
	if op.Schema(){
		var collector xmlMappingTypeCollector
		var members []string
		for _,scope:=range scopes{
			if err:=ctx.Err();err!=nil{return value,err}
			if op.DatabaseScope(){members=append(members,scope.namespace.Name())}
			for _,rel:=range scope.relations{if op.SchemaScope(){members=append(members,rel.Name())};if err:=collector.AddColumns(ctx,rel.Columns);err!=nil{return value,err}}
		}
		types,err:=collector.Finish(ctx,auth.Catalog);if err!=nil{return value,err}
		schema:=xml.MappingScopeSchema{Kind:kind,Database:auth.Catalog.MustDatabase(catalog.CurrentDatabaseAddress).Name(),Members:members,Types:types,Options:options,BinaryHex:output.XMLBinaryHex}
		if op.SchemaScope(){schema.Namespace=name}
		checked,err:=xml.ConstructScopeMappingSchema(ctx,schema);if err!=nil{return value,err};token=&checked
		if !op.Combined(){return sqltype.XMLMappingSchema(ctx,result,checked)}
	}
	leave,err:=s.enterExpressionEnvironment(auth.State.CurrentUser);if err!=nil{return value,err}
	defer func(){failure=leave(failure);if failure!=nil{value=sqltype.Value{}}}()
	// Borrow current actor-aware output formatters, retaining this invocation's
	// XML policy and notice sink rather than incidental producer settings.
	env:=c.FunctionXMLOutput();env.XMLBinaryHex,env.Zone,env.XMLNotice=output.XMLBinaryHex,output.Zone,output.XMLNotice
	mapper,err:=sqltype.NewXMLScopeMapping(ctx,result,kind,name,options,token);if err!=nil{return value,err}
	oidType,_:=auth.Catalog.LookupType(sqltype.Oid);classType,_:=auth.Catalog.LookupType(sqltype.Regclass)
	classPlan,err:=sqltype.PlanBinaryCoercion(oidType,classType);if err!=nil{return value,err}
	var names []string
	for _,scope:=range scopes{
		if err:=ctx.Err();err!=nil{return value,err}
		if op.DatabaseScope(){if err:=mapper.BeginSchema(ctx,scope.namespace.Name());err!=nil{return value,err}}
		for _,rel:=range scope.relations{
			if err:=ctx.Err();err!=nil{return value,err}
			oid,err:=sqltype.OIDValue(oidType,uint32(rel.Address().ObjectID));if err!=nil{return value,err}
			class,err:=classPlan.Apply(ctx,oid);if err!=nil{return value,err}
			spelling,err:=classType.OutputWithEnvironment(ctx,class,env);if err!=nil{return value,err};text:="SELECT * FROM "+spelling
			err=s.xmlMappingQuery(text,false,func(rows exec.RowCursor)error{
				columns:=rows.Schema();if cap(names)<len(columns){names=make([]string,len(columns))}else{names=names[:len(columns)]};defer clear(names)
				for i,col:=range columns{if i%256==0{if err:=ctx.Err();err!=nil{return err}};names[i]=col.Name}
				if err:=mapper.BeginTable(ctx,rel.Name(),names);err!=nil{return err}
				if err:=consumeXMLMappingRows(s,rows,env,text,mapper.Append);err!=nil{return err}
				return mapper.EndTable(ctx)
			});if err!=nil{return value,err}
		}
		if op.DatabaseScope(){if err:=mapper.EndSchema(ctx);err!=nil{return value,err}}
	}
	return mapper.Finish(ctx)
}
