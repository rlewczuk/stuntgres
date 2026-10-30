package control

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

func TestServerSignalACLReconstruction(t *testing.T) {
	owner := authorization.PrincipalRef{}
	procedures := routine.BuiltinProcedures(owner)
	routine.SeedServerSignalACLs(procedures, owner)
	seen := make([]ids.OID, 0, 3)
	for _, p := range procedures {
		if !routine.RestrictedServerSignalRoutine(p.Address().ObjectID) {
			if serverSignalACLReconstructed(&p, owner) { t.Fatal("unrelated declaration omitted", p.Name()) }
			continue
		}
		seen = append(seen, p.Address().ObjectID)
		if !serverSignalACLReconstructed(&p, owner) { t.Fatal("pristine seed not reconstructed", p.Name()) }
		for _, mutation := range []string{"null", "empty", "public", "duplicate", "mask", "grant-option", "name", "signature", "namespace", "class", "subid", "provider"} {
			changed := p
			changed.ACL.Entries = slices.Clone(p.ACL.Entries)
			switch mutation {
			case "null": changed.ACL = authorization.ACLValue{}
			case "empty": changed.ACL.Entries = nil
			case "public": changed.ACL.Entries[0].Grantee = authorization.ACLGrantee{Public: true}
			case "duplicate": changed.ACL.Entries = append(changed.ACL.Entries, changed.ACL.Entries[0])
			case "mask": changed.ACL.Entries[0].Privileges = 0
			case "grant-option": changed.ACL.Entries[0].GrantOptions = authorization.ExecutePrivilege
			case "name": changed.ObjectName = "renamed"
			case "signature": changed.Signature = "25"
			case "namespace": changed.ObjectNamespace.ObjectID = ids.PublicNamespaceOID
			case "class": changed.ObjectAddress.ClassID = ids.RelationClassID
			case "subid": changed.ObjectAddress.SubID = 1
			case "provider": changed.Builtin = nil
			}
			if serverSignalACLReconstructed(&changed, owner) { t.Fatal("changed raw authority omitted", p.Name(), mutation) }
			if !serverSignalACLReconstructed(&p, owner) { t.Fatal("mutation changed original seed") }
		}
	}
	slices.Sort(seen)
	if !slices.Equal(seen, []ids.OID{2621, 2622, 4543}) { t.Fatal("exact restricted reconstructed membership", seen) }
}
