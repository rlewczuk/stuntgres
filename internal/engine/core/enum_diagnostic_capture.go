package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// CaptureTypeDiagnosticName freezes type naming against the catalog and the
// reference path used for binding (catalog.TypeDiagnosticNamer).
func CaptureTypeDiagnosticName(auth AuthorizationView) (func(sqltype.ID) string, error) {
	path, err := ReferencePath(auth)
	if err != nil {
		return nil, err
	}
	return catalog.TypeDiagnosticNamer(auth.Catalog, path), nil
}

// Safety belongs to admission, not discovery of an enum node. Scope inputs and
// standalone binders borrow the same immutable root; candidate mutations clone
// it. Inputs are never retained across admissions, so an existing callback is
// already the current scope's capture and can be shared without another closure.
func CaptureEnumOperationSafety(op *session.Operation, input *sqltype.InputEnvironment) {
	if input.EnumMemberSafe != nil || op == nil || op.Session == nil || op.Session.Transactions == nil || op.Session.Transactions.Catalog == nil {
		return
	}
	input.EnumMemberSafe = typedef.EnumSafety(typedef.EnumJournalOf(op.Session.Transactions.Catalog.Enum).UnsafeMembers())
}

// Capture naming only after the caller supplies its coherent catalog authority.
// Alternate staged binders retain lazy naming through captureTypeDiagnosticInput.
func captureEnumOperationInput(auth AuthorizationView, input *sqltype.InputEnvironment) error {
	if input.EnumMemberSafe == nil || input.TypeDiagnosticName != nil {
		return nil
	}
	var err error
	input.TypeDiagnosticName, err = CaptureTypeDiagnosticName(auth)
	return err
}

func (e *Engine) CaptureTypeDiagnosticInput(auth AuthorizationView, op *session.Operation, input *sqltype.InputEnvironment, typ *sqltype.Descriptor) error {
	if sqltype.IsFTS(typ.BaseType().ID()) && input.FTS == nil {
		input.FTS = session.FTSInput{Op: op}
	}
	if typ.ContainsACLItem() && input.ACLItem == nil {
		input.ACLItem = e.CaptureACLItemInput(auth, op)
	}
	if input.TypeDiagnosticName == nil && analyze.NeedsTypeDiagnosticInput(typ) {
		var err error
		input.TypeDiagnosticName, err = CaptureTypeDiagnosticName(auth)
		return err
	}
	return nil
}
