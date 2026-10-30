package control

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"testing"
)

func TestReplicationOriginStateACLReconstruction(t *testing.T) {
	owner := authorization.PrincipalRef{}
	procedures := routine.BuiltinProcedures(owner)
	routine.SeedReplicationOriginACLs(procedures, owner)
	var seen []ids.OID
	for _, p := range procedures {
		if !routine.RestrictedReplicationOriginRoutine(p.Address().ObjectID) {
			if recoveryControlACLReconstructed(&p, owner) {
				t.Fatal("unrelated declaration omitted", p.Name())
			}
			continue
		}
		seen = append(seen, p.Address().ObjectID)
		if !recoveryControlACLReconstructed(&p, owner) {
			t.Fatal("pristine seed not reconstructed", p.Name())
		}
		for _, mutation := range []string{"null", "empty", "public", "duplicate", "mask", "grant_option", "name", "signature", "namespace", "class", "subid", "provider", "oid"} {
			changed := p
			changed.ACL.Entries = slices.Clone(p.ACL.Entries)
			switch mutation {
			case "null":
				changed.ACL = authorization.ACLValue{}
			case "empty":
				changed.ACL.Entries = nil
			case "public":
				changed.ACL.Entries[0].Grantee = authorization.ACLGrantee{Public: true}
			case "duplicate":
				changed.ACL.Entries = append(changed.ACL.Entries, changed.ACL.Entries[0])
			case "mask":
				changed.ACL.Entries[0].Privileges = 0
			case "grant_option":
				changed.ACL.Entries[0].GrantOptions = authorization.ExecutePrivilege
			case "name":
				changed.ObjectName = "changed"
			case "signature":
				changed.Signature = "23"
			case "namespace":
				changed.ObjectNamespace.ObjectID = ids.PublicNamespaceOID
			case "class":
				changed.ObjectAddress.ClassID = ids.RelationClassID
			case "subid":
				changed.ObjectAddress.SubID = 1
			case "provider":
				changed.Builtin = nil
			case "oid":
				changed.ObjectAddress.ObjectID = 6006
			}
			if recoveryControlACLReconstructed(&changed, owner) {
				t.Fatal("changed authority omitted", p.Name(), mutation)
			}
			if !recoveryControlACLReconstructed(&p, owner) {
				t.Fatal("mutation changed original seed")
			}
		}
	}
	slices.Sort(seen)
	if !slices.Equal(seen, []ids.OID{6003, 6004, 6005, 6012, 6013}) {
		t.Fatal("restricted origin roster", seen)
	}
}
