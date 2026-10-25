package extension

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ProcedureRemapper maps the original declaration graph without resolving its
// addresses through a newer catalog root. The memo is shared by all consumers
// in one replacement.
func ProcedureRemapper(mapping map[ids.ObjectAddress]ids.ObjectAddress, principal catalog.ProcedureRemapper, prepare func(*catalog.Procedure) error) catalog.ProcedureRemapper {
	procedures := make(map[*catalog.Procedure]*catalog.Procedure)
	var procedure catalog.ProcedureRemapper
	procedure = func(p *catalog.Procedure) (*catalog.Procedure, error) {
		if p == nil {
			return nil, nil
		}
		if n := procedures[p]; n != nil {
			return n, nil
		}
		original := p
		if principal != nil {
			var err error
			p, err = principal(p)
			if err != nil {
				return nil, err
			}
		}
		if n := procedures[p]; n != nil {
			procedures[original] = n
			return n, nil
		}
		remapped := RemapDescriptor(p, mapping).(*catalog.Procedure)
		if len(p.Body) == 0 && prepare == nil {
			procedures[original] = remapped
			return remapped, nil
		}
		n := *remapped
		procedures[original] = &n
		procedures[&n] = &n
		if prepare != nil {
			if err := prepare(&n); err != nil {
				return nil, err
			}
		}
		n.Body = append([]catalog.RoutineBodyEdge(nil), p.Body...)
		for i := range p.Body {
			var err error
			// Principal remapping has its own memo; recurse from the source
			// graph so composition never reapplies a domain conversion.
			n.Body[i].Routine, err = procedure(original.Body[i].Routine)
			if err != nil {
				return nil, err
			}
		}
		return &n, nil
	}
	return procedure
}

