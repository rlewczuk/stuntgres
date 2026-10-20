package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (b *expressionBinder) builtinVisible(name parser.ObjectName, kind analyze.LookupKind) (bool, error) {
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
