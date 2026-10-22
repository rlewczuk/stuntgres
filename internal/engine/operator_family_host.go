package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
)

func (h ddlHost) SelectFamilyFunction(a ddl.Authority, name parser.ObjectName, signature *catalog.ProcedureSignature) (catalog.RoutineIdentity, int, error) {
	// Support-function identity lookup is an object-address utility, not an
	// expression. Preserve the AST while using its namespace without a location.
	if len(name.Parts) == 2 {
		parts := [2]parser.Identifier{name.Parts[0], name.Parts[1]}
		parts[0].Pos = parser.Position{}
		name.Parts = parts[:]
	}
	v := ddlAuthorityOf(a)
	path, err := h.e.lookupNamespaces(v, name, analyze.FunctionLookup)
	if err != nil {
		return catalog.RoutineIdentity{}, 0, err
	}
	identity, count := regobj.SelectRoutine(v.Catalog, path, name.Parts[len(name.Parts)-1].Text, signature)
	return identity, count, nil
}

func (h ddlHost) CheckFamilyMemberDrop(auth ddl.Authority, target ids.ObjectAddress) error {
	if err := h.RecordDependencyPath(auth); err != nil {
		return err
	}
	targets := []ids.ObjectAddress{target}
	edges, err := domainDependencyClosure(h.op.Context(), h.stage, targets)
	if err != nil {
		return err
	}
	for _, edge := range edges {
		if !edge.Automatic {
			return domainRestrictError(h.stage, targets, edges, 1, target)
		}
	}
	return nil
}