func RemapDescriptor(d catalog.Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress) catalog.Descriptor {
	address := func(a ids.ObjectAddress) ids.ObjectAddress {
		if next, ok := mapping[a]; ok {
			return next
		}
		return a
	}
	switch d := d.(type) {
	case *catalog.Language:
		n := *d
		n.ObjectAddress = address(d.Address())
		n.Call, n.Inline, n.Validator = address(d.Call), address(d.Inline), address(d.Validator)
		if n.ObjectAddress == d.ObjectAddress && n.Call == d.Call && n.Inline == d.Inline && n.Validator == d.Validator {
			return d
		}
		return &n
	case *catalog.Type:
		next := address(d.Extension)
		if next == d.Extension {
			return d
		}
		n := *d
		n.Extension = next
		return &n
	case *catalog.Relation:
		var addresses []ids.ObjectAddress
		for i, a := range d.AutoExtensions {
			next := address(a)
			if next != a && addresses == nil {
				addresses = append([]ids.ObjectAddress(nil), d.AutoExtensions...)
			}
			if addresses != nil {
				addresses[i] = next
			}
		}
		if addresses == nil {
			return d
		}
		sort.Slice(addresses, func(i, j int) bool { return addresses[i].ObjectID < addresses[j].ObjectID })
		n := *d
		n.AutoExtensions = addresses
		return &n
	case *catalog.Extension:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.TargetNamespace = address(n.TargetNamespace)
		var members []ids.ObjectAddress
		for i, a := range d.Members {
			mapped := address(a)
			if mapped != a && members == nil {
				members = slices.Clone(d.Members)
			}
			if members != nil {
				members[i] = mapped
			}
		}
		if members != nil {
			n.Members = members
		}
		if n.Address() == d.Address() && n.TargetNamespace == d.TargetNamespace && members == nil {
			return d
		}
		return &n
	case *catalog.Procedure:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.ObjectNamespace = address(n.Namespace())
		n.Extension = address(n.Extension)
		n.Dictionary = address(n.Dictionary)
		n.Language = address(d.Language)
		if len(d.DeclarationDependencies) != 0 {
			copied := false
			for i, old := range d.DeclarationDependencies {
				base := old
				base.SubID = 0
				next := address(base)
				next.SubID = old.SubID
				if next != old {
					if !copied {
						n.DeclarationDependencies = append([]ids.ObjectAddress(nil), d.DeclarationDependencies...)
						copied = true
					}
					n.DeclarationDependencies[i] = next
				}
			}
		}
		if d.Aggregate != nil && d.DynamicCallable() {
			owned := *d.Aggregate
			owned.Definition.Function = n.Address().ObjectID
			for i, oid := range owned.Definition.Functions {
				owned.Definition.Functions[i] = address(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}).ObjectID
			}
			owned.Definition.TransitionType = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: owned.Definition.TransitionType}).ObjectID
			if d.Aggregate.BinaryRecipe != nil {
				original := d.Aggregate.BinaryRecipe
				var recipe *catalog.AggregateBinaryRecipe
				for i, old := range original.Casts {
					next := old
					next.Source = sqltype.ID(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Source)}).ObjectID)
					next.Target = sqltype.ID(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Target)}).ObjectID)
					if next != old {
						if recipe == nil {
							recipe = &catalog.AggregateBinaryRecipe{Casts: append([]catalog.Cast(nil), original.Casts...)}
						}
						recipe.Casts[i] = next
					}
				}
				if recipe != nil {
					owned.BinaryRecipe = recipe
				}
			}
			n.Aggregate = &owned
			n.Result = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: d.Result}).ObjectID
			if n.Signature != "" {
				parts := strings.Split(string(n.Signature), ",")
				for i, part := range parts {
					if id, err := strconv.ParseUint(part, 10, 32); err == nil {
						parts[i] = strconv.FormatUint(uint64(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}).ObjectID), 10)
					}
				}
				n.Signature = catalog.ProcedureSignature(strings.Join(parts, ","))
			}
		} else {
			n.Aggregate = remapOwnedAggregate(d.Aggregate, n.Address(), mapping)
		}
		if n.RangeConstructor != nil || CubeProviderOf(&n) != nil || EarthdistanceProviderOf(&n) != nil {
			n.Result = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n.Result}).ObjectID
			parts := strings.Split(string(n.Signature), ",")
			for i, part := range parts {
				if oid, err := strconv.ParseUint(part, 10, 32); err == nil {
					parts[i] = strconv.FormatUint(uint64(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oid)}).ObjectID), 10)
				}
			}
			n.Signature = catalog.ProcedureSignature(strings.Join(parts, ","))
		}
		if IntarrayProviderOf(&n) != nil || CitextProviderOf(&n) != nil || IsnProviderOf(&n) != nil {
			// Provider validation owns these immutable formal signatures.
			// Comparisons instead remap their citext inputs and retain BOOL.
			citextConversion := CitextProviderOf(d) == StockCitext && (d.Signature == "1042" || d.Signature == "16" || d.Signature == "869")
			if d.Signature == "2275" || citextConversion {
				n.Result = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n.Result}).ObjectID
			} else {
				parts := strings.Split(string(n.Signature), ",")
				for i, part := range parts {
					if oid, err := strconv.ParseUint(part, 10, 32); err == nil {
						parts[i] = strconv.FormatUint(uint64(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oid)}).ObjectID), 10)
					}
				}
				n.Signature = catalog.ProcedureSignature(strings.Join(parts, ","))
			}
		}
		if n.Address() == d.Address() && n.Namespace() == d.Namespace() && n.Extension == d.Extension && n.Dictionary == d.Dictionary && n.Signature == d.Signature && n.Result == d.Result && n.Aggregate == d.Aggregate {
			return d
		}
		return &n
	case *catalog.OperatorClass:
		return catalog.RemapOperatorClass(d, mapping)
	case *catalog.OperatorFamily, *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
		return catalog.RemapOperatorFamily(d, mapping)
	case *catalog.Operator:
		n := *d
		n.ObjectAddress = address(n.ObjectAddress)
		n.ObjectNamespace = address(n.ObjectNamespace)
		n.Extension = address(n.Extension)
		n.Left = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n.Left}).ObjectID
		n.Right = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n.Right}).ObjectID
		n.Result = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n.Result}).ObjectID
		n.Function = address(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: n.Function}).ObjectID
		n.Commutator = address(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: n.Commutator}).ObjectID
		n.Negator = address(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: n.Negator}).ObjectID
		n.Restriction = address(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: n.Restriction}).ObjectID
		n.Join = address(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: n.Join}).ObjectID
		if n == *d {
			return d
		}
		return &n
	case *catalog.TSTemplate:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.ObjectNamespace = address(n.Namespace())
		n.Extension = address(n.Extension)
		n.Init = address(n.Init)
		n.Lexize = address(n.Lexize)
		return &n
	case *catalog.TSDictionary:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.ObjectNamespace = address(n.Namespace())
		n.Extension = address(n.Extension)
		n.Template = address(n.Template)
		return &n
	}
	return d
}
