package extension

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ImportOwners applies Restore's extension owner records to candidateView's
// private cloned state. Its real catalog owner and paired role domain retain
// provider/provenance authority while records change member ownership.
func ImportOwners(ctx context.Context, candidateView catalog.Read, roles authorization.Read, records []OwnerRecord) (bool, error) {
	if len(records) == 0 {
		return false, nil
	}
	candidate := candidateView.State()
	if candidate == nil || candidateView.Owner() == nil || !roles.BelongsTo(candidateView.Owner().Principals) {
		return false, fmt.Errorf("stuntgres: invalid extension owner import authority")
	}
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
		if record.Name == "earthdistance" && (len(ext.Members) != EarthMemberCount || !validEarthOwnerRoles(record)) {
			return false, fmt.Errorf("stuntgres: invalid earthdistance ownership roles")
		}
		if record.Name == "lo" {
			if ext.Version != "1.2" || len(record.Members) != 2 || len(ext.Members) != 2 || record.Members[0].Owner != record.Members[1].Owner {
				return false, fmt.Errorf("stuntgres: inconsistent lo ownership roles")
			}
			for _, member := range record.Members {
				if member.Slot < 0 || member.Slot >= 2 || member.Kind != "type" || member.Signature != "" || member.Name != [...]string{"lo", "lo[]"}[member.Slot] {
					return false, fmt.Errorf("stuntgres: invalid lo ownership role")
				}
			}
		}
		if record.Name == "cube" && (len(record.Members) != CubeMemberCount || len(ext.Members) != CubeMemberCount) {
			return false, fmt.Errorf("stuntgres: incomplete cube ownership manifest")
		}
		expected := len(ext.Members)
		if record.Name == "unaccent" {
			expected--
		}
		if len(record.Members) != expected {
			return false, fmt.Errorf("stuntgres: incomplete extension owners")
		}
		memberSlots := make(map[int]bool, expected)
		membersSeen := make(map[ids.ObjectAddress]bool, expected)
		for i, member := range record.Members {
			// Reown lo's domain/companion pair together before validating the
			// companion slot, even when a decoded record lists that slot first.
			// Do not reorder or mutate the caller's immutable metadata slice.
			if record.Name == "lo" && record.Members[0].Slot == 1 {
				member = record.Members[1-i]
			}
			owner, ok := roles.Lookup(member.Owner)
			if !ok {
				return false, fmt.Errorf("stuntgres: extension member owner %q does not exist", member.Owner)
			}
			if member.Slot < 0 || member.Slot >= len(ext.Members) || memberSlots[member.Slot] {
				return false, fmt.Errorf("stuntgres: invalid extension ownership slot")
			}
			memberSlots[member.Slot] = true
			a := ext.Members[member.Slot]
			d := candidate.Get(a)
			if d == nil || d.Address() != a || catalog.ExtensionMembership(d) != ext.Address() || membersSeen[a] {
				return false, fmt.Errorf("stuntgres: invalid extension ownership member")
			}
			membersSeen[a] = true
			namespace, ok := candidate.Namespace(d.Namespace())
			if !ok || namespace.Name() != member.Schema {
				return false, fmt.Errorf("stuntgres: mismatching member namespace")
			}
			name, signature, kind := d.Name(), "", ""
			switch selected := d.(type) {
			case *catalog.Procedure:
				symbol, err := ProcedureSymbol(candidateView, selected)
				if err != nil {
					return false, err
				}
				kind, signature = "procedure", string(selected.Signature)
				if IntarrayProviderOf(selected) != nil && symbol == "bqarr_out" {
					signature = "query_int"
				}
				if CubeProviderOf(selected) != nil || EarthdistanceProviderOf(selected) != nil {
					var valid bool
					signature, valid = ownerProcedureSignature(record.Name, record.Version, member.Slot)
					if !valid {
						return false, fmt.Errorf("stuntgres: invalid cube ownership signature")
					}
				}
				if CitextProviderOf(selected) != nil && symbol == "citextout" {
					signature = "citext"
				}
				if IsnProviderOf(selected) != nil && symbol == "ean13_out" {
					signature = "ean13"
				}
			case *catalog.Operator:
				if record.Name == "earthdistance" {
					if member.Slot != EarthPointOperatorSlot || selected.Address() != ext.Members[EarthPointOperatorSlot] {
						return false, fmt.Errorf("stuntgres: invalid earth operator ownership slot")
					}
					if err := StockEarthdistance.validatePoint(candidateView, ext); err != nil {
						return false, err
					}
					kind, signature = "operator", "600,600"
					break
				}
				expectedName, portableSignature, valid := cubeOwnerOperatorIdentity(member.Slot)
				if record.Name != "cube" || !valid {
					return false, fmt.Errorf("stuntgres: invalid extension operator owner")
				}
				base, ok := candidate.Type(ext.Members[0])
				if !ok || base.SQLType == nil || !base.SQLType.IsCube() || selected.Namespace() != ext.TargetNamespace || selected.Name() != expectedName || selected.Left != ids.OID(base.SQLType.ID()) || candidate.NameAddress(catalog.DescriptorNameKey(selected)) != selected.Address() {
					return false, fmt.Errorf("stuntgres: invalid cube ownership signature")
				}
				switch {
				case member.Slot >= cubeCoordinateOperatorStart && member.Slot < cubeSpatialProcedureStart:
					i := member.Slot - cubeCoordinateOperatorStart
					backing := ext.Members[cubeRoutineMember(len(cubeRoutines)-len(cubeCoordinateOperators)+i)]
					if selected.Right != ids.OID(sqltype.Int4) || selected.Result != ids.OID(sqltype.Float8) || selected.Function != backing.ObjectID || selected.Commutator != 0 || selected.Negator != 0 || selected.Restriction != 0 || selected.Join != 0 || selected.Hash || selected.Merge {
						return false, fmt.Errorf("stuntgres: invalid cube ownership signature")
					}
				case member.Slot >= cubeSpatialOperatorStart:
					spatial := cubeSpatialMembers[member.Slot-cubeSpatialOperatorStart]
					if selected.Right != selected.Left || selected.Result != ids.OID(spatial.operation.ResultID()) || selected.Function != ext.Members[cubeSpatialProcedureStart+member.Slot-cubeSpatialOperatorStart].ObjectID {
						return false, fmt.Errorf("stuntgres: invalid cube ownership signature")
					}
				default:
					i := member.Slot - 10
					if selected.Right != selected.Left || selected.Result != 16 || selected.Function != ext.Members[i+3].ObjectID || selected.Commutator != ext.Members[10+cubeCommutators[i]].ObjectID || selected.Negator != ext.Members[10+cubeNegators[i]].ObjectID || selected.Hash || selected.Merge != (i == 0) || selected.Restriction != cubeRestrictions[i] || selected.Join != cubeJoins[i] {
						return false, fmt.Errorf("stuntgres: invalid cube ownership signature")
					}
				}
				kind, signature = "operator", portableSignature
			case *catalog.OperatorClass:
				base, ok := candidate.Type(ext.Members[0])
				if record.Name != "cube" || member.Slot != 17 || !ok || base.SQLType == nil || selected.Namespace() != ext.TargetNamespace || selected.Name() != "cube_ops" || selected.Method != 403 || selected.Family != ext.Members[16].ObjectID || selected.InputType != ids.OID(base.SQLType.ID()) || selected.StorageType != 0 || !selected.Default || candidate.NameAddress(catalog.DescriptorNameKey(selected)) != selected.Address() {
					return false, fmt.Errorf("stuntgres: invalid extension ownership method")
				}
				kind, signature = "operator_class", "btree"
			case *catalog.OperatorFamily:
				if record.Name != "cube" || member.Slot != 16 || selected.Namespace() != ext.TargetNamespace || selected.Name() != "cube_ops" || selected.Method != 403 || candidate.NameAddress(catalog.DescriptorNameKey(selected)) != selected.Address() {
					return false, fmt.Errorf("stuntgres: invalid extension ownership method")
				}
				kind, signature = "operator_family", "btree"
			case *catalog.Type:
				kind = "type"
				var err error
				name, err = MemberTypeRole(candidateView, ext, selected)
				if err != nil {
					return false, err
				}
				if (record.Name == "earthdistance" || record.Name == "lo") && selected.SQLType.IsArray() {
					domain, ok := candidate.Type(ext.Members[0])
					if !ok || !domain.SQLType.IsDomain() || a != ext.Members[1] || ids.OID(domain.Array) != a.ObjectID || selected.SQLType.Element() != domain.SQLType || selected.Namespace() != domain.Namespace() {
						return false, fmt.Errorf("stuntgres: invalid installed domain ownership companion")
					}
				}
			case *catalog.TSDictionary:
				kind = "dictionary"
			}
			if member.Name != name || member.Signature != signature || member.Kind != kind || kind == "" {
				return false, fmt.Errorf("stuntgres: mismatching extension ownership identity")
			}
			switch d := d.(type) {
			case *catalog.Type:
				if d.Owner != owner.Ref {
					n := *d
					n.Owner = owner.Ref
					n.ACL = authorization.SchemaACLOwner(d.ACL, d.Owner, owner.Ref)
					candidate.SetDescriptor(a, &n)
					if d.Array != 0 {
						arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Array)}
						array, ok := candidate.Type(arrayAddress)
						if !ok || !array.SQLType.IsArray() || array.SQLType.Element() != d.SQLType {
							return false, fmt.Errorf("stuntgres: invalid owned type array")
						}
						mapped := *array
						mapped.Owner = owner.Ref
						mapped.ACL = authorization.SchemaACLOwner(array.ACL, array.Owner, owner.Ref)
						candidate.SetDescriptor(arrayAddress, &mapped)
					}
					ownersChanged = true
				}
			case *catalog.Procedure:
				if d.Owner != owner.Ref {
					n := *d
					n.Owner = owner.Ref
					n.ACL = authorization.SchemaACLOwner(d.ACL, d.Owner, owner.Ref)
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
