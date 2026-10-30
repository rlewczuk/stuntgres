package xml

import (
	"context"
	"fmt"
	"strings"
)

// RowMappingOptions contains SQL identity and switches, never SQL authority.
type RowMappingOptions struct {
	TableName string
	Relation bool
	Nulls, Forest bool
	TargetNamespace string
}

// MappingField characters come exclusively from typed SQL/XML value output.
type MappingField struct { Null bool; Characters string }

// RowMapping is an invocation-local, noncopyable checked character producer.
// Its only published state is the immutable Result returned by Finish.
type RowMapping struct {
	buffer strings.Builder
	names []string
	root string
	options RowMappingOptions
	err error
	finished bool
}

func NewRowMapping(ctx context.Context, options RowMappingOptions, names []string, schema *MappingSchemaResult) (*RowMapping,error) {
	if err:=ctx.Err();err!=nil{return nil,err}
	if options.Relation&&options.TableName==""{return nil,fmt.Errorf("empty mapping relation identity")}
	m:=&RowMapping{options:options,names:make([]string,len(names)),root:"table"}
	if options.Relation { name,err:=MapName(ctx,options.TableName,true);if err!=nil{return nil,err};m.root=name }
	for i,name:=range names{if i%256==0{if err:=ctx.Err();err!=nil{return nil,err}};mapped,err:=MapName(ctx,name,true);if err!=nil{return nil,err};m.names[i]=mapped}
	if schema!=nil&&!schema.result.Valid(){return nil,fmt.Errorf("invalid mapping schema result")}
	if !options.Forest { m.start(ctx,m.root,schema!=nil);m.append(ctx,"\n") }
	if schema!=nil{m.append(ctx,schema.result.original,"\n\n")}
	if m.err!=nil{return nil,m.err};return m,nil
}

func (m *RowMapping) append(ctx context.Context, parts ...string) {
	if m.err!=nil{return}
	p:=parser{ctx:ctx}
	for _,part:=range parts{
		if !p.ensureBuilder(&m.buffer,len(part)){m.err=p.err;return}
		for len(part)>0{if err:=ctx.Err();err!=nil{m.err=err;return};n:=min(4096,len(part));m.buffer.WriteString(part[:n]);part=part[n:]}
	}
	m.err=ctx.Err()
}

func (m *RowMapping) start(ctx context.Context, root string, schema bool) {
	m.append(ctx,"<",root)
	if m.options.Forest||root==m.root {
		m.append(ctx,` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`)
		if m.options.TargetNamespace!=""{m.append(ctx,` xmlns="`,m.options.TargetNamespace,`"`)}
	}
	if schema{if m.options.TargetNamespace==""{m.append(ctx,` xsi:noNamespaceSchemaLocation="#"`)}else{m.append(ctx,` xsi:schemaLocation="`,m.options.TargetNamespace,` #"`)}}
	m.append(ctx,">\n")
}

func (m *RowMapping) AppendRow(ctx context.Context, fields []MappingField) error {
	if m.finished{return fmt.Errorf("finished XML row mapper")}
	if m.err!=nil{return m.err}
	if len(fields)!=len(m.names){m.err=fmt.Errorf("XML mapping row width mismatch");return m.err}
	root:="row"
	if m.options.Forest { if m.options.Relation{root=m.root};m.start(ctx,root,false) } else { m.append(ctx,"<row>\n") }
	for i,field:=range fields{
		if i%256==0{if err:=ctx.Err();err!=nil{m.err=err;return err}}
		if field.Null{if m.options.Nulls{m.append(ctx,"  <",m.names[i],` xsi:nil="true"/>`,"\n")};continue}
		m.append(ctx,"  <",m.names[i],">",field.Characters,"</",m.names[i],">\n")
	}
	m.append(ctx,"</",root,">\n\n")
	return m.err
}

func (m *RowMapping) Finish(ctx context.Context) (Result,error) {
	if m.finished{return Result{},fmt.Errorf("finished XML row mapper")}
	m.finished=true
	if !m.options.Forest{m.append(ctx,"</",m.root,">\n")}
	if m.err==nil{m.err=ctx.Err()}
	m.names=nil;m.options=RowMappingOptions{};m.root=""
	if m.err!=nil{m.buffer.Reset();return Result{},m.err}
	text:=m.buffer.String();m.buffer.Reset()
	return Result{original:text,requested:Content,effective:Content,valid:true},nil
}
