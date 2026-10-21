package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// lookupIndexClass serves the analysis host, which has no stage.
func (e *Engine) lookupIndexClass(auth AuthorizationView, raw parser.ObjectName, method catalog.IndexMethod) (sqltype.IndexClass, error) {
	return index.LookupClass(ddlHost{e: e}, auth, raw, method)
}

// bindIndexKey serves partition key binding, which has no stage.
func (e *Engine) bindIndexKey(op *session.Operation, auth AuthorizationView, u sqltype.Usage, raw parser.IndexKey, method catalog.IndexMethod) (catalog.IndexKey, error) {
	return index.BindKey(ddlHost{e: e, op: op}, auth, u, raw, method)
}

// bindIndexAttributes binds without a stage; binding reads none.
func (e *Engine) bindIndexAttributes(op *session.Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *catalog.Relation, s *parser.CreateIndex) (*catalog.IndexDefinition, []catalog.Column, []int32, map[string]int, error) {
	return index.BindAttributes(ddlHost{e: e, op: op}, auth, input, parent, s)
}

var _ index.KeyHost = ddlHost{}
