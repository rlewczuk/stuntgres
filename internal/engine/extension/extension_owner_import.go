package extension

import (
	"context"
	"fmt"
	"strings"

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
		if record.Name == "earthdistance" {
			if ext.Version != "1.2" || len(record.Members) != 2 || len(ext.Members) != 2 {
				return false, fmt.Errorf("stuntgres: invalid earthdistance ownership roles")
			}
			first, second := record.Members[0], record.Members[1]
			if first.Kind != "type" || second.Kind != "type" || first.Signature != "" || second.Signature != "" || first.Owner != second.Owner || !(first.Name == "earth" && second.Name == "earth[]" || first.Name == "earth[]" && second.Name == "earth") {
				return false, fmt.Errorf("stuntgres: inconsistent earthdistance ownership roles")
			}
		}
		if record.Name == "cube" && (len(record.Members) != CubeMemberCount || len(ext.Members) != CubeMemberCount) {
			return false, fmt.Errorf("stuntgres: incomplete cube ownership manifest")
		}
		membersSeen := map[ids.ObjectAddress]bool{}
		for _, member := range record.Members {
			owner, ok := roles.Lookup(member.Owner)
			if !ok {
				return false, fmt.Errorf("stuntgres: extension member owner %q does not exist", member.Owner)
			}
			var class ids.OID
			switch member.Kind {
			case "procedure":
				class = ids.ProcedureClassID
			case "dictionary":
				class = ids.TSDictionaryClassID
			case "type":
				class = ids.TypeClassID
			case "operator":
				class = ids.OperatorClassID
			case "operator_class":
				class = ids.OperatorClassCatalogID
			case "operator_family":
				class = ids.OperatorFamilyClassID
			default:
				return false, fmt.Errorf("stuntgres: invalid extension ownership kind")
			}
			signature := member.Signature
			var expectedCubeMember ids.ObjectAddress
			if member.Kind == "procedure" && (record.Name == "intarray" && member.Name == "bqarr_out" || record.Name == "citext" && member.Name == "citextout") {
				base, ok := candidate.Type(ext.Members[0])
				if !ok || record.Name == "intarray" && !base.SQLType.IsQueryInt() || record.Name == "citext" && !base.SQLType.IsCitext() {
					return false, fmt.Errorf("stuntgres: missing installed owner type")
				}
				signature = string(QueryIntSignature(base.SQLType.ID()))
			}
			if record.Name == "cube" && (member.Kind == "procedure" || member.Kind == "operator") {
				base, ok := candidate.Type(ext.Members[0])
				if !ok || !base.SQLType.IsCube() {
					return false, fmt.Errorf("stuntgres: missing cube ownership type")
				}
				parts := strings.Split(signature, ",")
				for i, part := range parts {
					if part == "cube" {
						parts[i] = string(CubeSignature(base.SQLType.ID()))
					}
				}
				signature = strings.Join(parts, ",")
				valid := false
				if member.Kind == "procedure" {
					for i, spec := range cubeRoutines {
						if spec.name == member.Name && member.Signature == spec.ownerSignature() && string(spec.signature(base.SQLType.ID())) == signature {
							valid = true
							expectedCubeMember = ext.Members[cubeRoutineMember(i)]
							break
						}
					}
				} else if member.Signature == "cube,cube" {
					for i, name := range cubeOperatorNames {
						if member.Name == name {
							valid = true
							expectedCubeMember = ext.Members[10+i]
							break
						}
					}
				}
				if member.Signature == "cube,cube" {
					for i, spatial := range cubeSpatialMembers {
						if member.Kind == "procedure" && member.Name == spatial.operation.Routine() {
							valid = true
							expectedCubeMember = ext.Members[cubeSpatialProcedureStart+i]
						} else if member.Kind == "operator" && member.Name == spatial.operator {
							valid = true
							expectedCubeMember = ext.Members[cubeSpatialOperatorStart+i]
						}
					}
				}
				if !valid {
					return false, fmt.Errorf("stuntgres: invalid cube ownership signature")
				}
			}
			if member.Kind == "operator_class" || member.Kind == "operator_family" {
				if record.Name != "cube" || member.Name != "cube_ops" || member.Signature != "btree" {
					return false, fmt.Errorf("stuntgres: invalid extension ownership method")
				}
				signature = "403"
				if member.Kind == "operator_class" {
					expectedCubeMember = ext.Members[17]
				} else {
					expectedCubeMember = ext.Members[16]
				}
			}
			a := candidate.NameAddress(ids.NameKey{ClassID: class, Namespace: ext.TargetNamespace, Name: member.Name, Signature: signature})
			if record.Name == "earthdistance" && member.Name == "earth[]" {
				domain, ok := candidate.Type(ext.Members[0])
				if !ok || !domain.SQLType.IsDomain() {
					return false, fmt.Errorf("stuntgres: missing earth ownership domain")
				}
				a = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(domain.Array)}
				array, ok := candidate.Type(a)
				if !ok || array.SQLType.Element() != domain.SQLType || array.Namespace() != domain.Namespace() || a != ext.Members[1] {
					return false, fmt.Errorf("stuntgres: invalid earth ownership companion")
				}
			}
			d := candidate.Get(a)
			if d == nil || catalog.ExtensionMembership(d) != ext.Address() || membersSeen[a] || expectedCubeMember != (ids.ObjectAddress{}) && a != expectedCubeMember {
				return false, fmt.Errorf("stuntgres: invalid extension ownership member")
			}
			membersSeen[a] = true
			switch d := d.(type) {
			case *catalog.Type:
				if d.Owner != owner.Ref {
					n := *d
					n.Owner = owner.Ref
					candidate.SetDescriptor(a, &n)
					if d.Array != 0 {
						arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Array)}
						array, ok := candidate.Type(arrayAddress)
						if !ok || !array.SQLType.IsArray() || array.SQLType.Element() != d.SQLType {
							return false, fmt.Errorf("stuntgres: invalid owned type array")
						}
						mapped := *array
						mapped.Owner = owner.Ref
						candidate.SetDescriptor(arrayAddress, &mapped)
					}
					ownersChanged = true
				}
			case *catalog.Procedure:
				if d.Owner != owner.Ref {
					n := *d
					n.Owner = owner.Ref
					candidate.SetDescriptor(a, &n)
					ownersChanged = true
				}
			case *catalog.Operator:
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
			case *catalog.OperatorClass:
				if d.Owner != owner.Ref {
					n := *d
					n.Owner = owner.Ref
					candidate.SetDescriptor(a, &n)
					ownersChanged = true
				}
			case *catalog.OperatorFamily:
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
