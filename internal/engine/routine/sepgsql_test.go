package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
)

// Classification is not admission: actual entry must separately prove the
// selected source lifetime and execute initialization before using this fact.
func TestSepgsqlSimulationRoutine(t *testing.T) {
	expected := map[ids.OID]bool{3: true, 330: true, 331: true, 332: true, 333: true, 334: true, 335: true, 3383: true, 3384: true, 3385: true, 3386: true, 2743: true, 2774: true, 2744: true, 3920: true, 3724: true, 3656: true, 3657: true, 3658: true, 2700: true, 3921: true, 3480: true, 3482: true, 3483: true, 3484: true, 3485: true, 3486: true, 3487: true, 3488: true, 3489: true}
	originals := NativeAccessMethodProcedures(authorization.PrincipalRef{})
	if len(originals) != len(expected) {
		t.Fatalf("original special declarations: got %d, want %d", len(originals), len(expected))
	}
	for _, original := range originals {
		if !expected[original.Address().ObjectID] {
			t.Fatalf("unexpected special original %v", original.Address())
		}
		delete(expected, original.Address().ObjectID)
		if !FmgrBuiltin(&original) {
			t.Fatalf("genuine original lost fmgr exemption: %v", original.Address())
		}
		metadata := original
		metadata.ObjectName = "renamed_original"
		metadata.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900001}
		metadata.Strict = !metadata.Strict
		metadata.Volatility = expr.Stable
		metadata.Display = catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage, Source: "mutable presentation"}
		if !FmgrBuiltin(&metadata) {
			t.Fatalf("mutable metadata revoked original fmgr identity: %v", original.Address())
		}
		badABI := original
		badABI.Signature = "25"
		if FmgrBuiltin(&badABI) {
			t.Fatalf("different ABI borrowed original exemption: %v", original.Address())
		}
		sqlReplacement := original
		sqlReplacement.SQL = &proc.Definition{}
		if FmgrBuiltin(&sqlReplacement) {
			t.Fatalf("SQL replacement borrowed original exemption: %v", original.Address())
		}
		alias := original
		alias.ObjectAddress.ObjectID = 900000
		if FmgrBuiltin(&alias) {
			t.Fatalf("fresh alias borrowed original exemption: %v", original.Address())
		}
		foreignClass := original
		foreignClass.ObjectAddress.ClassID = ids.TypeClassID
		if FmgrBuiltin(&foreignClass) {
			t.Fatalf("wrong class borrowed original exemption: %v", original.Address())
		}
		mixed := original
		mixed.BrinSupport = 1
		mixed.GinSupport = 1
		if FmgrBuiltin(&mixed) {
			t.Fatalf("mixed operation capabilities borrowed exemption: %v", original.Address())
		}
	}
	if len(expected) != 0 {
		t.Fatalf("missing original special declarations: %v", expected)
	}
	if FmgrBuiltin(nil) || FmgrBuiltin(&catalog.Procedure{}) {
		t.Fatal("empty declaration acquired original fmgr identity")
	}
}
