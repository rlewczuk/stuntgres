package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func routineIdentityByOID(view catalog.Read, id ids.OID) (catalog.RoutineIdentity, bool) {
	if p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}); ok {
		return catalog.RoutineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}, true
	}
	if p, ok := catalog.BuiltinRoutineIdentityByOID(id); ok {
		return p, true
	}
	if owner := routineDescriptorOwner(view, id); owner != nil {
		for _, helper := range owner.Aggregate.Helpers {
			if helper.ID == id {
				return helper, true
			}
		}
	}
	return catalog.RoutineIdentity{}, false
}

func routineNamespaceVisible(view catalog.Read, namespace ids.ObjectAddress) bool {
	ns, ok := view.Namespace(namespace)
	return ok && ns.Temporary == (ids.SessionToken{})
}

// Exact signatures use both existing catalog NameKeys and the immutable release
// signature index. The first candidate masks all later identical signatures.
func selectExactRoutine(view catalog.Read, path namespaceSelection, name string, signature catalog.ProcedureSignature) (catalog.RoutineIdentity, int) {
	for i := range path.Count() {
		namespace := path.At(i)
		if !routineNamespaceVisible(view, namespace) {
			continue
		}
		if p := procedureInNamespaces(view, namespaceSelection{Qualified: namespace}, name, signature); p != nil {
			return catalog.RoutineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}, 1
		}
		for _, d := range view.Descriptors() {
			if owner, ok := d.(*catalog.Procedure); ok && owner.Aggregate != nil && owner.Namespace() == namespace {
				for _, helper := range owner.Aggregate.Helpers {
					if helper.Name == name && helper.Signature == signature {
						return helper, 1
					}
				}
			}
		}
		if namespace.ObjectID == ids.PGCatalogNamespaceOID {
			if p, ok := catalog.BuiltinRoutineIdentityBySignature(name, signature); ok {
				return p, 1
			}
		}
	}
	return catalog.RoutineIdentity{}, 0
}

// Earlier schemas mask only identical signatures, not every same-named routine.
// Bare-name selection stops after two candidates, the regproc ambiguity boundary.
func selectRoutine(view catalog.Read, path namespaceSelection, name string, signature *catalog.ProcedureSignature) (catalog.RoutineIdentity, int) {
	if signature != nil {
		return selectExactRoutine(view, path, name, *signature)
	}
	var selected catalog.RoutineIdentity
	count := 0
	seen := make(map[catalog.ProcedureSignature]bool)
	accept := func(p catalog.RoutineIdentity) bool {
		if seen[p.Signature] {
			return false
		}
		seen[p.Signature] = true
		selected = p
		count++
		return count == 2
	}
	for i := range path.Count() {
		namespace := path.At(i)
		if !routineNamespaceVisible(view, namespace) {
			continue
		}
		for _, d := range view.Descriptors() {
			if p, ok := d.(*catalog.Procedure); ok && p.Namespace() == namespace {
				if p.Name() == name && accept(catalog.RoutineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}) {
					return selected, count
				}
				if p.Aggregate != nil {
					for _, helper := range p.Aggregate.Helpers {
						if helper.Name == name && accept(helper) {
							return selected, count
						}
					}
				}
			}
		}
		if namespace.ObjectID == ids.PGCatalogNamespaceOID {
			for _, p := range catalog.BuiltinRoutineIdentitiesNamed(name) {
				if accept(p) {
					return selected, count
				}
			}
		}
	}
	return selected, count
}

// A helper's dependencies are dependencies on this real aggregate descriptor,
// never invented ObjectRefs for metadata-only identities.
func routineDescriptorOwner(view catalog.Read, id ids.OID) *catalog.Procedure {
	if p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}); ok {
		return p
	}
	for _, d := range view.Descriptors() {
		if p, ok := d.(*catalog.Procedure); ok && p.Aggregate != nil {
			for _, helper := range p.Aggregate.Helpers {
				if helper.ID == id {
					return p
				}
			}
		}
	}
	return nil
}
