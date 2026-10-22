package index

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// The grant plan of DROP INDEX, ALTER INDEX and REINDEX. The engine resolves
// names under Engine.mu and calls these steps in order: MaintenanceNames
// before the lock, then AddTarget per name, CollectGroup, AddAttach for
// ALTER INDEX ATTACH PARTITION, and FinishPlan. After releasing the lock it
// sorts the plan (SortLockOrder) and acquires ParentMode on the parents,
// ACCESS EXCLUSIVE on the indexes and AlterTarget's mode last.

// MaintenanceNames returns the relation names stmt targets, whether missing
// ones are skipped and whether it drops. REINDEX options and targets are
// checked here, before any lock.
func MaintenanceNames(stmt parser.Statement) (names []parser.ObjectName, ifExists, drop bool, err error) {
	switch v := stmt.(type) {
	case *parser.DropIndex:
		names = v.Names
		ifExists = v.IfExists
		drop = true
	case *parser.AlterIndex:
		names = []parser.ObjectName{v.Name}
		ifExists = v.IfExists
	case *parser.Reindex:
		if err := ValidateReindexOptions(v); err != nil {
			return nil, false, false, err
		}
		if v.Kind.Text != "index" && v.Kind.Text != "table" {
			return nil, false, false, ReindexControlError(v)
		}
		names = []parser.ObjectName{v.Name}
	}
	return names, ifExists, drop, nil
}

// AddTarget records the resolution of name: ref, or err when resolution
// failed. missing reports that err is a missing relation or schema, which
// IF EXISTS turns into a notice. A target already in the plan is skipped.
func (g *MaintenanceGrant) AddTarget(view catalog.Read, name parser.ObjectName, ref ids.ObjectRef, err error, missing, drop, ifExists bool) error {
	if err != nil {
		if !missing {
			return err
		}
		kind, label := "relation", name.Parts[len(name.Parts)-1].Text
		diagnostic := err.(*sqlstate.Error)
		if diagnostic.Code == "3F000" {
			kind, label = "schema", name.Parts[0].Text
		} else if drop {
			kind = "index"
			err = sqlError("42704", fmt.Sprintf(`index "%s" does not exist`, label))
		}
		if !ifExists {
			return err
		}
		g.Notices = append(g.Notices, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`%s "%s" does not exist, skipping`, kind, label)})
		return nil
	}
	if g.Descriptors[ref.Address] != nil {
		return nil
	}
	g.Targets = append(g.Targets, ref)
	g.Descriptors[ref.Address] = view.MustRelation(ref.Address)
	return nil
}

// CollectGroup checks the whole requested group, then collects the parents
// and indexes the statement locks. Wrong-kind targets fail the check and so
// never reach an index dereference.
func (g *MaintenanceGrant) CollectGroup(view access.View, env access.Environment, current catalog.Read, stmt parser.Statement) error {
	if err := g.Check(view, env, stmt, false); err != nil {
		return err
	}
	for _, ref := range g.Targets {
		d := g.Descriptors[ref.Address]
		switch v := stmt.(type) {
		case *parser.DropIndex:
			g.Indexes = append(g.Indexes, ref)
			parent := d.SQLIndex.Parent
			if g.Descriptors[parent] == nil {
				g.Parents = append(g.Parents, ref.Domain().Ref(ref.Epoch, parent))
				g.Descriptors[parent] = current.MustRelation(parent)
			}
		case *parser.Reindex:
			parent := d.Address()
			if v.Kind.Text == "index" {
				parent = d.SQLIndex.Parent
				g.Indexes = append(g.Indexes, ref)
			}
			g.Parents = append(g.Parents, ref.Domain().Ref(ref.Epoch, parent))
			g.Descriptors[parent] = current.MustRelation(parent)
			if v.Kind.Text == "table" {
				g.IndexSets = map[ids.ObjectAddress]int{parent: 0}
				for address, descriptor := range current.Descriptors() {
					index, ok := descriptor.(*catalog.Relation)
					if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent {
						continue
					}
					g.Indexes = append(g.Indexes, ref.Domain().Ref(ref.Epoch, address))
					g.Descriptors[address] = index
					g.IndexSets[parent]++
				}
			}
		}
	}
	return nil
}

// NeedsAttach reports whether stmt is ALTER INDEX ATTACH PARTITION with a
// target, whose partition the engine then resolves for AddAttach.
func (g *MaintenanceGrant) NeedsAttach(stmt parser.Statement) (*parser.AlterIndex, bool) {
	v, ok := stmt.(*parser.AlterIndex)
	return v, ok && v.Action == "attach" && len(g.Targets) > 0
}

// AddAttach adds the resolved partition index ref of ATTACH PARTITION and
// checks that it can attach to the target.
func (g *MaintenanceGrant) AddAttach(ctx context.Context, current catalog.Read, ref ids.ObjectRef) error {
	child := current.MustRelation(ref.Address)
	if child.SQLIndex == nil {
		diagnostic := WrongKind(child, false).(*sqlstate.Error)
		diagnostic.Code = "42P17"
		return diagnostic
	}
	g.Attach = ref
	g.Descriptors[ref.Address] = child
	g.Indexes = append(g.Indexes, ref)
	parent := g.Descriptors[g.Targets[0].Address]
	return PartitionIndexAttachCheck(ctx, current, parent, child)
}

// FinishPlan collects the partitioned-index members and rejects the
// unsupported forms of the statement.
func (g *MaintenanceGrant) FinishPlan(view access.View, stmt parser.Statement) error {
	if err := g.CollectPartitionIndexes(view, stmt); err != nil {
		return err
	}
	if len(g.Targets) == 0 {
		return nil
	}
	switch v := stmt.(type) {
	case *parser.DropIndex:
		if v.Concurrent.Character > 0 {
			return Capability("concurrent index operations are unsupported", v.Concurrent)
		}
	case *parser.AlterIndex:
		switch v.Action {
		case "set", "reset":
			return ValidateIndexOptions(v, g.Descriptors[g.Targets[0].Address].SQLIndex.Method)
		case "tablespace":
			return Capability("index tablespaces are unsupported", v.ActionPos)
		case "statistics":
			return Capability("index statistics are unsupported", v.ActionPos)
		}
	case *parser.Reindex:
		return ReindexControlError(v)
	}
	return nil
}

// SortLockOrder orders the parents and indexes by object ID, the order they
// are locked in.
func (g *MaintenanceGrant) SortLockOrder() {
	slices.SortFunc(g.Parents, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	slices.SortFunc(g.Indexes, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
}

// ParentMode is the lock mode of the parents: ACCESS EXCLUSIVE to drop or
// attach, SHARE otherwise.
func (g *MaintenanceGrant) ParentMode(drop bool) lock.Mode {
	if drop || g.Attach != (ids.ObjectRef{}) {
		return lock.AccessExclusive
	}
	return lock.Share
}

// AlterTarget returns the ALTER INDEX target and its lock mode: SHARE UPDATE
// EXCLUSIVE for a plain index, ACCESS EXCLUSIVE for a partitioned one. ok is
// false for other statements or when no target was found.
func (g *MaintenanceGrant) AlterTarget(stmt parser.Statement) (ref ids.ObjectRef, mode lock.Mode, ok bool) {
	if _, alter := stmt.(*parser.AlterIndex); !alter || len(g.Targets) == 0 {
		return ids.ObjectRef{}, 0, false
	}
	ref = g.Targets[0]
	mode = lock.AccessExclusive
	if g.Descriptors[ref.Address].RelationKind == catalog.IndexRelation {
		mode = lock.ShareUpdateExclusive
	}
	return ref, mode, true
}
