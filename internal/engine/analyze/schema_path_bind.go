package analyze

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (b *Binder) builtinVisible(name parser.ObjectName, kind LookupKind) (bool, error) {
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
