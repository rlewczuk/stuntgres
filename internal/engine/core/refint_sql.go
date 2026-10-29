package core

import (
 "context"
 "fmt"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

type refintQuery struct { text string; statement parser.Statement }

// Preparation is deliberately per invocation and precedes every foreign action.
// It borrows lexical scratch only: no command completion or journal publication.
func (s *StatementScope) prepareRefint(text string, inputs []sqltype.Usage) (query refintQuery, err error) {
 defer func(){ err = exec.SPIStatementDiagnostic(err,text) }()
 s.Op.EffectiveSettings().Mu.Lock()
 standard := s.Op.EffectiveSettings().Value("standard_conforming_strings") == "on"
 s.Op.EffectiveSettings().Mu.Unlock()
 statements, err := parser.ParseWithOptions(text,parser.Options{Context:s.executionContext(), StandardConformingStrings:standard, Notice:s.Op.ParseNotice, ClosedRoutineFormals:true, FormalCount:len(inputs)})
 if err != nil { return query,err }
 if len(statements) != 1 { return query,fmt.Errorf("stuntgres: refint generated SQL must contain one command") }
 query = refintQuery{text:text, statement:statements[0]}
 read := s.refintCatalog()
 restore := s.ActivateSQLRoutine(inputs,read)
 defer func(){ err = restore(err) }()
 s.sqlRoutine.scalar, s.sqlRoutine.rows, s.sqlRoutine.closedParameters = false, true, true
 if err = s.AcquireRelation(query.statement,true); err != nil { return query,err }
 _, err = s.Engine().describeOperation(s,query.statement)
 return query,err
}
func (s *StatementScope) refintCatalog() catalog.Read {
 e:=s.Engine(); e.Mu.Lock(); defer e.Mu.Unlock()
 changes:=s.Catalog; if changes==nil { changes=s.Owner.Catalog }
 return e.PrivateCatalogLocked(changes)
}

// executeRefint uses the ordinary typed binder/executor and the outer journal.
// Inner AFTER/constraint work completes before the next reference is executed.
func (s *StatementScope) executeRefint(q refintQuery, inputs []sqltype.Usage, parameters []sqltype.Value) (count int64, err error) {
 defer func(){ err=exec.SPIStatementDiagnostic(err,q.text) }()
 restore:=s.ActivateSQLRoutine(inputs,s.refintCatalog())
 defer func(){ err=restore(err) }()
 s.sqlRoutine.scalar, s.sqlRoutine.rows, s.sqlRoutine.closedParameters = false, true, true
 finish,err:=s.BeginRoutineCommand(); if err!=nil { return 0,err }
 defer func(){ err=finish(err) }()
 if err=s.AcquireRelation(q.statement,true); err!=nil { return 0,err }
 description,err:=s.Engine().describeOperation(s,q.statement); if err!=nil { return 0,err }
 s.Expected=&description
 result,err:=s.Engine().executeStatement(s,q.statement,len(parameters),func(_ context.Context, info analyze.ParameterInfo,dst []sqltype.Value,_ sqltype.InputEnvironment) error {
  if info.Count!=len(parameters) || len(dst)!=len(parameters) { return fmt.Errorf("stuntgres: invalid refint parameter frame") }; copy(dst,parameters); return nil
 })
 defer func(){
  if result.Rows!=nil { if closeErr:=result.Rows.Close(); err==nil { err=closeErr } }
  for _,rows:=range result.RowSets { if closeErr:=rows.Close(); err==nil { err=closeErr } }
 }()
 if err!=nil { return 0,err }
 if result.Rows==nil { return result.Count,nil }
 row:=make([]sqltype.Value,len(result.Rows.Schema()))
 present,err:=result.Rows.Next(s.executionContext(),row)
 if err!=nil { return 0,err }; if present { return 1,nil }; return 0,nil
}
