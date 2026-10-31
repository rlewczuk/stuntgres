package core

import (
 "fmt"
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
 err = s.WriteHost().PrepareTriggerSQL(query.statement,inputs,s.refintCatalog())
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
 return s.WriteHost().ExecuteTriggerSQL(q.statement,inputs,s.refintCatalog(),parameters)
}
