package access

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

// RoleRecord is an immutable engine-owned publication stamp. All principal,
// descriptor, name and edge storage belongs to its embedded child root.
type RoleRecord struct {
	Root     authorization.Root
	Revision uint64
	Settings *RoleSettings
	// origin changes only when principal authority changes. ACL and settings
	// publications retain the original immutable graph lineage in constant time.
	origin *RoleRecord
}

// Advance returns a record with the same root and the next revision. Callers
// check revision exhaustion first.
func (r *RoleRecord) Advance() *RoleRecord {
	return &RoleRecord{Root: r.Root, Settings: r.Settings, Revision: r.Revision + 1, origin: r.authorityOrigin()}
}

// WithRoot replaces graph authority while retaining the same settings root.
func (r *RoleRecord) WithRoot(next authorization.Root) *RoleRecord {
	return &RoleRecord{Root: next, Settings: r.Settings, Revision: r.Revision + 1}
}

// WithSettings retains graph lineage, not complete-record equality.
func (r *RoleRecord) WithSettings(next *RoleSettings) *RoleRecord {
	return &RoleRecord{Root: r.Root, Settings: next, Revision: r.Revision + 1, origin: r.authorityOrigin()}
}

func (r *RoleRecord) authorityOrigin() *RoleRecord {
	if r == nil || r.origin == nil {
		return r
	}
	return r.origin
}

// SameAuthority distinguishes principal changes from ACL/settings publications.
// Exact publication stamps still compare records/revisions, not this lineage.
func (r *RoleRecord) SameAuthority(other *RoleRecord) bool {
	return r.authorityOrigin() == other.authorityOrigin()
}

// RoleRead returns the role view of root as seen by view's current database
// owner. It is empty unless view belongs to owner and root belongs to the
// principals domain.
func RoleRead(owner *catalog.Owner, principals *authorization.Domain, view catalog.Read, root *RoleRecord) authorization.Read {
	if owner == nil || view.Owner() != owner || view.State() == nil || root == nil {
		return authorization.Read{}
	}
	database, ok := view.Database(owner.DatabaseAddress())
	if !ok {
		return authorization.Read{}
	}
	read := root.Root.Read(database.Owner)
	if !read.BelongsTo(principals) {
		return authorization.Read{}
	}
	return read
}
