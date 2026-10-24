package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Lifecycle utilities alter only immutable attached payloads. Locks are acquired
// before Engine.mu; publication retains the relation and extension source fences.
func (e *Engine) executeTriggerLifecycle(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s, ok := stmt.(*parser.AlterTrigger); ok && s.Action == "rename" {
		if err := scope.acquirePartitionDescendants(scope.lockedRef, lock.AccessExclusive); err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	auth, err := e.statementAuthorizationLocked(scope.op, stmt)
	if err != nil {
		return fail(err)
	}
	var target parser.ObjectName
	var name string
	alter, renaming := stmt.(*parser.AlterTrigger)
	drop, dropping := stmt.(*parser.DropTrigger)
	if renaming {
		target, name = alter.Table, alter.Name.Text
	} else {
		target, name = drop.Table, drop.Name.Text
	}
	h := ddlHost{e: e, op: scope.op}
	ref, err := h.ResolveRelation(auth, target)
	if err != nil {
		if dropping && drop.IfExists && table.RenameMissing(err) {
			d := err.(*sqlstate.Error)
			return e.finishPolicyNoticeLocked(scope, d.Message+", skipping")
		}
		return fail(err)
	}
	if ref != scope.lockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	old := auth.Catalog.MustRelation(ref.Address)
	list := stored.TriggersOf(old)
	index := slices.IndexFunc(list, func(t stored.Trigger) bool { return t.Name() == name })
	missing := func() error {
		return sqlError("42704", fmt.Sprintf(`trigger "%s" for table "%s" does not exist`, name, old.Name()))
	}
	// Generic attached-object lookup checks absence before ownership. Rename
	// checks table ownership first, including when the trigger is missing.
	if (!renaming || alter.Action != "rename") && index < 0 {
		if dropping && drop.IfExists {
			return e.finishPolicyNoticeLocked(scope, fmt.Sprintf(`trigger "%s" for relation "%s" does not exist, skipping`, name, catalog.ObjectNameText(target)))
		}
		return fail(missing())
	}
	noun := "relation"
	if renaming && alter.Action == "rename" {
		noun = "table"
	}
	if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true, OwnerNoun: noun}); err != nil {
		return fail(err)
	}
	if index < 0 {
		return fail(missing())
	}
	p := list[index].Parts()
	var sources []catalog.Descriptor
	if dropping {
		if p.Parent.ObjectID != 0 {
			path, err := referencePath(auth)
			if err != nil {
				return fail(err)
			}
			return fail(view.TriggerPartitionDropError(auth.Catalog, old, list[index], path))
		}
		list = append(slices.Clone(list[:index]), list[index+1:]...)
	} else {
		switch alter.Action {
		case "rename":
			if p.Parent.ObjectID != 0 {
				parent, _ := stored.TriggerParent(auth.Catalog, p.Parent)
				if parent == nil {
					return fail(ids.ErrCatalogReplaced)
				}
				return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`cannot rename trigger "%s" on table "%s"`, name, old.Name()), Hint: fmt.Sprintf(`Rename the trigger on the partitioned table "%s" instead.`, parent.Name())})
			}
			if p.Name != alter.Target.Text && slices.ContainsFunc(list, func(t stored.Trigger) bool { return t.Name() == alter.Target.Text }) {
				return fail(sqlError("42710", fmt.Sprintf(`trigger "%s" for relation "%s" already exists`, alter.Target.Text, old.Name())))
			}
			if p.Name == alter.Target.Text {
				return e.publishRelationChangeLocked(scope, auth, view.RelationChange{Ref: ref, Next: old}, triggerReplacement{})
			}
			p.Name = alter.Target.Text
		case "depends", "no_depends":
			address, exists := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: alter.Target.Text})
			if !exists {
				return fail(sqlError("42704", fmt.Sprintf(`extension "%s" does not exist`, alter.Target.Text)))
			}
			extension, ok := auth.Catalog.Extension(address)
			if !ok {
				return fail(ids.ErrCatalogReplaced)
			}
			sources = append(sources, extension)
			present := slices.Contains(p.AutoExtensions, address)
			if present == (alter.Action == "depends") {
				return e.publishRelationChangeLocked(scope, auth, view.RelationChange{Ref: ref, Next: old, Sources: sources}, triggerReplacement{})
			}
			if alter.Action == "depends" && !present {
				p.AutoExtensions = append(slices.Clone(p.AutoExtensions), address)
			}
			if alter.Action == "no_depends" && present {
				p.AutoExtensions = slices.DeleteFunc(slices.Clone(p.AutoExtensions), func(a ids.ObjectAddress) bool { return a == address })
			}
		default:
			return fail(sqlError("0A000", "unsupported ALTER TRIGGER action"))
		}
		definition, err := stored.NewTrigger(p)
		if err != nil {
			return fail(err)
		}
		list = slices.Clone(list)
		list[index] = definition
		slices.SortFunc(list, func(a, b stored.Trigger) int { return strings.Compare(a.Name(), b.Name()) })
	}
	next := *old
	next.Triggers = stored.TriggersPayload(list)
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return fail(err)
	}
	return e.publishRelationChangeLocked(scope, auth, view.RelationChange{Ref: ref, Next: &next, Sources: sources}, triggerReplacement{})
}
