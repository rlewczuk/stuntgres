package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// ScopePgrowlocksReadmitLocked calls x.pgrowlocksReadmitLocked.
func ScopePgrowlocksReadmitLocked(x *StatementScope, e *Engine, auth AuthorizationView, name parser.ObjectName, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation) error {
	return x.pgrowlocksReadmitLocked(e, auth, name, ref, descriptor, stored)
}

// ScopeVerifyHeapamReadmitLocked calls x.verifyHeapamReadmitLocked.
func ScopeVerifyHeapamReadmitLocked(x *StatementScope, e *Engine, auth AuthorizationView, ref ids.ObjectRef, descriptor *catalog.Relation, stored *storage.Relation, sequence *storage.SequenceGenerator) error {
	return x.verifyHeapamReadmitLocked(e, auth, ref, descriptor, stored, sequence)
}
