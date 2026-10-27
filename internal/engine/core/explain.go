package core

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Bound graphs are borrowed only by this Describe/Execute activation. They are
// deliberately not part of Description, a portal, or the prepared fold owner.
type explainBound struct {
	Binding analyze.Binding
	Rules   *view.RuleProgram
}

func explainChild(statement *parser.Explain) parser.Statement {
	return statement.Statement
}

func explainChildPosition(statement parser.Statement) parser.Position {
	switch child := statement.(type) {
	case *parser.CreateQueryTable:
		return child.Pos
	case *parser.DeclareCursor:
		return child.Pos
	case *parser.ExecuteSQL:
		return child.Pos
	}
	return parser.Position{}
}

func explainSupportedChild(statement parser.Statement) bool {
	switch statement.(type) {
	case *parser.Select, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		return true
	}
	return false
}

func (e *Engine) explainSchemaLocked() []catalog.Column {
	typ, _ := e.Catalog.Current().LookupType(sqltype.Text)
	return []catalog.Column{{Name: "QUERY PLAN", Usage: typ.Usage()}}
}

func (e *Engine) describeExplain(scope *StatementScope, statement *parser.Explain) (Description, error) {
	child := explainChild(statement)
	if !explainSupportedChild(child) {
		return Description{}, explainError("0A000", "EXPLAIN for this statement is not supported", explainChildPosition(child))
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := scope.RefreshRelationGrantsLocked(e); err != nil {
		return Description{}, err
	}
	auth, err := scope.statementAuthorizationLocked(child)
	if err != nil {
		return Description{}, err
	}
	var bound explainBound
	description, err := e.describeBoundOperationLocked(scope, child, auth, &bound)
	if err != nil {
		return Description{}, err
	}
	format, err := explainDescribeFormat(statement)
	if err != nil {
		return Description{}, err
	}
	if format == "json" || format == "xml" || format == "yaml" {
		_, err := validateExplainOptions(statement)
		return Description{}, err
	}
	description.Results = e.explainSchemaLocked()
	return description, nil
}

func (e *Engine) executeExplain(scope *StatementScope, statement *parser.Explain, count int, importFrame FrameImporter) (Result, error) {
	child := explainChild(statement)
	if !explainSupportedChild(child) {
		return Result{}, explainError("0A000", "EXPLAIN for this statement is not supported", explainChildPosition(child))
	}
	e.Mu.Lock()
	if err := scope.RefreshRelationGrantsLocked(e); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	auth, err := scope.statementAuthorizationLocked(child)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	var bound explainBound
	description, err := e.describeBoundOperationLocked(scope, child, auth, &bound)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	options, err := validateExplainOptions(statement)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if count != description.Parameters.Count {
		e.Mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", count, description.Parameters.Count)
	}
	schema := e.explainSchemaLocked()
	// Rendering holds the catalog ownership window, but never captures rows or
	// invokes expression providers, defaults, triggers, or fold publication.
	renderer, err := newExplainRenderer(scope, auth, options)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	lines, err := renderer.command(child, bound)
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	// Keep the public argument-count/type boundary without using values to fold
	// or estimate the plan. No typed parameter frame escapes this activation.
	frame := make([]sqltype.Value, description.Parameters.Count)
	if importFrame != nil {
		if err := importFrame(scope.executionContext(), description.Parameters, frame, scope.Input); err != nil {
			return Result{}, err
		}
	}
	rows := make([][]sqltype.Value, len(lines))
	for i, line := range lines {
		rows[i] = []sqltype.Value{sqltype.String(schema[0].Usage.Type, line)}
	}
	if err := scope.beginProtocolSimplePortal(); err != nil {
		return Result{}, err
	}
	scope.CompleteUtility()
	return anonymousResult(schema, rows, 0)
}
