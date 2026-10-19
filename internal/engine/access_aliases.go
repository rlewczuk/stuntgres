package engine

import "stuntgres/internal/engine/access"

// Temporary aliases for the declarations moved to the access package in
// ENGINE-SPLIT B3. B4 replaces their uses with the access names.
type (
	accessView         = access.View
	accessEnvironment  = access.Environment
	AccessRequirement  = access.Requirement
	AuthorizationStamp = access.Stamp
	roleCatalog        = access.RoleRecord
	rlsDisposition     = access.RLSDisposition
)

const (
	rlsUnrestricted     = access.RLSUnrestricted
	rlsDefaultDeny      = access.RLSDefaultDeny
	rlsNeedsExpressions = access.RLSNeedsExpressions
)
