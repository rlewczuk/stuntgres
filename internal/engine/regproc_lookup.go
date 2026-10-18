package engine

import "stuntgres/internal/engine/ids"

// routineIdentity is lookup-only metadata. It is deliberately not a Descriptor:
// finding a routine alias must never grant execution or expand pg_proc SQL rows.
type routineIdentity struct {
	id        ids.OID
	namespace ids.OID
	name      string
	signature procedureSignature
}
type routineKey struct {
	name      string
	signature procedureSignature
}

var routineByOID, routinesByName, routineBySignature = indexBuiltinRoutines()

func indexBuiltinRoutines() (map[ids.OID]routineIdentity, map[string][]routineIdentity, map[routineKey]routineIdentity) {
	byOID := make(map[ids.OID]routineIdentity, len(builtinRoutineIdentities))
	byName := make(map[string][]routineIdentity)
	bySignature := make(map[routineKey]routineIdentity, len(builtinRoutineIdentities))
	for _, p := range builtinRoutineIdentities {
		byOID[p.id] = p
		byName[p.name] = append(byName[p.name], p)
		bySignature[routineKey{p.name, p.signature}] = p
	}
	return byOID, byName, bySignature
}

func routineIdentityByOID(view CatalogRead, id ids.OID) (routineIdentity, bool) {
	if p, ok := view.state.descriptors[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}].(*procedureDescriptor); ok {
		return routineIdentity{p.address.ObjectID, p.namespace.ObjectID, p.name, p.signature}, true
	}
	if p, ok := routineByOID[id]; ok {
		return p, true
	}
	if owner := routineDescriptorOwner(view, id); owner != nil {
		for _, helper := range owner.aggregate.helpers {
			if helper.id == id {
				return helper, true
			}
		}
	}
	return routineIdentity{}, false
}

func routineNamespaceVisible(view CatalogRead, namespace ids.ObjectAddress) bool {
	ns, ok := view.state.descriptors[namespace].(*namespaceDescriptor)
	return ok && ns.temporary == (SessionToken{})
}

// Exact signatures use both existing catalog NameKeys and the immutable release
// signature index. The first candidate masks all later identical signatures.
func selectExactRoutine(view CatalogRead, path namespaceSelection, name string, signature procedureSignature) (routineIdentity, int) {
	for i := range path.count() {
		namespace := path.at(i)
		if !routineNamespaceVisible(view, namespace) {
			continue
		}
		if p := procedureInNamespaces(view, namespaceSelection{qualified: namespace}, name, signature); p != nil {
			return routineIdentity{p.address.ObjectID, p.namespace.ObjectID, p.name, p.signature}, 1
		}
		for _, d := range view.state.descriptors {
			if owner, ok := d.(*procedureDescriptor); ok && owner.aggregate != nil && owner.namespace == namespace {
				for _, helper := range owner.aggregate.helpers {
					if helper.name == name && helper.signature == signature {
						return helper, 1
					}
				}
			}
		}
		if namespace.ObjectID == ids.PGCatalogNamespaceOID {
			if p, ok := routineBySignature[routineKey{name, signature}]; ok {
				return p, 1
			}
		}
	}
	return routineIdentity{}, 0
}

// Earlier schemas mask only identical signatures, not every same-named routine.
// Bare-name selection stops after two candidates, the regproc ambiguity boundary.
func selectRoutine(view CatalogRead, path namespaceSelection, name string, signature *procedureSignature) (routineIdentity, int) {
	if signature != nil {
		return selectExactRoutine(view, path, name, *signature)
	}
	var selected routineIdentity
	count := 0
	seen := make(map[procedureSignature]bool)
	accept := func(p routineIdentity) bool {
		if seen[p.signature] {
			return false
		}
		seen[p.signature] = true
		selected = p
		count++
		return count == 2
	}
	for i := range path.count() {
		namespace := path.at(i)
		if !routineNamespaceVisible(view, namespace) {
			continue
		}
		for _, d := range view.state.descriptors {
			if p, ok := d.(*procedureDescriptor); ok && p.namespace == namespace {
				if p.name == name && accept(routineIdentity{p.address.ObjectID, p.namespace.ObjectID, p.name, p.signature}) {
					return selected, count
				}
				if p.aggregate != nil {
					for _, helper := range p.aggregate.helpers {
						if helper.name == name && accept(helper) {
							return selected, count
						}
					}
				}
			}
		}
		if namespace.ObjectID == ids.PGCatalogNamespaceOID {
			for _, p := range routinesByName[name] {
				if accept(p) {
					return selected, count
				}
			}
		}
	}
	return selected, count
}
