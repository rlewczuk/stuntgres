package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) LookupObjectAddress(c *exec.Context, values [3]sqltype.Value) (ids.ObjectAddress, error) {
	scope := h.scope()
	if scope.inquiry.commentCatalog.State() == nil {
		return ids.ObjectAddress{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return addressLookup(scope).Lookup(c.Context, values)
}

// addressLookup is the statement's object address snapshot. Namespace checks
// use the deparse authority as captured; notices go to the statement's
// operation.
func addressLookup(scope *StatementScope) regobj.AddressLookup {
	auth := scope.inquiry.deparseAuth
	return regobj.AddressLookup{
		Catalog: scope.inquiry.commentCatalog,
		Roles:   scope.inquiry.commentRoles,
		Capture: regobj.InputCapture{
			Authority: inputAuthority(auth), Path: scope.inquiry.deparsePath,
			Database: scope.op.Session.DatabaseName, Token: scope.op.Session.SessionToken, Temporary: temporaryNamespace(auth),
		},
		Namespace: func(name parser.ObjectName) (analyze.NamespaceSelection, error) {
			return lookupNamespacesSnapshot(auth, name, analyze.FunctionLookup)
		},
		Notice: scope.op.EmitNotice,
	}
}

func (h *statementHost) IdentifyObject(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return identifyObjectValues(c, values, result, functions.IdentifyObject)
}

func (h *statementHost) IdentifyObjectAddress(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return identifyObjectValues(c, values, result, functions.IdentifyObjectAddress)
}

func (h *statementHost) DescribeObject(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return identifyObjectValues(c, values, result, functions.DescribeObject)
}
func identifyObjectValues(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor, operation functions.ObjectAddressOperation) (sqltype.Value, error) {
	scope := scopeOf(c.LiveHost())
	if scope == nil || scope.inquiry.commentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return regobj.IdentifyValues(scope.inquiry.commentCatalog, scope.inquiry.commentRoles, scope.inquiry.deparsePath, values, result, operation)
}
