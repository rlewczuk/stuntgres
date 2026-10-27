package control

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

func TestGenericFileListACLReplayBaseline(t *testing.T) {
	domain := new(authorization.Domain)
	seed := authorization.Seed(domain)
	roles := seed.Read(domain.Principal(10))
	owner, monitor := domain.Principal(10), roles.LookupRef("pg_monitor")
	// Independent system_functions.sql facts, not SeedGenericFileACLs output.
	for _, tc := range []struct {
		oid     ids.OID
		monitor bool
	}{
		{2625, false}, {3297, false}, {3353, true}, {3354, true},
		{5031, true}, {6270, true}, {6271, true}, {6272, true}, {6400, true},
		{3826, false}, {6208, false}, {2624, false}, {3293, false},
		{3828, false}, {6209, false}, {3827, false}, {3295, false},
		{2623, false}, {3307, false}, {5029, true}, {5030, true},
	} {
		var original catalog.Procedure
		for _, p := range routine.BuiltinProcedures(owner) {
			if p.Address().ObjectID == tc.oid {
				original = p
				break
			}
		}
		if original.Builtin == nil {
			t.Fatal("missing callable declaration", tc.oid)
		}
		original.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{
			Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege,
		}}}
		if tc.monitor {
			original.ACL.Entries = append(original.ACL.Entries, authorization.ACLEntry{
				Grantor: owner, Grantee: authorization.ACLGrantee{Role: monitor}, Privileges: authorization.ExecutePrivilege,
			})
		}
		t.Run(string(original.Signature)+"/"+original.Name(), func(t *testing.T) {
			if !genericFileACLReconstructed(&original, owner, monitor) {
				t.Fatal("exact seed must be elided", tc.oid)
			}
			changes := map[string]func(*catalog.Procedure){
				"null":  func(p *catalog.Procedure) { p.ACL = authorization.ACLValue{} },
				"empty": func(p *catalog.Procedure) { p.ACL.Entries = nil },
				"default-equivalent": func(p *catalog.Procedure) {
					p.ACL.Entries = append(p.ACL.Entries[:1], authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege})
				},
				"duplicate": func(p *catalog.Procedure) { p.ACL.Entries = append(p.ACL.Entries, p.ACL.Entries[0]) },
				"options":   func(p *catalog.Procedure) { p.ACL.Entries[0].GrantOptions = authorization.ExecutePrivilege },
				"grantor":   func(p *catalog.Procedure) { p.ACL.Entries[0].Grantor = monitor },
				"grantee-domain": func(p *catalog.Procedure) {
					p.ACL.Entries[0].Grantee.Role = new(authorization.Domain).Principal(owner.ID())
				},
				"owner":               func(p *catalog.Procedure) { p.Owner = monitor },
				"namespace":           func(p *catalog.Procedure) { p.ObjectNamespace.ObjectID = ids.PublicNamespaceOID },
				"name":                func(p *catalog.Procedure) { p.ObjectName = "changed" },
				"signature":           func(p *catalog.Procedure) { p.Signature = "16" },
				"class":               func(p *catalog.Procedure) { p.ObjectAddress.ClassID = ids.RelationClassID },
				"identity":            func(p *catalog.Procedure) { p.ObjectAddress.ObjectID = 5052 },
				"declaration":         func(p *catalog.Procedure) { p.Builtin = routine.BuiltinRoutine(5052) },
				"missing-declaration": func(p *catalog.Procedure) { p.Builtin = nil },
			}
			if tc.monitor {
				changes["order"] = func(p *catalog.Procedure) { p.ACL.Entries[0], p.ACL.Entries[1] = p.ACL.Entries[1], p.ACL.Entries[0] }
			}
			for name, change := range changes {
				t.Run(name, func(t *testing.T) {
					p := original
					p.ACL.Entries = slices.Clone(original.ACL.Entries)
					change(&p)
					if genericFileACLReconstructed(&p, owner, monitor) {
						t.Fatal("changed raw authority discarded", tc.oid, name)
					}
				})
			}
		})
	}
}
