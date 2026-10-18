package engine

import "stuntgres/internal/engine/ids"

// routineIdentity is lookup-only metadata. It is deliberately not a Descriptor:
// finding a routine alias must never grant execution or expand pg_proc SQL rows.
type routineIdentity struct {
	ID        ids.OID
	Namespace ids.OID
	Name      string
	Signature procedureSignature
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
		byOID[p.ID] = p
		byName[p.Name] = append(byName[p.Name], p)
		bySignature[routineKey{p.Name, p.Signature}] = p
	}
	return byOID, byName, bySignature
}

func routineIdentityByOID(view CatalogRead, id ids.OID) (routineIdentity, bool) {
	if p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}); ok {
		return routineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}, true
	}
	if p, ok := routineByOID[id]; ok {
		return p, true
	}
	if owner := routineDescriptorOwner(view, id); owner != nil {
		for _, helper := range owner.Aggregate.Helpers {
			if helper.ID == id {
				return helper, true
			}
		}
	}
	return routineIdentity{}, false
}

func routineNamespaceVisible(view CatalogRead, namespace ids.ObjectAddress) bool {
	ns, ok := view.Namespace(namespace)
	return ok && ns.Temporary == (ids.SessionToken{})
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
			return routineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}, 1
		}
		for _, d := range view.Descriptors() {
			if owner, ok := d.(*procedureDescriptor); ok && owner.Aggregate != nil && owner.Namespace() == namespace {
				for _, helper := range owner.Aggregate.Helpers {
					if helper.Name == name && helper.Signature == signature {
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
		if seen[p.Signature] {
			return false
		}
		seen[p.Signature] = true
		selected = p
		count++
		return count == 2
	}
	for i := range path.count() {
		namespace := path.at(i)
		if !routineNamespaceVisible(view, namespace) {
			continue
		}
		for _, d := range view.Descriptors() {
			if p, ok := d.(*procedureDescriptor); ok && p.Namespace() == namespace {
				if p.Name() == name && accept(routineIdentity{p.Address().ObjectID, p.Namespace().ObjectID, p.Name(), p.Signature}) {
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
			for _, p := range routinesByName[name] {
				if accept(p) {
					return selected, count
				}
			}
		}
	}
	return selected, count
}
