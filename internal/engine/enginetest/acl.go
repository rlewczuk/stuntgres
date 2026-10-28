package enginetest

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
)

// SetObjectACL publishes acl on ref directly, outside GRANT and REVOKE,
// with the validation, revision and publication steps of a grant. It follows
// the root fixture Engine.SetObjectACL.
func SetObjectACL(e *core.Engine, ctx context.Context, ref ids.ObjectRef, acl authorization.ACLValue) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() {
		return fmt.Errorf("stuntgres: foreign or stale ACL object")
	}
	addr := ref.Address
	addr.SubID = 0
	d, ok := e.Catalog.State.Find(addr)
	if !ok {
		return fmt.Errorf("stuntgres: missing ACL object")
	}
	if !acl.Explicit && len(acl.Entries) != 0 {
		return fmt.Errorf("stuntgres: entries in absent ACL")
	}
	acl.Entries = append([]authorization.ACLEntry(nil), acl.Entries...)
	next, err := withObjectACL(d, ref.Address.SubID, acl)
	if err != nil {
		return err
	}
	if err := access.ValidateACL(e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), next); err != nil {
		return err
	}
	if e.RoleStore.Published.Revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	state := catalog.CloneState(e.Catalog.State)
	if parameter, ok := next.(*catalog.Parameter); ok {
		catalog.InstallParameterGrant(state, parameter)
	} else {
		state.SetDescriptor(addr, next)
	}
	roles := e.RoleStore.Published.Advance()
	sharedRoots := e.Catalog.ProjectSharedLocked(state, e.Cluster.Catalogs)
	if err := ctx.Err(); err != nil {
		return err
	}
	if n, ok := next.(*catalog.Relation); ok && n.RelationKind == catalog.SequenceRelation && ref.Address.SubID == 0 {
		stamp, err := e.Catalog.RevisionLocked()
		if err != nil {
			return err
		}
		n.SequencePlanStamp = stamp
	}
	if _, ok := next.(*catalog.Namespace); ok {
		if err := state.AdvanceNamespaceGeneration(); err != nil {
			return err
		}
	}
	e.Catalog.PublishLocked(state)
	sharedRoots.InstallLocked()
	e.RoleStore.PublishLocked(roles)
	return nil
}

// withObjectACL backs the SetObjectACL fixture. It returns a copy of d whose
// ACL is acl: the object's own ACL when subID is 0, otherwise the ACL of
// column subID of a relation (an absent acl removes it; the column map is
// copied, never written). acl is stored as given, so the caller owns its
// entries. Types reset their synthetic default.
func withObjectACL(d catalog.Descriptor, subID int32, acl authorization.ACLValue) (catalog.Descriptor, error) {
	switch d := d.(type) {
	case *catalog.Database:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: database subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Namespace:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: namespace subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Type:
		if subID != 0 || d.SQLType.IsArray() {
			return nil, fmt.Errorf("stuntgres: invalid type ACL target")
		}
		n := *d
		n.ACL = acl
		n.ACLSyntheticDefault = false
		return &n, nil
	case *catalog.Procedure:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: procedure subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Language:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: language subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Parameter:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: parameter subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Tablespace:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: tablespace subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.LargeObject:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: large object subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Relation:
		if subID < 0 || subID > 0 && !d.HasAttribute(subID) {
			return nil, fmt.Errorf("stuntgres: invalid ACL column")
		}
		n := *d
		if subID == 0 {
			n.Security.ACL = acl
		} else {
			n.Security.ColumnACL = make(map[int32]authorization.ACLValue, len(d.Security.ColumnACL)+1)
			for k, v := range d.Security.ColumnACL {
				n.Security.ColumnACL[k] = v
			}
			if acl.Explicit {
				n.Security.ColumnACL[subID] = acl
			} else {
				delete(n.Security.ColumnACL, subID)
			}
		}
		return &n, nil
	default:
		return nil, fmt.Errorf("stuntgres: unsupported ACL object")
	}
}
