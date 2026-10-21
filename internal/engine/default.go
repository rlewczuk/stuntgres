package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
)

func bindCreateDefaults(b *analyze.Binder, columns []catalog.Column, declarations []analyze.ColumnDeclaration) (map[int32]catalog.ColumnDefault, error) {
	var defaults map[int32]catalog.ColumnDefault
	auth, err := b.BindingAuthorization()
	if err != nil {
		return nil, err
	}
	view := auth.ReadCatalog()
	for slot := range declarations {
		raw := declarations[slot].DefaultExpr
		if raw == nil {
			continue
		}
		node, err := b.BindDefault(raw, columns[slot])
		if err != nil {
			return nil, err
		}
		if stored.OmitColumnDefault(view, node, columns[slot].Usage) {
			continue
		}
		definition := catalog.ColumnDefault{Expression: node}
		_, err = (stored.ScalarRenderer{Definition: b.DefinitionReader(), ACLOutput: regobj.ACLItemOutput{Owner: b.Host.CatalogOwner(), Roles: b.Host.Roles(auth)}, Dependencies: &definition.Dependencies, CommentSnapshot: &definition.CatalogSnapshot, CatalogAliasReferences: &definition.CatalogAliasReferences}).SQL(b.Ctx, view, node)
		if err != nil {
			return nil, err
		}
		if defaults == nil {
			defaults = make(map[int32]catalog.ColumnDefault)
		}
		defaults[int32(slot+1)] = definition
	}
	return defaults, nil
}
