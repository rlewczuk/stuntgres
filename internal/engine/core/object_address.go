package core

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
	if scope.Inquiry.CommentCatalog.State() == nil {
		return ids.ObjectAddress{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return addressLookup(scope).Lookup(c.Context, values)
}

// addressLookup is the statement's object address snapshot. Namespace checks
// use the deparse authority as captured; notices go to the statement's
// operation.
func addressLookup(scope *StatementScope) regobj.AddressLookup {
	auth := scope.Inquiry.deparseAuth
	return regobj.AddressLookup{
		Catalog: scope.Inquiry.CommentCatalog,
		Roles:   scope.Inquiry.commentRoles,
		Capture: regobj.InputCapture{
			Authority: inputAuthority(auth), Path: scope.Inquiry.DeparsePath,
			Database: scope.Op.Session.DatabaseName, Token: scope.Op.Session.SessionToken, Temporary: temporaryNamespace(auth),
		},
		Namespace: func(name parser.ObjectName) (analyze.NamespaceSelection, error) {
			return LookupNamespacesSnapshot(auth, name, analyze.FunctionLookup)
		},
		Notice: scope.Op.EmitNotice,
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
	scope := ScopeOf(c.LiveHost())
	if scope == nil || scope.Inquiry.CommentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return regobj.IdentifyValues(scope.Inquiry.CommentCatalog, scope.Inquiry.commentRoles, scope.Inquiry.DeparsePath, values, result, operation, scope.Op.Session.SessionToken)
}
