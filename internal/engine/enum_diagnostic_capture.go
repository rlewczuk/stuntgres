package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// captureTypeDiagnosticName freezes input and demanded-comparison naming against
// the catalog and reference path used for binding. Providers consult it on failure.
func captureTypeDiagnosticName(auth AuthorizationView) (func(sqltype.ID) string, error) {
	path, err := referencePath(auth)
	if err != nil {
		return nil, err
	}
	catalogView := auth.Catalog
	return func(id sqltype.ID) string {
		descriptor, ok := catalogView.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
		if !ok {
			return ""
		}
		if descriptor.SQLType.NamespaceName() == "pg_catalog" {
			// Released diagnostics use SQL aliases, not catalog spellings such
			// as int4, and do not identifier-quote builtin names such as json.
			return descriptor.SQLType.DiagnosticName()
		}
		return catalog.TypeDiagnosticNameAt(catalogView, descriptor, path)
	}, nil
}

// Safety belongs to admission, not discovery of an enum node. Scope inputs and
// standalone binders borrow the same immutable root; candidate mutations clone
// it. Inputs are never retained across admissions, so an existing callback is
// already the current scope's capture and can be shared without another closure.
func captureEnumOperationSafety(op *session.Operation, input *sqltype.InputEnvironment) {
	if input.EnumMemberSafe != nil || op == nil || op.Session == nil || op.Session.Transactions == nil || op.Session.Transactions.Catalog == nil {
		return
	}
	input.EnumMemberSafe = typedef.EnumSafety(typedef.EnumJournalOf(op.Session.Transactions.Catalog.Enum).UnsafeMembers())
}

// Capture naming only after the caller supplies its coherent catalog authority.
// Alternate staged binders retain lazy naming through captureTypeDiagnosticInput.
func (e *Engine) captureEnumOperationInput(auth AuthorizationView, input *sqltype.InputEnvironment) error {
	if input.EnumMemberSafe == nil || input.TypeDiagnosticName != nil {
		return nil
	}
	var err error
	input.TypeDiagnosticName, err = captureTypeDiagnosticName(auth)
	return err
}

func (e *Engine) captureTypeDiagnosticInput(auth AuthorizationView, op *session.Operation, input *sqltype.InputEnvironment, typ *sqltype.Descriptor) error {
	if sqltype.IsFTS(typ.BaseType().ID()) && input.FTS == nil {
		input.FTS = capturedFTSInput{op: op}
	}
	if typ.ContainsACLItem() && input.ACLItem == nil {
		input.ACLItem = e.captureACLItemInput(auth, op)
	}
	if input.TypeDiagnosticName == nil && analyze.NeedsTypeDiagnosticInput(typ) {
		var err error
		input.TypeDiagnosticName, err = captureTypeDiagnosticName(auth)
		return err
	}
	return nil
}
