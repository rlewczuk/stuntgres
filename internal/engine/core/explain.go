package core

import (
	"database/sql/driver"
	"fmt"
	"slices"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/explain"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

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

// newExplainRenderer starts a renderer over scope's statement host.
func newExplainRenderer(scope *StatementScope, auth AuthorizationView, options explain.Options) (*explain.Renderer, error) {
	return explain.NewRenderer((*statementHost)(scope), auth, options)
}

func (e *Engine) explainSchemaLocked(format string) []catalog.Column {
	id := sqltype.Text
	if format == "json" {
		id = sqltype.JSON
	}
	typ, _ := e.Catalog.Current().LookupType(id)
	return []catalog.Column{{Name: "QUERY PLAN", Usage: typ.Usage()}}
}

func (e *Engine) describeExplain(scope *StatementScope, statement *parser.Explain) (Description, error) {
	child := explainChild(statement)
	if !explainSupportedChild(child) {
		return Description{}, explain.Error("0A000", "EXPLAIN for this statement is not supported", explainChildPosition(child))
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
	var bound explain.Bound
	description, err := e.describeBoundOperationLocked(scope, child, auth, &bound)
	if err != nil {
		return Description{}, err
	}
	format, err := explain.DescribeFormat(statement)
	if err != nil {
		return Description{}, err
	}
	if format == "xml" || format == "yaml" {
		_, err := explain.ValidateOptions(statement)
		return Description{}, err
	}
	description.Results = e.explainSchemaLocked(format)
	return description, nil
}

func (e *Engine) executeExplain(scope *StatementScope, statement *parser.Explain, count int, importFrame FrameImporter) (Result, error) {
	child := explainChild(statement)
	if !explainSupportedChild(child) {
		return Result{}, explain.Error("0A000", "EXPLAIN for this statement is not supported", explainChildPosition(child))
	}
	options, optionErr := explain.Options{}, error(nil)
	if admission := scope.explainAdmission; admission != nil {
		options, optionErr = admission.options, admission.err
	} else {
		options, optionErr = explain.ValidateOptions(statement)
	}
	if optionErr == nil && options.Analyze {
		return e.executeExplainAnalyze(scope, statement, count, importFrame, options)
	}
	var planningStart time.Time
	if options.Summary {
		planningStart = time.Now()
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
	var bound explain.Bound
	description, err := e.describeBoundOperationLocked(scope, child, auth, &bound)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if optionErr != nil {
		e.Mu.Unlock()
		return Result{}, optionErr
	}
	if count != description.Parameters.Count {
		e.Mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want %d", count, description.Parameters.Count)
	}
	schema := e.explainSchemaLocked(options.Format)
	// Rendering holds the catalog ownership window, but never captures rows or
	// invokes expression providers, defaults, triggers, or fold publication.
	renderer, err := newExplainRenderer(scope, auth, options)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	captured := auth.Catalog.State()
	_, exportValue, _ := e.Foreign.Codecs()
	e.Mu.Unlock()
	scope.foreignUnlocked()
	// Import arguments before provider planning, without evaluating expressions.
	frame := make([]sqltype.Value, description.Parameters.Count)
	if importFrame != nil {
		if err := importFrame(scope.executionContext(), description.Parameters, frame, scope.Input); err != nil {
			return Result{}, err
		}
	}
	var values []driver.Value
	if exportValue != nil {
		for _, value := range frame {
			cell, err := exportValue(scope.executionContext(), value, scope.Output)
			if err != nil {
				return Result{}, err
			}
			if bytes, ok := cell.([]byte); ok {
				cell = slices.Clone(bytes)
			}
			values = append(values, cell)
		}
	}
	renderer.SetForeignParameters(exportValue, frame, values)
	e.Mu.Lock()
	if err := renderer.ForeignFence(captured); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	lines, err := renderer.Command(child, bound)
	if err == nil {
		err = renderer.ForeignFence(captured)
	}
	if err == nil {
		err = scope.RefreshRelationGrantsLocked(e)
	}
	if err == nil {
		current, authorityErr := scope.statementAuthorizationLocked(child)
		err = authorityErr
		if err == nil {
			var finalBound explain.Bound
			_, err = e.describeBoundOperationLocked(scope, child, current, &finalBound)
		}
	}
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	var summaries explain.Summaries
	if options.Summary {
		planning := time.Since(planningStart)
		summaries.Planning = &planning
	}
	rows, err := explain.Output(scope.executionContext(), schema, options, []*explain.Renderer{renderer}, lines, summaries)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	if err := renderer.ForeignFence(captured); err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	e.Mu.Unlock()
	if err := scope.beginProtocolSimplePortal(); err != nil {
		return Result{}, err
	}
	scope.CompleteUtility()
	return anonymousResult(schema, rows, 0)
}
