package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestNamespaceIdentityStoredValidation(t *testing.T) {
	types := formatUnitTypes()
	oidType := types[sqltype.Oid]
	boolType := types[sqltype.Bool]
	n := NamespaceInquiry{Info: expr.Info{Usage: oidType.Usage(), Property: expr.Properties{Volatility: expr.Stable}}, Kind: MyTempNamespace}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := n
	bad.Property.Volatility = expr.Immutable
	if bad.Validate() == nil {
		t.Fatal("lost stable volatility accepted")
	}
	bad = n
	bad.Usage = boolType.Usage()
	if bad.Validate() == nil {
		t.Fatal("wrong result accepted")
	}
	n.Kind = OtherTempNamespace
	n.Usage = boolType.Usage()
	value, _ := sqltype.OIDValue(oidType, 0)
	n.Arg = temporalTestLiteral(value)
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	n.Arg = nil
	if n.Validate() == nil {
		t.Fatal("missing operand accepted")
	}
}
