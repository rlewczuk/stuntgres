package control

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// PersistentReplacementCatalog returns the private candidate a catalog
// replacement installs: view without its temporary namespaces, their members,
// names and operator/procedure index entries, and the defaults that named them.
//
// Replacement transfers only portable objects. SessionToken authority belongs
// to the source physical session and cannot be imported, even by self replacement:
// the destination overlays its exact current live TEMP objects after capture.
// Keep the supplied published view immutable and use this private candidate for
// provider validation and generator/storage capture as well as installation.
func PersistentReplacementCatalog(view catalog.Read) (catalog.Read, error) {
	for _, descriptor := range view.Descriptors() {
		if extension, ok := descriptor.(*catalog.Extension); ok {
			namespace, ok := view.Namespace(extension.TargetNamespace)
			if !ok {
				return catalog.Read{}, fmt.Errorf("stuntgres: extension namespace is missing")
			}
			if namespace.Temporary != (ids.SessionToken{}) {
				return catalog.Read{}, fmt.Errorf("stuntgres: temporary extension installation is not portable")
			}
		}
	}
	next := catalog.CloneState(view.State())
	operators := catalog.OperatorIndexEdit{Index: view.State().OperatorNames()}
	procedures := catalog.ProcedureIndexEdit{Index: view.State().ProcedureNames()}
	for a, d := range view.Descriptors() {
		ns, isNamespace := d.(*catalog.Namespace)
		parent, hasParent := view.Namespace(d.Namespace())
		if isNamespace && ns.Temporary != (ids.SessionToken{}) || hasParent && parent.Temporary != (ids.SessionToken{}) {
			operators.Replace(d, nil)
			procedures.Replace(d, nil)
			next.RemoveDescriptor(a)
		}
	}
	for key, a := range view.Names() {
		parent, hasParent := view.Namespace(key.Namespace)
		if view.Get(a) != nil && next.Get(a) == nil || hasParent && parent.Temporary != (ids.SessionToken{}) {
			next.UnbindName(key)
		}
	}
	next.SetOperatorNames(operators.Index)
	next.SetProcedureNames(procedures.Index)
	catalog.PruneNamespaceDefaults(view.State(), next, nil)
	return view.WithState(next), nil
}
