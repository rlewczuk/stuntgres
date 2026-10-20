package engine

import "stuntgres/internal/engine/analyze"

// Temporary aliases for the binding and analysis types that moved into
// analyze (ENGINE-SPLIT steps 12d.1, 12d.2, 12d.4 and 12d.6). Step 12e removes them.
type (
	admittedMutationTarget    = analyze.MutationTarget
	analyzedGroupItem         = analyze.AnalyzedGroupItem
	analyzeHost               = analyze.Host
	argumentWindowOwner       = analyze.ArgumentWindowOwner
	binding                   = analyze.Binding
	boundMerge                = analyze.MergeBinding
	boundMergeArm             = analyze.MergeArm
	boundReturning            = analyze.Returning
	boundUpdate               = analyze.UpdateBinding
	clauseState               = analyze.ClauseState
	comparisonOperand         = analyze.ComparisonOperand
	compositeOccurrence       = analyze.CompositeOccurrence
	compositeOccurrenceKind   = analyze.CompositeOccurrenceKind
	consumerContext           = analyze.ConsumerContext
	createColumnDeclaration   = analyze.ColumnDeclaration
	createInheritanceInput    = analyze.InheritanceInput
	createLikeInput           = analyze.LikeInput
	createTableBinding        = analyze.CreateTableBinding
	createUniqueConstraint    = analyze.UniqueConstraint
	defaultOrigin             = analyze.DefaultOrigin
	deleteBinding             = analyze.DeleteBinding
	expressionBinder          = analyze.Binder
	expressionContext         = analyze.ExpressionContext
	expressionPolicy          = analyze.ExpressionPolicy
	fromSetObservation        = analyze.FromSetObservation
	functionProjectionScope   = analyze.FunctionProjectionScope
	groupingState             = analyze.GroupingState
	insertBinding             = analyze.InsertBinding
	lookupKind                = analyze.LookupKind
	namespaceSelection        = analyze.NamespaceSelection
	ParameterInfo             = analyze.ParameterInfo
	preparedCaptures          = analyze.PreparedCaptures
	preparedCompositeKey      = analyze.PreparedCompositeKey
	preparedSource            = analyze.PreparedSource
	queryColumn               = analyze.QueryColumn
	queryLockAdmission        = analyze.LockAdmission
	queryNamespace            = analyze.QueryNamespace
	queryQualifier            = analyze.QueryQualifier
	queryScopeState           = analyze.QueryScopeState
	QueryTargetComparison     = analyze.QueryTargetComparison
	QueryTargetOccurrence     = analyze.QueryTargetOccurrence
	regObjectReference        = analyze.RegObjectReference
	regtypeConstant           = analyze.RegtypeConstant
	resolverInputs            = analyze.ResolverInputs
	ruleBinding               = analyze.RuleBinding
	ruleCTEsSlot              = analyze.RuleCTEsSlot
	sqlCallable               = analyze.SQLCallable
	storedExpressionSyntaxKey = analyze.StoredExpressionSyntaxKey
	storedSyntaxState         = analyze.StoredSyntaxState
	tableLikeMask             = analyze.LikeMask
	temporalConstant          = analyze.TemporalConstant
	updateAssignmentGroup     = analyze.AssignmentGroup
	viewCapture               = analyze.ViewCapture
	viewColumn                = analyze.ViewColumn
	viewLiteralClause         = analyze.ViewLiteralClause
	viewLiteralPath           = analyze.ViewLiteralPath
	viewNamespace             = analyze.ViewNamespace
	viewNamespaceColumn       = analyze.ViewNamespaceColumn
	viewNamespaceQualifier    = analyze.ViewNamespaceQualifier
	viewWriteSlot             = analyze.ViewWriteSlot
)
