package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// lookupIndexClass serves the analysis host, which has no stage.
func (e *Engine) lookupIndexClass(auth AuthorizationView, raw parser.ObjectName, method catalog.IndexMethod) (sqltype.IndexClass, error) {
	return index.LookupClass(DDLHost{E: e}, auth, raw, method.OID())
}

var _ index.KeyHost = DDLHost{}
