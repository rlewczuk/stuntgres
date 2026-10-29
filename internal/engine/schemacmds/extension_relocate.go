package schemacmds

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func stageExtensionSchemaLocked(h *extensionHost, old *catalog.Extension, target parser.Identifier) error {
	auth := h.Auth
	base := core.DDLHost{E: h.Engine, Op: h.Op, CatalogStage: h.Stage}
	// PostgreSQL LookupCreationNamespace includes CREATE admission. Our captured
	// resolver separates it, so perform that gate before extension ownership.
	destination, nextAuth, err := base.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{target, {Text: old.Name()}}})
	if err != nil {
		return err
	}
	auth = core.DDLAuthorityOf(nextAuth)
	h.Auth = auth
	h.namespace = destination.Address
	if err := base.CheckCreateIn(auth, destination); err != nil {
		return err
	}
	if err := auth.View.Check(auth, access.Requirement{Object: auth.Owner.Domain.Ref(auth.Catalog.Epoch(), old.Address()), OwnerOnly: true, OwnerNoun: "extension"}); err != nil {
		return err
	}
	if err := base.CheckCreateIn(auth, destination); err != nil {
		return err
	}
	if d := h.CurrentRead().Get(destination.Address); d != nil && catalog.ExtensionMembership(d) == old.Address() {
		return extensionAlterError("55000", fmt.Sprintf("cannot move extension %q into schema %q because the extension contains the schema", old.Name(), d.Name()))
	}
	if destination.Address == old.TargetNamespace {
		return nil
	}
	implementation := auth.Owner.Extensions[old.Name()]
	if implementation == nil {
		return fmt.Errorf("stuntgres: installed extension has no implementation")
	}
	if !implementation.Control().Relocatable {
		return extensionAlterError("0A000", fmt.Sprintf("extension %q does not support SET SCHEMA", old.Name()))
	}
	// The global allocator cannot advance an exhausted descriptor predecessor.
	// Reject before staging any member of the immutable relocation graph.
	if old.Revision() == ^ids.DefinitionRevision(0) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	predecessors := h.CurrentRead()
	for _, a := range old.Members {
		if d := predecessors.Get(a); d != nil && d.Namespace() != (ids.ObjectAddress{}) && d.Revision() == ^ids.DefinitionRevision(0) {
			return fmt.Errorf("stuntgres: definition revision exhausted")
		}
	}
	moved := make(map[ids.ObjectAddress]bool)
	routines := RoutineHost{base}
	types := tablecmds.TypeHost{tablecmds.TableHost{base}}
	for _, a := range old.Members {
		if err := h.Context().Err(); err != nil {
			return err
		}
		d := h.CurrentRead().Get(a)
		if d == nil || catalog.ExtensionMembership(d) != old.Address() {
			return fmt.Errorf("stuntgres: invalid extension member projection")
		}
		var previous ids.ObjectAddress
		switch object := d.(type) {
		case *catalog.Type:
			previous, err = typedef.StageMoveTypeNamespace(types, auth, object, destination.Address, moved)
		case *catalog.Relation:
			if !moved[a] {
				previous, err = stageExtensionRelationNamespace(h, object, destination.Address, moved)
			}
		case *catalog.Procedure:
			if !moved[a] {
				previous, err = routine.StageRoutineNamespace(routines, auth, object, destination.Address)
			}
		default:
			if d.Namespace() != (ids.ObjectAddress{}) && !moved[a] {
				previous, err = ddl.StageObjectNamespace(base, auth, d, destination.Address)
			}
		}
		if err != nil {
			return err
		}
		if previous != (ids.ObjectAddress{}) && previous != old.TargetNamespace {
			description, err := base.DependencyObject(auth, a)
			if err != nil {
				return err
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf("extension %q does not support SET SCHEMA", old.Name()), Detail: fmt.Sprintf("%s is not in the extension's schema %q", description, auth.Catalog.MustNamespace(old.TargetNamespace).Name())}
		}
		moved[a] = true
	}
	next := *old
	next.TargetNamespace = destination.Address
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	if err := ddl.ReplaceCommand(h.Stage, "ALTER EXTENSION", &next); err != nil {
		return err
	}
	return base.RecordDependencyPath(auth)
}

func stageExtensionRelationNamespace(h *extensionHost, old *catalog.Relation, destination ids.ObjectAddress, moved map[ids.ObjectAddress]bool) (ids.ObjectAddress, error) {
	previous := old.Namespace()
	if previous == destination {
		return previous, nil
	}
	view := h.CurrentRead()
	if err := catalog.CheckSetNamespace(view, previous, destination); err != nil {
		return ids.ObjectAddress{}, err
	}
	owned := table.SetSchemaOwned(view, old)
	group, err := table.SetSchemaGroup(view, old, owned)
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	next, err := table.SetSchemaCandidates(view, group, destination, view.MustNamespace(destination).Name())
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	// Internal relation movement does not reapply public ALTER TABLE ownership.
	// The actual relation synchronizer owns row-type/array generations.
	for i := range next {
		if next[i].RowType != 0 {
			row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(next[i].RowType)})
			array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
			if a := h.Stage.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: destination, Name: array.Name()}); a != (ids.ObjectAddress{}) && a != array.Address() {
				return ids.ObjectAddress{}, extensionAlterError("42710", fmt.Sprintf("type %q already exists in schema %q", array.Name(), view.MustNamespace(destination).Name()))
			}
			moved[row.Address()] = true
			moved[array.Address()] = true
		}
		if err := h.Replace(&next[i]); err != nil {
			return ids.ObjectAddress{}, err
		}
		moved[next[i].Address()] = true
	}
	return previous, nil
}
