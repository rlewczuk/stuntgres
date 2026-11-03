package xml

import (
	"context"
	"fmt"
)

// MappingScopeKind selects the bounded nesting grammar, not SQL authority.
type MappingScopeKind uint8
const (
	SchemaScope MappingScopeKind = iota+1
	DatabaseScope
)

func NewScopeMapping(ctx context.Context,kind MappingScopeKind,name string,options RowMappingOptions,schema *MappingSchemaResult)(*RowMapping,error){
	if err:=ctx.Err();err!=nil{return nil,err}
	if kind!=SchemaScope&&kind!=DatabaseScope{return nil,fmt.Errorf("invalid XML mapping scope")}
	if name==""{return nil,fmt.Errorf("empty XML mapping scope identity")}
	if schema!=nil&&(!schema.checked||!schema.result.Valid()){return nil,fmt.Errorf("invalid mapping schema result")}
	root,err:=MapName(ctx,name,true);if err!=nil{return nil,err}
	m:=&RowMapping{root:root,options:options}
	// The outer scope is a document even for a table forest. Only this start
	// owns namespace declarations and the optional schema link.
	m.start(ctx,root,schema!=nil);m.append(ctx,"\n")
	m.scope=kind
	if schema!=nil{m.append(ctx,schema.result.original,"\n\n")}
	if m.err!=nil{return nil,m.err};return m,nil
}

func (m *RowMapping) scopeStep(ctx context.Context,valid bool)error{
	if m.finished{return fmt.Errorf("finished XML row mapper")}
	if m.err!=nil{return m.err}
	if err:=ctx.Err();err!=nil{m.err=err;return err}
	if !valid{m.err=fmt.Errorf("invalid XML mapping scope transition")}
	return m.err
}

func (m *RowMapping) BeginSchema(ctx context.Context,name string)error{
	if err:=m.scopeStep(ctx,m.scope==DatabaseScope&&!m.schemaOpen&&!m.tableOpen&&name!="");err!=nil{return err}
	root,err:=MapName(ctx,name,true);if err!=nil{m.err=err;return err}
	m.schemaRoot=root;m.schemaOpen=true
	m.append(ctx,"<",root,">\n\n");return m.err
}

func (m *RowMapping) BeginTable(ctx context.Context,name string,names []string)error{
	if err:=m.scopeStep(ctx,(m.scope==SchemaScope||m.scope==DatabaseScope&&m.schemaOpen)&&!m.tableOpen&&name!="");err!=nil{return err}
	root,err:=MapName(ctx,name,true);if err!=nil{m.err=err;return err}
	if cap(m.names)<len(names){m.names=make([]string,len(names))}else{m.names=m.names[:len(names)]}
	for i,name:=range names{
		if i%256==0{if err:=ctx.Err();err!=nil{m.err=err;return err}}
		mapped,err:=MapName(ctx,name,true);if err!=nil{m.err=err;return err};m.names[i]=mapped
	}
	m.tableRoot=root;m.tableOpen=true
	if !m.options.Forest{m.append(ctx,"<",root,">\n\n")}
	return m.err
}

func (m *RowMapping) EndTable(ctx context.Context)error{
	if err:=m.scopeStep(ctx,m.scope!=0&&m.tableOpen);err!=nil{return err}
	if !m.options.Forest{m.append(ctx,"</",m.tableRoot,">\n")}
	m.append(ctx,"\n");clear(m.names);m.names=m.names[:0];m.tableRoot="";m.tableOpen=false
	return m.err
}

func (m *RowMapping) EndSchema(ctx context.Context)error{
	if err:=m.scopeStep(ctx,m.scope==DatabaseScope&&m.schemaOpen&&!m.tableOpen);err!=nil{return err}
	m.append(ctx,"</",m.schemaRoot,">\n\n");m.schemaRoot="";m.schemaOpen=false
	return m.err
}
