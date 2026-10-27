package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func TestBuiltinRoutineCosts(t *testing.T) {
	// Independently transcribed explicit costs for represented declarations from
	// REL_18_6 pg_proc.dat and system_functions.sql (including SQL COST 1).
	// All other represented declarations retain their actual language default.
	explicit := map[ids.OID]float32{
		879: 1, 880: 1, 1215: 100, 1216: 100, 1296: 1, 1298: 1,
		1305: 1, 1306: 1, 1307: 1, 1309: 1, 1310: 1, 1311: 1,
		1348: 100, 1384: 1, 1426: 1, 1481: 1, 1544: 1,
		1708: 1, 1710: 1, 1741: 1, 1810: 1, 1811: 1, 1812: 1,
		1848: 1, 1993: 100, 2042: 1, 2043: 1, 2044: 1, 2059: 1,
		2074: 1, 2079: 10, 2080: 10, 2093: 10,
		2546: 1, 2547: 1, 2548: 1, 2549: 1, 2550: 1, 2631: 1,
		3024: 10, 3025: 10, 3026: 10, 3027: 10, 3028: 10, 3029: 10,
		3084: 10, 3445: 100, 3448: 100, 3685: 100, 3745: 100, 3815: 10,
		3935: 1, 3936: 1, 5023: 1, 6159: 10, 6212: 1, 6249: 100,
		6348: 10, 6349: 10, 6350: 10,
	}
	for _, p := range BuiltinProcedures(authorization.PrincipalRef{}) {
		id := p.Address().ObjectID
		want, explicitCost := explicit[id]
		if !explicitCost {
			want = 1
			if p.Display.Language.String() == "SQL" || p.Display.Language.String() == "PLPGSQL" {
				want = 100
			}
		}
		if p.Cost != want {
			t.Fatalf("declaration %d (%s) cost = %g, want %g", id, p.Name(), p.Cost, want)
		}
		if got := BuiltinRoutineCost(p.Builtin); got != want {
			t.Fatalf("declaration %d immutable cost = %g, want %g", id, got, want)
		}
		// Presentation changes copy the declaration metadata, not a name lookup.
		renamed := p
		renamed.ObjectName = "renamed_cost_control"
		renamed.ObjectNamespace.ObjectID = 99999
		if renamed.Display != p.Display || renamed.Builtin != p.Builtin {
			t.Fatalf("declaration %d lost metadata through identity-preserving copy", id)
		}
		delete(explicit, id)
	}
	if len(explicit) != 0 {
		t.Fatalf("explicit-cost declarations were not materialized: %v", explicit)
	}
	for i := range sharedMemoryDeclarations {
		if got := BuiltinRoutineCost(&sharedMemoryDeclarations[i]); got != 1 {
			t.Fatalf("view-authority declaration %d cost = %g, want 1", sharedMemoryDeclarations[i].Identity.ID, got)
		}
	}
}
