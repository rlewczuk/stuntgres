package engine

import "stuntgres/internal/engine/analyze"

// Temporary aliases for the binding and analysis types that moved into
// analyze (ENGINE-SPLIT steps 12d.1, 12d.2 and 12d.4). Step 12e removes them.
type (
	admittedMutationTarget  = analyze.MutationTarget
	binding                 = analyze.Binding
	boundMerge              = analyze.MergeBinding
	boundMergeArm           = analyze.MergeArm
	boundReturning          = analyze.Returning
	boundUpdate             = analyze.UpdateBinding
	compositeOccurrence     = analyze.CompositeOccurrence
	compositeOccurrenceKind = analyze.CompositeOccurrenceKind
	createColumnDeclaration = analyze.ColumnDeclaration
	createInheritanceInput  = analyze.InheritanceInput
	createLikeInput         = analyze.LikeInput
	createTableBinding      = analyze.CreateTableBinding
	createUniqueConstraint  = analyze.UniqueConstraint
	defaultOrigin           = analyze.DefaultOrigin
	deleteBinding           = analyze.DeleteBinding
	insertBinding           = analyze.InsertBinding
	lookupKind              = analyze.LookupKind
	namespaceSelection      = analyze.NamespaceSelection
	ParameterInfo           = analyze.ParameterInfo
	preparedCaptures        = analyze.PreparedCaptures
	preparedCompositeKey    = analyze.PreparedCompositeKey
	preparedSource          = analyze.PreparedSource
	queryLockAdmission      = analyze.LockAdmission
	regObjectReference      = analyze.RegObjectReference
	regtypeConstant         = analyze.RegtypeConstant
	resolverInputs          = analyze.ResolverInputs
	ruleBinding             = analyze.RuleBinding
	ruleCTEsSlot            = analyze.RuleCTEsSlot
	tableLikeMask           = analyze.LikeMask
	temporalConstant        = analyze.TemporalConstant
	updateAssignmentGroup   = analyze.AssignmentGroup
	viewCapture             = analyze.ViewCapture
	viewColumn              = analyze.ViewColumn
	viewLiteralClause       = analyze.ViewLiteralClause
	viewLiteralPath         = analyze.ViewLiteralPath
	viewNamespace           = analyze.ViewNamespace
	viewNamespaceColumn     = analyze.ViewNamespaceColumn
	viewNamespaceQualifier  = analyze.ViewNamespaceQualifier
	viewWriteSlot           = analyze.ViewWriteSlot
)
