package analyze

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (b *Binder) builtinVisible(name parser.ObjectName, kind LookupKind) (bool, error) {
	if selected := b.Clause.selectedProcedure; selected != nil && selected.Builtin != nil && len(name.Parts) > 0 && name.Parts[len(name.Parts)-1].Text == selected.Builtin.Identity.Name {
		return true, nil
	}
	auth, err := b.BindingAuthorization()
	if err != nil {
		return false, err
	}
	path, err := b.Host.LookupNamespaces(auth, name, kind)
	if err != nil {
		return false, err
	}
	for i := range path.Count() {
		if path.At(i).ObjectID == ids.PGCatalogNamespaceOID {
			return true, nil
		}
	}
	return false, nil
}
