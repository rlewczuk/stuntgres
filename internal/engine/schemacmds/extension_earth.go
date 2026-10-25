package schemacmds

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// EarthChecks runs only inside the extension's catalog stage with e.mu held.
// Recipes use normal CHECK records and the same renderer/dependency authority
// as SQL-created domains; execution remains EvaluateDomainChecks.
func (h *extensionHost) EarthChecks(d *catalog.Type) error {
	view := h.Engine.StageRead(h.Stage)
	base := d.SQLType.DomainBase()
	boolean, ok := view.LookupType(sqltype.Bool)
	if !ok || !base.Type.IsCube() || len(d.DomainChecks) != 0 {
		return fmt.Errorf("stuntgres: invalid earth domain staging")
	}
	property := expr.Properties{DomainDependent: true}
	for _, recipe := range []struct {
		name    string
		kind    sqltype.EarthCheck
		ordinal int
	}{
		{"not_3d", sqltype.EarthNot3D, 1}, {"not_point", sqltype.EarthNotPoint, 0}, {"on_surface", sqltype.EarthOnSurface, 2},
	} {
		node := plan.EarthCheckExpr{Info: expr.Info{Usage: boolean.Usage(), Property: property}, Kind: recipe.kind, Value: plan.DomainValueExpr{Info: expr.Info{Usage: base, Property: property}}}
		var refs []ids.ObjectRef
		if _, err := (stored.ScalarRenderer{DomainValue: &base, Dependencies: &refs}).SQL(h.Context(), view, node); err != nil {
			return err
		}
		oid, err := h.AllocateOID()
		if err != nil {
			return err
		}
		check := catalog.DomainCheckConstraint{OID: oid, Name: recipe.name, Ordinal: recipe.ordinal, Expression: node}
		for _, ref := range refs {
			address := ref.Address
			address.SubID = 0
			provider, ok := view.Descriptor(address)
			if !ok {
				return fmt.Errorf("stuntgres: missing earth CHECK provider")
			}
			check.Dependencies = append(check.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: provider.Revision()})
			h.Stage.DomainBases = append(h.Stage.DomainBases, provider)
		}
		d.DomainChecks = append(d.DomainChecks, check)
	}
	return nil
}
