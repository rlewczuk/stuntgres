package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// OF names are catalog object names, not SQL declaration aliases.
func (e *Engine) namedType(auth AuthorizationView, name parser.ObjectName) (*catalog.Type, error) {
	path, err := e.LookupNamespaces(auth, name, analyze.TypeLookup)
	if err != nil {
		return nil, err
	}
	for i := range path.Count() {
		found := auth.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: name.Parts[len(name.Parts)-1].Text})
		if len(found) == 1 {
			return auth.Catalog.MustType(found[0]), nil
		}
	}
	return nil, expr.ErrorAt("42704", fmt.Sprintf(`type "%s" does not exist`, catalog.ObjectNameText(name)), name.Parts[0].Pos)
}
