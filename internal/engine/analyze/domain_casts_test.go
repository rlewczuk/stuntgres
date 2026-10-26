package analyze

import "testing"

func TestDomainIdentityCaptureRetirement(t *testing.T) {
	domain := PreparedTypeKey{Offset: 4, Kind: DomainCast}
	composite := PreparedTypeKey{Offset: 8, Kind: CompositeExpansion}
	typeName := PreparedTypeKey{Offset: 12, Kind: TypeNameCast}
	identity := PreparedTypeKey{Offset: 16, Kind: IdentityCast}
	shared := map[PreparedTypeKey]TypeOccurrence{domain: {Kind: DomainCast}, composite: {Kind: CompositeExpansion}, typeName: {Kind: TypeNameCast}, identity: {Kind: IdentityCast}}
	left, retired := RetireRefreshableTypes(shared)
	if _, ok := left[domain]; ok {
		t.Fatal("domain capture retained after replan")
	}
	if _, ok := left[typeName]; ok {
		t.Fatal("typename capture retained after replan")
	}
	if _, ok := left[identity]; ok {
		t.Fatal("user identity cast retained after replan")
	}
	if _, ok := left[composite]; !ok || len(shared) != 4 || !retired {
		t.Fatal("composite/shared captures changed or retirement not reported")
	}
	if again, retired := RetireRefreshableTypes(left); retired || len(again) != 1 {
		t.Fatal("retiring without casts changed the captures", again, retired)
	}
	left = PublishRefreshableTypes(left, map[PreparedTypeKey]TypeOccurrence{domain: {Kind: DomainCast}, typeName: {Kind: TypeNameCast}, identity: {Kind: IdentityCast}})
	if len(left) != 4 || len(shared) != 4 {
		t.Fatal("successful reanalysis lost unrelated occurrence")
	}
	// Successful candidates replace this cast group without mutating a sibling's
	// captured map. A later failed candidate cannot revive the retired group.
	left, _ = RetireRefreshableTypes(left)
	left = PublishRefreshableTypes(left, map[PreparedTypeKey]TypeOccurrence{typeName: {Kind: TypeNameCast}})
	if _, ok := left[domain]; ok || len(left) != 2 || len(shared) != 4 {
		t.Fatal("cast recapture revived a retired occurrence or mutated sibling")
	}
}
