package exec

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ForeignHandlerOwner is borrowed only while invoking the selected handler.
// Returned method authority never retains a session, evaluator or scope.
type ForeignHandlerOwner interface {
	EnterDirectRoutine(*catalog.Procedure, ids.ObjectRef) (sqltype.RoutineBodyToken, error)
}

// ForeignHandlerSelection is immutable selection metadata, not a method table
// or an invocation receipt. Inquiries may inspect it without invoking a handler.
type ForeignHandlerSelection struct {
	Procedure *catalog.Procedure
	Reference ids.ObjectRef
}

func SelectForeignHandler(read catalog.Read, address ids.ObjectAddress) ForeignHandlerSelection {
	return ForeignHandlerSelection{Procedure: read.MustProcedure(address), Reference: read.Domain().Ref(read.Epoch(), address)}
}

func (s ForeignHandlerSelection) Methods(owner ForeignHandlerOwner) (catalog.ForeignDataWrapper, error) {
	if owner == nil || s.Procedure == nil || s.Reference.Address != s.Procedure.Address() {
		return nil, fmt.Errorf("stuntgres: missing foreign handler invocation owner")
	}
	body, err := owner.EnterDirectRoutine(s.Procedure, s.Reference)
	if err != nil {
		return nil, err
	}
	defer body.Leave()
	return ForeignHandlerMethods(s.Procedure)
}

// ForeignHandlerMethods is the native equivalent of the registered handler's
// returned FdwRoutine: its immutable registered method authority. It executes
// inside the actual handler boundary, not around any provider method callback.
func ForeignHandlerMethods(original *catalog.Procedure) (catalog.ForeignDataWrapper, error) {
	if original == nil || original.Signature != catalog.ForeignHandlerSignature || original.Foreign == nil || original.Foreign.Wrapper == nil {
		return nil, fmt.Errorf("stuntgres: invalid selected foreign handler")
	}
	return original.Foreign.Wrapper, nil
}

// InitializeHandler is called at actual planning/startup demand, never merely
// because a relation's metadata was captured. Subsequent raw methods consume
// the returned table with the caller's subject already restored.
func (f *ForeignQuerySource) InitializeHandler(owner ForeignHandlerOwner) error {
	if f.methodsReady {
		return nil
	}
	if f.Handler == nil && f.Selection.Procedure == nil {
		// Internal algorithm-only sources have no SQL handler installation.
		return nil
	}
	methods, err := f.Selection.Methods(owner)
	if err != nil {
		return err
	}
	f.Wrapper = methods
	f.methodsReady = true
	return nil
}
