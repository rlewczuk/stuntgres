package core

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core/tsearch"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

type refintQuery struct { text string; statement parser.Statement }

// Preparation is deliberately per invocation and precedes every foreign action.
// It borrows lexical scratch only: no command completion or journal publication.
func prepareRefint[A tsearch.Authority[A]](h Host[A], text string, inputs []sqltype.Usage) (query refintQuery, err error) {
 defer func(){ err = exec.SPIStatementDiagnostic(err,text) }()
 op := h.Operation()
 op.EffectiveSettings().Mu.Lock()
 standard := op.EffectiveSettings().Value("standard_conforming_strings") == "on"
 op.EffectiveSettings().Mu.Unlock()
 statements, err := parser.ParseWithOptions(text,parser.Options{Context:h.Context(), StandardConformingStrings:standard, Notice:op.ParseNotice, ClosedRoutineFormals:true, FormalCount:len(inputs)})
 if err != nil { return query,err }
 if len(statements) != 1 { return query,fmt.Errorf("stuntgres: refint generated SQL must contain one command") }
 query = refintQuery{text:text, statement:statements[0]}
 err = h.PrepareTriggerSQL(query.statement,inputs,refintCatalog(h))
 return query,err
}
func refintCatalog[A tsearch.Authority[A]](h Host[A]) catalog.Read {
 h.ReacquireEngine(); defer h.ReleaseEngineLocked()
 return h.PrivateCatalogLocked(h.PendingCatalog())
}

// executeRefint uses the ordinary typed binder/executor and the outer journal.
// Inner AFTER/constraint work completes before the next reference is executed.
func executeRefint[A tsearch.Authority[A]](h Host[A], q refintQuery, inputs []sqltype.Usage, parameters []sqltype.Value) (count int64, err error) {
 defer func(){ err=exec.SPIStatementDiagnostic(err,q.text) }()
 return h.ExecuteTriggerSQL(q.statement,inputs,refintCatalog(h),parameters)
}
