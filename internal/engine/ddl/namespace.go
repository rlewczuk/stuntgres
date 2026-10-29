package ddl

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// StageObjectNamespace implements the generic lower catalog utility. Types,
// routines and relations retain their specialized mechanical owners.
func StageObjectNamespace(h Host, auth Authority, d catalog.Descriptor, destination ids.ObjectAddress) (ids.ObjectAddress, error) {
	previous := d.Namespace()
	if previous == destination {
		return previous, nil
	}
	view := h.AccessView(auth)
	if err := catalog.CheckSetNamespace(view.Catalog, previous, destination); err != nil {
		return ids.ObjectAddress{}, err
	}
	kind := ""
	ownerless := false
	switch d.(type) {
	case *catalog.Operator:
		kind = "operator"
	case *catalog.OperatorClass:
		kind = "operator class"
	case *catalog.OperatorFamily:
		kind = "operator family"
	case *catalog.Collation:
		kind = "collation"
	case *catalog.Conversion:
		kind = "conversion"
	case *catalog.TSDictionary:
		kind = "text search dictionary"
	case *catalog.TSConfiguration:
		kind = "text search configuration"
	case *catalog.TSParser:
		kind = "text search parser"
		ownerless = true
	case *catalog.TSTemplate:
		kind = "text search template"
		ownerless = true
	default:
		return ids.ObjectAddress{}, fmt.Errorf("stuntgres: object has no generic namespace mover")
	}
	if ownerless {
		actor, err := view.Roles.Descriptor(view.State.CurrentUser)
		if err != nil {
			return ids.ObjectAddress{}, err
		}
		if !actor.Superuser {
			description, err := h.DependencyObject(auth, d.Address())
			if err != nil {
				return ids.ObjectAddress{}, err
			}
			return ids.ObjectAddress{}, sqlError("42501", "must be superuser to set schema of "+description)
		}
	} else {
		if err := view.Check(auth, access.Requirement{Object: ObjectRef(h, auth, d.Address()), OwnerOnly: true, OwnerNoun: kind}); err != nil {
			return ids.ObjectAddress{}, err
		}
	}
	if err := h.CheckCreateIn(auth, ObjectRef(h, auth, destination)); err != nil {
		return ids.ObjectAddress{}, err
	}
	revision, err := h.Revision()
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	var next catalog.Descriptor
	switch d := d.(type) {
	case *catalog.Operator:
		n := *d
		n.ObjectNamespace = destination
		n.ObjectRevision = revision
		next = &n
	case *catalog.OperatorClass:
		n := *d
		n.ObjectNamespace = destination
		n.ObjectRevision = revision
		next = &n
	case *catalog.OperatorFamily:
		n := *d
		n.ObjectNamespace = destination
		n.ObjectRevision = revision
		next = &n
	case *catalog.Collation:
		n := *d
		n.ObjectNamespace = destination
		n.ObjectRevision = revision
		next = &n
	case *catalog.Conversion:
		n := *d
		n.ObjectNamespace = destination
		n.ObjectRevision = revision
		next = &n
	case *catalog.TSDictionary:
		n := *d
		n.ObjectNamespace = destination
		n.ObjectRevision = revision
		next = &n
	case *catalog.TSConfiguration:
		n := *d
		n.ObjectNamespace = destination
		n.ObjectRevision = revision
		next = &n
	case *catalog.TSParser:
		n := *d
		n.ObjectNamespace = destination
		n.ObjectRevision = revision
		next = &n
	case *catalog.TSTemplate:
		n := *d
		n.ObjectNamespace = destination
		n.ObjectRevision = revision
		next = &n
	}
	if a := h.Stage().LookupName(catalog.DescriptorNameKey(next)); a != (ids.ObjectAddress{}) {
		return ids.ObjectAddress{}, sqlError("42710", fmt.Sprintf("%s %q already exists in schema %q", kind, d.Name(), view.Catalog.MustNamespace(destination).Name()))
	}
	if err := h.Stage().Replace(next); err != nil {
		return ids.ObjectAddress{}, err
	}
	return previous, h.RecordDependencyPath(auth)
}
