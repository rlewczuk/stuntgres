package enginetests_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

// TestStagedDefinitionOIDsMatchCatalogScan checks that the staged definition
// identity check accepts and rejects the same stages as the whole-catalog
// scan: stages that keep, add, move, reuse and duplicate identities.
func TestStagedDefinitionOIDsMatchCatalogScan(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE r(a INT CONSTRAINT r_a CHECK (a > 0), b INT CONSTRAINT r_b CHECK (b > 0))")
	enginetest.Run(t, e, "CREATE TABLE s(a INT)")
	base := e.CatalogRead()
	r := base.MustRelation(enginetest.RoleTableRef(t, e, "r").Address)
	s := base.MustRelation(enginetest.RoleTableRef(t, e, "s").Address)
	if len(r.Constraints.Check) != 2 || len(s.Constraints.Check) != 0 {
		t.Fatal(r.Constraints.Check, s.Constraints.Check)
	}
	used := r.Constraints.Check[0].OID
	// Far above every identity a fresh engine uses.
	const high ids.OID = 1 << 31
	// withChecks copies rel at oid (0 keeps its address) with CHECK
	// constraints that copy r's first one under the given identities.
	withChecks := func(rel *catalog.Relation, oid ids.OID, checks ...ids.OID) *catalog.Relation {
		c := *rel
		if oid != 0 {
			c.ObjectAddress.ObjectID = oid
		}
		c.Constraints.Check = nil
		for _, id := range checks {
			check := r.Constraints.Check[0]
			check.OID = id
			c.Constraints.Check = append(c.Constraints.Check, check)
		}
		return &c
	}
	rWithout := withChecks(r, 0, r.Constraints.Check[1].OID)
	cases := []struct {
		name    string
		staged  []*catalog.Relation
		invalid bool
	}{
		{"keep", []*catalog.Relation{withChecks(r, 0, used, r.Constraints.Check[1].OID)}, false},
		{"add above ceiling", []*catalog.Relation{withChecks(s, high, high+1)}, false},
		{"replace with new identity", []*catalog.Relation{withChecks(s, 0, high)}, false},
		{"move from replaced owner", []*catalog.Relation{rWithout, withChecks(s, 0, used)}, false},
		{"reuse unchanged owner's identity", []*catalog.Relation{withChecks(s, 0, used)}, true},
		{"claim an unchanged address", []*catalog.Relation{withChecks(s, 0, r.Address().ObjectID)}, true},
		{"new address on an owned identity", []*catalog.Relation{withChecks(s, used)}, true},
		{"duplicate staged identities", []*catalog.Relation{withChecks(s, 0, high, high)}, true},
		{"staged identity on a staged address", []*catalog.Relation{withChecks(s, high), withChecks(r, 0, used, high)}, true},
		{"zero identity", []*catalog.Relation{withChecks(s, 0, 0)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := catalog.NewCandidate(base)
			for _, d := range tc.staged {
				c.UpdateDescriptor(d)
			}
			read := base.WithState(c.State())
			full, staged := catalog.ValidateDefinitionOIDs(read), c.ValidateStagedDefinitionOIDs(read)
			if (full != nil) != tc.invalid || (staged != nil) != tc.invalid {
				t.Fatalf("scan %v, staged %v, want invalid=%t", full, staged, tc.invalid)
			}
		})
	}
}

// TestOIDAllocationAroundCeiling allocates from cursors at used identities,
// below the catalog's highest one, and above it.
func TestOIDAllocationAroundCeiling(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE r(a INT CONSTRAINT r_a CHECK (a > 0))")
	r := e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "r").Address)
	owned := r.Constraints.Check[0].OID
	e.Mu.Lock()
	defer e.Mu.Unlock()
	taken := []ids.OID{r.Address().ObjectID, owned}
	for _, start := range taken {
		cursor := start
		got, err := e.Catalog.AllocateOIDFromLocked(&cursor, 0)
		if err != nil || slices.Contains(taken, got) || got <= start {
			t.Fatal("allocated a used identity", start, got, err)
		}
	}
	cursor := ids.OID(1 << 31)
	if got, err := e.Catalog.AllocateOIDFromLocked(&cursor, 0); err != nil || got != 1<<31 || cursor != got+1 {
		t.Fatal("allocation above the ceiling", got, cursor, err)
	}
}
