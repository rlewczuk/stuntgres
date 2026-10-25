package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// AnonymousResult calls anonymousResult.
func AnonymousResult(schema []catalog.Column, rows [][]sqltype.Value, count int64) (Result, error) {
	return anonymousResult(schema, rows, count)
}

// BorrowQueryAuthority calls borrowQueryAuthority.
func BorrowQueryAuthority(s *exec.Source, scope *StatementScope) {
	borrowQueryAuthority(s, scope)
}

// InquiryCommentRoles returns x.commentRoles.
func InquiryCommentRoles(x *inquirySnapshot) authorization.Read { return x.commentRoles }

// InquiryDeparseAuth returns x.deparseAuth.
func InquiryDeparseAuth(x *inquirySnapshot) AuthorizationView { return x.deparseAuth }

// InquiryVisibilityCatalog returns x.visibilityCatalog.
func InquiryVisibilityCatalog(x *inquirySnapshot) catalog.Read { return x.visibilityCatalog }

// InquiryVisibilityOtherPath returns x.visibilityOtherPath.
func InquiryVisibilityOtherPath(x *inquirySnapshot) []ids.ObjectAddress { return x.visibilityOtherPath }

// InquiryVisibilityRelationPath returns x.visibilityRelationPath.
func InquiryVisibilityRelationPath(x *inquirySnapshot) []ids.ObjectAddress {
	return x.visibilityRelationPath
}

// SetInquiryCommentRoles sets x.commentRoles.
func SetInquiryCommentRoles(x *inquirySnapshot, v authorization.Read) { x.commentRoles = v }

// EngineCapturePreparedQuerySourcesLocked calls x.capturePreparedQuerySourcesLocked.
func EngineCapturePreparedQuerySourcesLocked(x *Engine, scope *StatementScope, prepared *querySourcePreparation, locks *exec.LockContext) (*exec.Source, error) {
	return x.capturePreparedQuerySourcesLocked(scope, prepared, locks)
}

// EngineMaterializePolicySourcesLocked calls x.materializePolicySourcesLocked.
func EngineMaterializePolicySourcesLocked(x *Engine, s *StatementScope) error {
	return x.materializePolicySourcesLocked(s)
}

// EnginePrepareQuerySourcesLocked calls x.prepareQuerySourcesLocked.
func EnginePrepareQuerySourcesLocked(x *Engine, s *StatementScope, leaves []exec.Leaf, singleton bool, invoker authorization.PrincipalRef, inherited plan.RowMark) (*querySourcePreparation, error) {
	return x.prepareQuerySourcesLocked(s, leaves, singleton, invoker, inherited)
}

// InquiryDeparseAuthRef returns &x.deparseAuth.
func InquiryDeparseAuthRef(x *inquirySnapshot) *AuthorizationView { return &x.deparseAuth }

// InquiryFields holds the inquirySnapshot fields tests set.
type InquiryFields struct {
	CommentCatalog      catalog.Read
	CommentRoles        authorization.Read
	DeparseAuth         AuthorizationView
	DeparsePath         []ids.ObjectAddress
	VisibilityCatalog   catalog.Read
	VisibilityOtherPath []ids.ObjectAddress
}

// NewInquiry builds a inquirySnapshot from f.
func NewInquiry(f InquiryFields) inquirySnapshot {
	return inquirySnapshot{CommentCatalog: f.CommentCatalog, commentRoles: f.CommentRoles, deparseAuth: f.DeparseAuth, DeparsePath: f.DeparsePath, visibilityCatalog: f.VisibilityCatalog, visibilityOtherPath: f.VisibilityOtherPath}
}

// PolicySourcePreparationClause returns x.clause.
func PolicySourcePreparationClause(x *policySourcePreparation) *exec.RLSRuntimeClause {
	return x.clause
}

// QuerySourcePreparationConsumed returns x.consumed.
func QuerySourcePreparationConsumed(x *querySourcePreparation) bool { return x.consumed }
