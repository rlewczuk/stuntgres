package engine

import "stuntgres/internal/engine/stored"

// Temporary aliases for the stored types the engine names in many files.
// Stored 22d removes them.
type (
	pgDependRefs      = stored.DependRefs
	pgRenderer        = stored.Deparser
	pgRenderParent    = stored.DeparseParent
	policyExpression  = stored.Policy
	procedureProvider = stored.ProcedureProvider
	scalarRenderer    = stored.ScalarRenderer
	viewCheckMode     = stored.CheckMode
	viewDefinition    = stored.View
	viewLeaf          = stored.ViewLeaf
	viewPresentation  = stored.Presentation
	viewQueryRemapper = stored.ViewQueryRemapper
	viewRenderer      = stored.QueryRenderer
)
