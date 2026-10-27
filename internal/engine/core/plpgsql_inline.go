package core

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) executeDO(s *StatementScope, raw *parser.Do, count int) (Result, error) {
	if count != 0 {
		return Result{}, SQLError("42P02", "there is no parameter $1")
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	language, err := routine.AdmitLanguage(auth.View, auth, raw.Language.Text)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	handler, ok := auth.Catalog.Get(language.Inline).(*catalog.Procedure)
	if !ok || handler.PLHandler != catalog.PLInlineHandler {
		e.Mu.Unlock()
		return Result{}, SQLError("0A000", fmt.Sprintf("language %q does not support inline code execution", language.Name()))
	}
	if err = catalog.ValidatePLHandler(auth.Catalog, handler); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	void, _ := auth.Catalog.LookupType(sqltype.Void)
	syntax, err := parser.ParsePLpgSQL(raw.Body, proc.PLReturnPolicy(void.Usage(), 0, false))
	if err != nil {
		e.Mu.Unlock()
		return Result{}, parser.PLBodyError(err, raw.BodyPos)
	}
	definition := &proc.Definition{Kind: proc.PLpgSQLBody, Source: raw.Body, Position: raw.BodyPos, Name: "inline_code_block", Result: void.Usage(), PLSyntax: syntax}
	d := &catalog.Procedure{ObjectName: "inline_code_block", Language: language.Address(), SQL: definition, Display: catalog.RoutineDisplay{Language: catalog.PLpgSQLRoutineLanguage, Source: raw.Body}, RoutineKind: catalog.FunctionRoutine}
	err = e.CompilePLRoutineLocked(s.Op, auth, d)
	read := auth.Catalog
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = s.enterSQLRoutine(); err != nil {
		return Result{}, err
	}
	defer s.leaveSQLRoutine()
	_, err = s.invokePLRoutine(d, &proc.Frame{}, read, sqltype.CollationRef{}, nil)
	return Result{}, err
}
