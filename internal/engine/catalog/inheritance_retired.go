package catalog

import (
	"stuntgres/internal/engine/ids"
)

// Retired support is not a live provider edge. PostgreSQL retains its historical
// coninhcount when a parent's column disappears but this physical column stays.
// Source rank/fact identities belong to the archived lifetime, not to compacted
// attribute numbers in a later logical import. The current parent reference and
// this physical constraint's identity remain exact live authority.
type RetiredInheritanceOrigin struct {
	Parent     ids.ObjectRef
	Attribute  int32
	Column     string
	Name       string
	Constraint ids.OID
	Lifetime   uint64
	Archive    uint64
	Own        ids.OID
	Check      bool
	// Logical import retains the archived source lifetime, not fresh OID/ranks.
	Replayed bool
}

func (p InheritedConstraint) Count() int { return len(p.Origins) + len(p.Retired) }
