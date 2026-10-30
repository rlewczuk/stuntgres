package core

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Data SPI parses the entire input before effects, then analyzes/executes each
// command in turn. Schema SPI instead prepares a single cursor without FETCH.
func (s *StatementScope) xmlMappingQuery(text string,schema bool,consume func(exec.RowCursor)error)(err error){
	if err:=s.enterSQLRoutine();err!=nil{return err};defer s.leaveSQLRoutine()
	read:=xmlMappingRead(s)
	settings:=s.Op.EffectiveSettings();settings.Mu.Lock();standard:=settings.Value("standard_conforming_strings")=="on";settings.Mu.Unlock()
	statements,err:=parser.ParseWithOptions(text,parser.Options{Context:s.Op.Context(),StandardConformingStrings:standard,Notice:s.Op.ParseNotice,ClosedRoutineFormals:true})
	if err!=nil{return exec.RewriteSQLDiagnostic(err,text)}
	if schema&&len(statements)!=1{
		for _,statement:=range statements{if err:=s.xmlMappingPrepare(statement,read);err!=nil{return exec.SPIStatementDiagnostic(err,text)}}
		return SQLError("42P11","cannot open multi-query plan as cursor")
	}
	if len(statements)==0{return SQLError("22000","invalid query")}
	for i,statement:=range statements{if err:=s.xmlMappingStatement(statement,read,text,schema,i==len(statements)-1,consume);err!=nil{return err}}
	return nil
}
func (s *StatementScope) xmlMappingStatement(statement parser.Statement,read catalog.Read,text string,schema,last bool,consume func(exec.RowCursor)error)(err error){
	tuple:=nestedTupleStatement(statement)
	// SPI prepares query-bearing utilities, but plain utilities remain raw
	// CMD_UTILITY plans: neither generic binding nor utility execution belongs
	// to cursor-kind admission. The preparer owns its sole nested activation.
	if schema&&!tuple{
		if err:=s.xmlMappingPrepare(statement,read);err!=nil{return exec.SPIStatementDiagnostic(err,text)}
		return SQLError("42P11","cannot open "+RoutineCommandTag(statement)+" query as cursor")
	}
	view,refs:=s.View,s.rlsDataRefs
	restore:=s.ActivateSQLRoutine(nil,read)
	defer func(){err=restore(err)}()
	s.View,s.rlsDataRefs=view,refs
	s.sqlRoutine.scalar,s.sqlRoutine.nestedCursor=false,true
	s.sqlRoutine.readOnlySPI=!schema||tuple
	if !schema {
		switch stmt:=statement.(type){
		case *parser.TransactionControl,*parser.SavepointControl:
			return SQLError("22000","invalid query")
		case *parser.Copy:
			if stmt.Endpoint==parser.CopyStdin||stmt.Endpoint==parser.CopyStdout{return SQLError("22000","invalid query")}
		}
		if err:=s.routineUtilityEligibility(statement);err!=nil{return exec.SPIStatementDiagnostic(err,text)}
	}
	if err:=s.AcquireRelation(statement,true);err!=nil{return exec.SPIStatementDiagnostic(err,text)}
	description,err:=s.Engine().describeOperation(s,statement)
	if err!=nil{return exec.SPIStatementDiagnostic(err,text)}
	s.Expected=&description
	tag:=nestedReadOnlyViolation(statement)
	if tag==""&&!tuple{tag=RoutineCommandTag(statement)}
	if tag!=""{return exec.SPIStatementDiagnostic(SQLError("0A000",tag+" is not allowed in a non-volatile function"),text)}
	execution,err:=s.Engine().executeStatement(s,statement,0,func(_ context.Context,info analyze.ParameterInfo,_ []sqltype.Value,_ sqltype.InputEnvironment)error{if info.Count!=0{return fmt.Errorf("stuntgres: XML mapping SQL parameter frame is not empty")};return nil})
	if err!=nil{return exec.SPIStatementDiagnostic(err,text)}
	defer func(){if execution.Rows!=nil{if closeErr:=execution.Rows.Close();err==nil{err=closeErr}};for _,rows:=range execution.RowSets{if closeErr:=rows.Close();err==nil{err=closeErr}}}()
	if execution.Rows==nil{return SQLError("22000","invalid query")}
	if schema||last{return consume(execution.Rows)}
	row:=make([]sqltype.Value,len(execution.Rows.Schema()));defer clear(row)
	for {present,err:=execution.Rows.Next(s.Op.Context(),row);if err!=nil{return exec.SPIStatementDiagnostic(err,text)};if !present{return nil}}
}

// The ordinary-SQL planned-statement boundary matches released
// stmt_requires_parse_analysis. Preparing never runs a utility or inherits
// runtime readonly rejection; cursor opening owns that later decision.
func (s *StatementScope) xmlMappingPrepare(statement parser.Statement,read catalog.Read)(err error){
	if !session.ProtocolPlannedStatement(statement){return nil}
	view,refs:=s.View,s.rlsDataRefs
	restore:=s.ActivateSQLRoutine(nil,read)
	defer func(){err=restore(err)}()
	s.View,s.rlsDataRefs=view,refs
	// Activation derives visibility from the original caller policy first.
	// Only nonexecuting preparation bypasses runtime readonly eligibility.
	s.sqlRoutine.scalar,s.sqlRoutine.readOnlySPI,s.sqlRoutine.policy.readOnly=false,false,false
	if err=s.AcquireRelation(statement,true);err!=nil{return err}
	_,err=s.Engine().describeOperation(s,statement)
	return err
}
