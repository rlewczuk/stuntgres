package enginetests

import (
 "context"
 "testing"
 "stuntgres/internal/sqltype"
)

func ltxtqueryUnitType(t *testing.T,id sqltype.ID) *sqltype.Descriptor {
 t.Helper(); d,err:=sqltype.Ltxtquery(id,"ltxtquery","public.ltxtquery"); if err!=nil {t.Fatal(err)}; return d
}

func TestLtxtqueryGeneration(t *testing.T) {
	ctx := context.Background()
	d, foreign, destination := ltxtqueryUnitType(t,65000), ltxtqueryUnitType(t,65000), ltxtqueryUnitType(t,65001)
	if !d.IsLtxtquery() || !d.IsUserDefined() || d.IsBuiltin() || !d.Concrete() || d.DatabaseName() != "LTXTQUERY" { t.Fatal("scalar identity") }
	facts, ok := d.PGTypeMetadata()
	if !ok || facts.Length != -1 || facts.ByValue || facts.Alignment != "i" || facts.Storage != "x" || facts.Kind != "b" || facts.Category != "U" || facts.Modifier != -1 || facts.Collation != 0 { t.Fatal("catalog facts",facts,ok) }
	v, err := d.Input(ctx,"foo*@%",-1)
	if err != nil { t.Fatal(err) }
	for _, value := range []sqltype.Value{v,sqltype.Null(d)} {
		if same, err := value.Retag(d); err != nil || same != value { t.Fatal("same generation",err) }
		for _, target := range []*sqltype.Descriptor{foreign,destination} {
			if _, err := value.Retag(target); err == nil { t.Fatal("foreign retag admitted") }
		}
		mapped, err := value.RemapLtxtquery(destination)
		if err != nil || mapped.Type() != destination || mapped.IsNull() != value.IsNull() { t.Fatal("authorized remap",err) }
		back, err := mapped.RemapLtxtquery(d)
		if err != nil || back != value { t.Fatal("remap copied immutable payload",err) }
	}
	var invalid sqltype.Value
	if invalid.ValidResult(d.Usage()) { t.Fatal("missing payload admitted") }
	if _, err := invalid.Retag(d); err == nil { t.Fatal("invalid datum retagged") }
	if _, err := invalid.RemapLtxtquery(d); err == nil { t.Fatal("invalid datum imported") }
	if _, err := d.Output(ctx,invalid); err == nil { t.Fatal("invalid payload output") }
	other, _ := d.Input(ctx,"bar",-1)
	if sqltype.SameDatum(v,other) { t.Fatal("distinct canonical bytes compare equal") }
	if same, err := v.SamePayload(ctx,other); err != nil || same { t.Fatal("distinct payload",same,err) }
	if equal, order := sqltype.ComparisonCapabilities(d); equal || order { t.Fatal("storage granted SQL comparison") }
	if _, err := sqltype.Array(65002,"_ltxtquery",d); err == nil { t.Fatal("storage granted executable array") }
	foreignValue,_:=foreign.Input(ctx,"foo*@%",-1)
	if _,err:=d.Output(ctx,foreignValue);err==nil {t.Fatal("foreign generation output admitted")}
	wrong,_:=sqltype.Ltree(65002,"ltree","public.ltree")
	if _,err:=v.RemapLtxtquery(wrong);err==nil {t.Fatal("cross-family remap admitted")}

}
