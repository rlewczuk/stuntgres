package plan

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// EarthCheckExpr is an immutable provider-owned domain recipe. Its lexical
// VALUE child captures the actual installed cube generation, never a name.
type EarthCheckExpr struct {
	expr.Info
	Kind  sqltype.EarthCheck
	Value DomainValueExpr
}

// EarthCheckRecipe permits the lower-layer extension manifest to inspect the
// immutable recipe without importing plan. Concrete stored-tree validation
// remains the ordinary renderer's responsibility.
func (e EarthCheckExpr) EarthCheckRecipe() (sqltype.EarthCheck, sqltype.Usage, bool) {
	return e.Kind, e.Value.Usage, e.Valid()
}

func (e EarthCheckExpr) Valid() bool {
	return e.Kind.Valid() && e.Usage.Valid() && e.Usage.Type.ID() == sqltype.Bool &&
		e.Property == (expr.Properties{DomainDependent: true}) &&
		e.Value.Usage.Valid() && e.Value.Usage.Type.IsCube() &&
		e.Value.Property == (expr.Properties{DomainDependent: true})
}

func (e EarthCheckExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if !e.Valid() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid earth CHECK expression")
	}
	v, err := e.Value.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if v.Type() != e.Value.Usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign earth CHECK value")
	}
	if v.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	ok, err := v.CheckEarthCube(e.Kind)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Boolean(e.Usage.Type, ok), nil
}
