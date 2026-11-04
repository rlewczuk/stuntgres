package regobj

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func RoutineIdentityByOID(view catalog.Read, id ids.OID) (catalog.RoutineIdentity, bool) {
	if p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}); ok {
		return catalog.RoutineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}, true
	}
	if p, ok := catalog.BuiltinRoutineIdentityByOID(id); ok {
		return p, true
	}
	return catalog.RoutineIdentity{}, false
}

func routineNamespaceVisible(view catalog.Read, namespace ids.ObjectAddress) bool {
	ns, ok := view.Namespace(namespace)
	return ok && ns.Temporary == (ids.SessionToken{})
}

// Exact signatures use both existing catalog NameKeys and the immutable release
// signature index. The first candidate masks all later identical signatures.
func SelectExactRoutine(view catalog.Read, path analyze.NamespaceSelection, name string, signature catalog.ProcedureSignature) (catalog.RoutineIdentity, int) {
	for i := range path.Count() {
		namespace := path.At(i)
		if path.Qualified == (ids.ObjectAddress{}) && !routineNamespaceVisible(view, namespace) {
			continue
		}
		if p := analyze.ProcedureInNamespaces(view, analyze.NamespaceSelection{Qualified: namespace}, name, signature); p != nil {
			return catalog.RoutineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}, 1
		}
		if namespace.ObjectID == ids.PGCatalogNamespaceOID {
			if p, ok := catalog.BuiltinRoutineIdentityBySignature(name, signature); ok {
				if current, represented := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: p.ID}); represented &&
					(current.Namespace().ObjectID != p.Namespace || current.Name() != p.Name || current.Signature != p.Signature) {
					continue
				}
				return p, 1
			}
		}
	}
	return catalog.RoutineIdentity{}, 0
}

// Earlier schemas mask only identical signatures, not every same-named routine.
// Bare-name selection stops after two candidates, the regproc ambiguity boundary.
func SelectRoutine(view catalog.Read, path analyze.NamespaceSelection, name string, signature *catalog.ProcedureSignature) (catalog.RoutineIdentity, int) {
	if signature != nil {
		return SelectExactRoutine(view, path, name, *signature)
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
		if path.Qualified == (ids.ObjectAddress{}) && !routineNamespaceVisible(view, namespace) {
			continue
		}
		for _, d := range view.Descriptors() {
			if p, ok := d.(*catalog.Procedure); ok && p.Namespace() == namespace {
				if p.Name() == name && accept(catalog.RoutineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}) {
					return selected, count
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

// RoutineDescriptorOwner returns the genuine descriptor for this procedure,
// including installed support aliases. Metadata-only builtin identities have none.
func RoutineDescriptorOwner(view catalog.Read, id ids.OID) *catalog.Procedure {
	if p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}); ok {
		return p
	}
	return nil
}
