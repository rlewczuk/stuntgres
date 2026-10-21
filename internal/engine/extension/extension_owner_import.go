package extension

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ImportOwners applies Restore's extension owner records to
// candidate, a private clone. Each record must name an installed extension
// once, with its version, schema and installation owner; each member must
// belong to it. It reports whether any member owner changed.
func ImportOwners(ctx context.Context, candidate *catalog.State, roles authorization.Read, records []OwnerRecord) (bool, error) {
	seen := map[ids.ObjectAddress]bool{}
	ownersChanged := false
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		a := candidate.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: record.Name})
		ext, ok := candidate.Extension(a)
		if !ok || seen[a] || ext.Version != record.Version {
			return false, fmt.Errorf("stuntgres: invalid extension owner import target")
		}
		seen[a] = true
		ns, ok := candidate.Namespace(ext.TargetNamespace)
		if !ok || ns.Name() != record.Schema {
			return false, fmt.Errorf("stuntgres: mismatching extension owner namespace")
		}
		owner, ok := roles.Lookup(record.Owner)
		if !ok || owner.Ref != ext.Owner {
			return false, fmt.Errorf("stuntgres: mismatching extension installation owner")
		}
		for _, member := range record.Members {
			owner, ok := roles.Lookup(member.Owner)
			if !ok {
				return false, fmt.Errorf("stuntgres: extension member owner %q does not exist", member.Owner)
			}
			class := ids.ProcedureClassID
			if member.Kind == "dictionary" {
				class = ids.TSDictionaryClassID
			}
			if member.Kind == "type" {
				class = ids.TypeClassID
			}
			signature := member.Signature
			if member.Kind == "procedure" && (record.Name == "intarray" && member.Name == "bqarr_out" || record.Name == "cube" && member.Name == "cube_out") {
				base, ok := candidate.Type(ext.Members[0])
				if !ok || record.Name == "intarray" && !base.SQLType.IsQueryInt() || record.Name == "cube" && !base.SQLType.IsCube() {
					return false, fmt.Errorf("stuntgres: missing installed owner type")
				}
				signature = string(QueryIntSignature(base.SQLType.ID()))
			}
			a := candidate.NameAddress(ids.NameKey{ClassID: class, Namespace: ext.TargetNamespace, Name: member.Name, Signature: signature})
			d := candidate.Get(a)
			if d == nil || catalog.ExtensionMembership(d) != ext.Address() {
				return false, fmt.Errorf("stuntgres: invalid extension ownership member")
			}
			switch d := d.(type) {
			case *catalog.Type:
				if d.Owner != owner.Ref {
					n := *d
					n.Owner = owner.Ref
					candidate.SetDescriptor(a, &n)
					arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Array)}
					array, ok := candidate.Type(arrayAddress)
					if !ok || !array.SQLType.IsArray() || array.SQLType.Element() != d.SQLType {
						return false, fmt.Errorf("stuntgres: invalid owned intarray array")
					}
					mapped := *array
					mapped.Owner = owner.Ref
					candidate.SetDescriptor(arrayAddress, &mapped)
					ownersChanged = true
				}
			case *catalog.Procedure:
				if d.Owner != owner.Ref {
					n := *d
					n.Owner = owner.Ref
					candidate.SetDescriptor(a, &n)
					ownersChanged = true
				}
			case *catalog.TSDictionary:
				if d.Owner != owner.Ref {
					n := *d
					n.Owner = owner.Ref
					candidate.SetDescriptor(a, &n)
					ownersChanged = true
				}
			default:
				return false, fmt.Errorf("stuntgres: ownerless extension metadata member")
			}
		}
	}
	return ownersChanged, nil
}
