package core

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/xml"
)

func (h *statementHost) xmlCursorMapping(op functions.XMLMappingOperation,name string,count int64,options xml.RowMappingOptions,result *sqltype.Descriptor,output sqltype.OutputEnvironment,read catalog.Read)(value sqltype.Value,err error){
	s:=h.scope();ctx:=s.Op.Context();schemaOnly:=op==functions.XMLCursorSchema
	defer func(){if err!=nil{value=sqltype.Value{}}}()
	var columns []catalog.Column
	cursor:=PayloadOf(s.Op.Session).Cursors.Entries[name]
	var portal *protocolPortal
	if cursor!=nil{
		// Descriptor lookup accepts a failed producer, but not a retired identity.
		if err:=s.validateCursorIdentity(cursor,name);err!=nil{return value,err}
		columns=cursor.schema
	}else{
		portal=protocolPortalOf(PayloadOf(s.Op.Session).PreparedSQL.Binding(name))
		if portal==nil||portal.Retired||portal.Transaction!=s.TxRef{return value,missingSQLCursor(name)}
		if !session.ProtocolHasRows(portal.Entry.Body,portal.columns){return value,SQLError("24000",fmt.Sprintf("portal \"%s\" does not return tuples",name))}
		columns=portal.columns
	}
	if schemaOnly{schema,err:=xmlMappingSchema(ctx,read,columns,options,nil,output.XMLBinaryHex);if err!=nil{return value,err};token,err:=xml.ConstructMappingSchema(ctx,schema);if err!=nil{return value,err};return sqltype.XMLMappingSchema(ctx,result,token)}
	restore:=s.ActivateSQLRoutine(nil,read);defer func(){err=restore(err)}()
	if cursor!=nil{
		motion:=&parser.FetchCursor{Name:parser.Identifier{Text:name},Direction:parser.CursorForward,Count:count}
		batch,err:=s.fetchCursorPortal(cursor,motion);if err!=nil{return value,err}
		if batch.Rows==nil{return value,fmt.Errorf("XML cursor mapping lacks typed rows")}
		defer func(){if closeErr:=batch.Rows.Close();err==nil{err=closeErr}}()
		return consumeXMLMapping(s,batch.Rows,options,nil,result,output,"")
	}
	if portal.running||portal.state==protocolPortalFailed||portal.state==protocolPortalDone{return value,SQLError("55000",fmt.Sprintf("portal \"%s\" cannot be run",name))}
	motionComplete:=false
	portal.running=true;defer func(){portal.running=false;if err!=nil&&!motionComplete{portal.state=protocolPortalFailed;_ = portal.releaseExecutor(ctx)}}()
	if _,_,err=s.startProtocolPortal(portal,nil,nil,CopyProtocolHooks{});err!=nil{return value,err}
	if portal.producer!=nil{
		c:=portal.producer;s.enterProtocolProducer(c);defer s.leaveProtocolProducer(c)
		if err=s.authorizeSQLCursor(c);err!=nil{return value,err}
		rows:=exec.NewFinalRows(columns,0);rows.Output=c.state.Output
		motion:=&parser.FetchCursor{Name:parser.Identifier{Text:name},Direction:parser.CursorForward,Count:count}
		if _,err=c.Move(motion,rows,c.motionInput(s));err!=nil{return value,err}
		if err=c.foreignOwner.Quiesce(ctx);err!=nil{return value,err}
		if err=s.captureSQLCursorOutput(rows,c.catalog);err!=nil{return value,err}
		motionComplete,portal.running=true,false
		defer func(){if closeErr:=rows.Close();err==nil{err=closeErr}}()
		if c.AtEnd{portal.state=protocolPortalExhausted}
		return consumeXMLMapping(s,rows,options,nil,result,output,"")
	}
	// Eager tuple utilities already own a typed RowResult. Their ordinary wire
	// pull and this bounded borrow share the portal's sole fallback position.
	if portal.rows==nil{return value,SQLError("24000",fmt.Sprintf("portal \"%s\" does not return tuples",name))}
	if count<0||count==0&&portal.rowPosition.Place==exec.CursorOn{
		failure:=SQLError("55000","cursor can only scan forward")
		failure.(*sqlstate.Error).Hint="Declare it with SCROLL option to enable backward scan."
		return value,failure
	}
	rows:=&xmlProtocolRows{portal:portal,remaining:count,output:portal.rows.OutputEnvironment()}
	motionComplete,portal.running=true,false
	return consumeXMLMapping(s,rows,options,nil,result,output,"")
}

// xmlProtocolRows limits demand, not identity or position. Close must not close
// the borrowed portal, which still belongs to its actual session registry.
type xmlProtocolRows struct { portal *protocolPortal; remaining int64; output sqltype.OutputEnvironment }
func (r *xmlProtocolRows) Schema()[]catalog.Column{return r.portal.columns}
func (r *xmlProtocolRows) Next(ctx context.Context,row []sqltype.Value)(bool,error){
	if r.remaining<=0{return false,nil}
	r.portal.running=true
	present,err:=r.portal.next(ctx,row)
	r.portal.running=false
	if err!=nil{r.portal.state=protocolPortalFailed;_ = r.portal.releaseExecutor(ctx);return false,err}
	if present{r.remaining--}else{r.portal.state=protocolPortalExhausted}
	return present,nil
}
func (*xmlProtocolRows) Close()error{return nil}
func (r *xmlProtocolRows) OutputEnvironment()sqltype.OutputEnvironment{return r.output}
func (r *xmlProtocolRows) RegclassText(v sqltype.Value)(string,error){return r.portal.rows.RegclassText(v)}
