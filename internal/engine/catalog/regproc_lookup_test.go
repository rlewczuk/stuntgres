package catalog

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestBuiltinRoutineResultInventory(t *testing.T) {
	// Independently extracted fixed pg_proc facts, not expectations computed
	// from builtinRoutineIdentities or its indexes. Only preexisting lookup
	// identities are included; the 16 additional oracle identities stay absent.
	data, err := os.ReadFile("testdata/builtin_routine_results_pg18_6.tsv")
	if err != nil {
		t.Fatal(err)
	}
	identities := BuiltinRoutineIdentities()
	if len(identities) != 3397 {
		t.Fatalf("lookup identity count = %d, want 3397", len(identities))
	}
	seen := make(map[ids.OID]bool, len(identities))
	for _, identity := range identities {
		if seen[identity.ID] {
			t.Fatalf("duplicate lookup OID %d", identity.ID)
		}
		seen[identity.ID] = true
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			t.Fatalf("invalid independent fixture row %q", line)
		}
		parse := func(raw string) ids.OID {
			n, err := strconv.ParseUint(raw, 10, 32)
			if err != nil {
				t.Fatal(err)
			}
			return ids.OID(n)
		}
		id, namespace, result := parse(fields[0]), parse(fields[1]), parse(fields[4])
		want := RoutineIdentity{ID: id, Namespace: namespace, Name: fields[2], Signature: ProcedureSignature(fields[3])}
		if !seen[id] {
			t.Fatalf("oracle identity missing from lookup inventory: %+v", want)
		}
		delete(seen, id)
		if got, ok := BuiltinRoutineIdentityByOID(id); !ok || got != want {
			t.Fatalf("OID %d identity = %+v/%v, want %+v", id, got, ok, want)
		}
		if got, ok := BuiltinRoutineIdentityBySignature(want.Name, want.Signature); !ok || got != want {
			t.Fatalf("OID %d signature identity = %+v/%v, want %+v", id, got, ok, want)
		}
		if named := BuiltinRoutineIdentitiesNamed(want.Name); !slices.Contains(named, want) {
			t.Fatalf("OID %d missing from named identities", id)
		}
		if got, ok := BuiltinRoutineResultByOID(id); !ok || got == 0 || got != result {
			t.Fatalf("OID %d result = %d/%v, want %d", id, got, ok, result)
		}
		count++
	}
	if count != 3397 || len(seen) != 0 {
		t.Fatalf("independent fixture covered %d identities, uncovered = %v", count, seen)
	}
	for _, id := range []ids.OID{0, 13168, 13230, 13580, 16384, ^ids.OID(0)} {
		if result, ok := BuiltinRoutineResultByOID(id); ok || result != 0 {
			t.Fatalf("absent OID %d result = %d/%v", id, result, ok)
		}
	}
	// Returning richer private rows must not change the identity-only copy API.
	first := identities[0]
	identities[0] = RoutineIdentity{}
	if got := BuiltinRoutineIdentities()[0]; got != first {
		t.Fatalf("identity copy mutation escaped: %+v", got)
	}
	named := BuiltinRoutineIdentitiesNamed(first.Name)
	named[0] = RoutineIdentity{}
	if got := BuiltinRoutineIdentitiesNamed(first.Name)[0]; got != first {
		t.Fatalf("named copy mutation escaped: %+v", got)
	}
}
