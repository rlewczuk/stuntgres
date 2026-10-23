package engine

import (
	"context"
	"fmt"
	"sort"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// VacuumTarget is one selected relation. It retains identity and the name it
// was selected by, not permission, shape or storage authority. Descendants and
// database-wide candidates have no name and silently skip an unavailable or
// disappeared work target.
type VacuumTarget struct {
	Ref  ids.ObjectRef
	Name string
}

// VacuumWarning is one WARNING of a VACUUM statement. Callers emit warnings
// after unlocking, in the order they were produced.
type VacuumWarning struct{ Code, Message string }

// VacuumPermissionWarning reports a relation the current user may not vacuum.
func VacuumPermissionWarning(name string) VacuumWarning {
	return VacuumWarning{"01000", fmt.Sprintf(`permission denied to vacuum "%s", skipping it`, name)}
}

// VacuumLockWarning reports a SKIP_LOCKED target whose lock was not available.
func VacuumLockWarning(name string) VacuumWarning {
	return VacuumWarning{"55P03", fmt.Sprintf(`skipping vacuum of "%s" --- lock not available`, name)}
}

// VacuumMissingWarning reports a named target dropped before its work lock.
func VacuumMissingWarning(name string) VacuumWarning {
	return VacuumWarning{"42P01", fmt.Sprintf(`skipping vacuum of "%s" --- relation no longer exists`, name)}
}

// VacuumSelection is one VACUUM statement's targets and its selection warnings,
// both in encounter order. All expansion finishes before any work.
type VacuumSelection struct {
	Targets  []VacuumTarget
	Warnings []VacuumWarning
}

// AddDatabase fills an empty selection with every heap and partitioned table of
// view's catalog, OID-ordered. Native provider catalogs are not fabricated
// physical heaps. A relation the current user may not vacuum becomes a
// warning instead, in catalog order.
func (s *VacuumSelection) AddDatabase(ctx context.Context, view access.View, env access.Environment) error {
	for _, descriptor := range view.Catalog.Descriptors() {
		if d, ok := descriptor.(*catalog.Relation); ok && (VacuumHeap(d) || d.Partition != nil) {
			if err := s.addRoot(ctx, view, env, d, ""); err != nil {
				return err
			}
		}
	}
	sort.Slice(s.Targets, func(i, j int) bool { return s.Targets[i].Ref.Address.ObjectID < s.Targets[j].Ref.Address.ObjectID })
	return nil
}

// AddRoot selects d, the relation a named target resolved to under its lock,
// and unless only its inheritance and partition descendants. ONLY on a
// partitioned table warns that it has no effect.
func (s *VacuumSelection) AddRoot(ctx context.Context, view access.View, env access.Environment, d *catalog.Relation, name string, only bool) error {
	if err := s.addRoot(ctx, view, env, d, name); err != nil {
		return err
	}
	if only && d.Partition != nil {
		s.Warnings = append(s.Warnings, VacuumWarning{"01000", fmt.Sprintf(`VACUUM ONLY of partitioned table "%s" has no effect`, d.Name())})
	}
	if only {
		return nil
	}
	// Breadth-first, direct children OID-ordered. Dedup only this root's
	// expansion, never overlapping textual roots.
	queue := vacuumChildren(view.Catalog, d.Address())
	if len(queue) == 0 {
		return nil
	}
	seen := map[ids.ObjectAddress]bool{d.Address(): true}
	// The bound grows as descendants are appended; a fixed-length range would
	// truncate this BFS.
	for head := 0; head < len(queue); head++ {
		child := queue[head]
		if seen[child.Address()] {
			continue
		}
		seen[child.Address()] = true
		s.Targets = append(s.Targets, VacuumTarget{view.Ref(child.Address()), ""})
		queue = append(queue, vacuumChildren(view.Catalog, child.Address())...)
	}
	return nil
}

// LockUnavailable records that the named target's lock was not available.
func (s *VacuumSelection) LockUnavailable(name string) {
	s.Warnings = append(s.Warnings, VacuumLockWarning(name))
}

func (s *VacuumSelection) addRoot(ctx context.Context, view access.View, env access.Environment, d *catalog.Relation, name string) error {
	allowed, err := VacuumAllowed(ctx, view, env, d)
	if err != nil {
		return err
	}
	if allowed {
		s.Targets = append(s.Targets, VacuumTarget{view.Ref(d.Address()), name})
	} else {
		s.Warnings = append(s.Warnings, VacuumPermissionWarning(d.Name()))
	}
	return nil
}

// VacuumAllowed reports whether view's current user may vacuum d: a superuser,
// a member of d's owner or, for an unshared relation, of the database owner,
// or a holder of MAINTAIN on d, read through env.
func VacuumAllowed(ctx context.Context, view access.View, env access.Environment, d *catalog.Relation) (bool, error) {
	if actor, ok := view.Roles.Find(view.State.CurrentUser); ok && actor.Superuser {
		return true, nil
	}
	principals, err := view.Roles.RoleClosure(ctx, view.State.CurrentUser, true)
	if err != nil {
		return false, err
	}
	var databaseOwner authorization.PrincipalRef
	if !catalog.SharedRelation(d) {
		if database, ok := view.Catalog.Database(view.Owner.DatabaseAddress()); ok {
			databaseOwner = database.Owner
		}
	}
	for _, principal := range principals {
		if principal == d.Owner || databaseOwner != (authorization.PrincipalRef{}) && principal == databaseOwner {
			return true, nil
		}
	}
	rights, err := view.Effective(env, view.Ref(d.Address()), false)
	return rights&authorization.MaintainPrivilege != 0, err
}

// VacuumHeap reports whether d has a physical heap VACUUM processes: an
// unpartitioned, non-foreign table or materialized view.
func VacuumHeap(d *catalog.Relation) bool {
	return d.Partition == nil && !d.VirtualView && d.Foreign == nil && (d.RelationKind == catalog.TableRelation || d.RelationKind == catalog.MaterializedViewRelation)
}

// VacuumForeignTemp reports whether d lives in another session's TEMP
// namespace; session is the current session's token.
func VacuumForeignTemp(view catalog.Read, session ids.SessionToken, d *catalog.Relation) bool {
	ns, ok := view.Namespace(d.Namespace())
	return ok && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != session
}

// VacuumWork decides, after d's SHARE UPDATE EXCLUSIVE grant and a fresh ACL
// capture, whether VACUUM processes d's heap. A relation the user may not
// vacuum and one without a heap return a warning; another session's TEMP
// relation, a partitioned parent and disabled main processing skip silently.
// None of them needs heap storage, progress or a main-work fact.
func VacuumWork(ctx context.Context, view access.View, env access.Environment, session ids.SessionToken, d *catalog.Relation, processMain bool) (bool, VacuumWarning, error) {
	allowed, err := VacuumAllowed(ctx, view, env, d)
	if err != nil {
		return false, VacuumWarning{}, err
	}
	if !allowed {
		return false, VacuumPermissionWarning(d.Name()), nil
	}
	if !VacuumHeap(d) && d.Partition == nil {
		return false, VacuumWarning{"01000", fmt.Sprintf(`skipping "%s" --- cannot vacuum non-tables or special system tables`, d.Name())}, nil
	}
	if VacuumForeignTemp(view.Catalog, session, d) || d.Partition != nil || !processMain {
		return false, VacuumWarning{}, nil
	}
	return true, VacuumWarning{}, nil
}

func vacuumChildren(view catalog.Read, parent ids.ObjectAddress) []*catalog.Relation {
	children := catalog.InheritanceChildren(view, parent)
	partitionChildren := false
	for _, descriptor := range view.Descriptors() {
		if child, ok := descriptor.(*catalog.Relation); ok && child.Membership != nil && child.Membership.Parent == parent {
			children = append(children, child)
			partitionChildren = true
		}
	}
	if partitionChildren {
		sort.Slice(children, func(i, j int) bool { return children[i].Address().ObjectID < children[j].Address().ObjectID })
	}
	return children
}

// VacuumDatabaseOID is the current database's OID from its existing descriptor,
// not an allocator guess; 0 without one.
func VacuumDatabaseOID(view catalog.Read) ids.OID {
	database, ok := view.Database(view.Owner().DatabaseAddress())
	if !ok {
		return 0
	}
	return database.Address().ObjectID
}
