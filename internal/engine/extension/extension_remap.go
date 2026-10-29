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
func ProcedureRemapper(descriptor func(catalog.Descriptor) catalog.Descriptor, principal catalog.ProcedureRemapper, prepare func(*catalog.Procedure) error) catalog.ProcedureRemapper {
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
		remapped := descriptor(p).(*catalog.Procedure)
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

func remapDescriptor(d catalog.Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress, installation func(*catalog.Installation) *catalog.Installation) catalog.Descriptor {
	address := func(a ids.ObjectAddress) ids.ObjectAddress {
		if next, ok := mapping[a]; ok {
			return next
		}
		return a
	}
	origin := func(o *catalog.InstalledOrigin) *catalog.InstalledOrigin {
		if o == nil {
			return nil
		}
		i := installation(o.Installation)
		if i == o.Installation {
			return o
		}
		n := *o
		n.Installation = i
		return &n
	}
	switch d := d.(type) {
	case *catalog.EventTrigger:
		n := *d
		n.ObjectAddress = address(d.Address())
		n.Extension = address(d.Extension)
		if n.ObjectAddress == d.ObjectAddress && n.Extension == d.Extension {
			return d
		}
		return &n
	case *catalog.Language:
		n := *d
		n.ObjectAddress = address(d.Address())
		n.Extension = address(d.Extension)
		n.Origin = origin(d.Origin)
		n.Call, n.Inline, n.Validator = address(d.Call), address(d.Inline), address(d.Validator)
		if n.ObjectAddress == d.ObjectAddress && n.Call == d.Call && n.Inline == d.Inline && n.Validator == d.Validator && n.Extension == d.Extension && n.Origin == d.Origin {
			return d
		}
		return &n
	case *catalog.Type:
		next := address(d.Extension)
		o := origin(d.Origin)
		if next == d.Extension && o == d.Origin {
			return d
		}
		n := *d
		n.Extension = next
		n.Origin = o
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
		if addresses == nil && address(d.Extension) == d.Extension {
			return d
		}
		if addresses != nil {
			sort.Slice(addresses, func(i, j int) bool { return addresses[i].ObjectID < addresses[j].ObjectID })
		}
		n := *d
		n.Extension = address(d.Extension)
		if addresses != nil {
			n.AutoExtensions = addresses
		}
		return &n
	case *catalog.Extension:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.TargetNamespace = address(n.TargetNamespace)
		n.Installation = installation(d.Installation)
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
		if n.Address() == d.Address() && n.TargetNamespace == d.TargetNamespace && members == nil && n.Installation == d.Installation {
			return d
		}
		return &n
	case *catalog.Procedure:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.ObjectNamespace = address(n.Namespace())
		n.Extension = address(n.Extension)
		n.Origin = origin(d.Origin)
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
			if owned.Definition.MovingType != 0 {
				owned.Definition.MovingType = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: owned.Definition.MovingType}).ObjectID
			}
			if owned.Definition.SortOperator != 0 {
				owned.Definition.SortOperator = address(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: owned.Definition.SortOperator}).ObjectID
			}
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
		if HstoreProviderOf(&n) != nil || LtreeProviderOf(&n) != nil || n.RangeConstructor != nil || CubeProviderOf(&n) != nil || EarthdistanceProviderOf(&n) != nil || TablefuncProviderOf(&n) != nil {
			n.Result = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n.Result}).ObjectID
			parts := strings.Split(string(n.Signature), ",")
			for i, part := range parts {
				if oid, err := strconv.ParseUint(part, 10, 32); err == nil {
					parts[i] = strconv.FormatUint(uint64(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oid)}).ObjectID), 10)
				}
			}
			n.Signature = catalog.ProcedureSignature(strings.Join(parts, ","))
		}
		if SegProviderOf(&n) != nil || IntarrayProviderOf(&n) != nil || CitextProviderOf(&n) != nil || IsnProviderOf(&n) != nil {
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
		if n.Address() == d.Address() && n.Namespace() == d.Namespace() && n.Extension == d.Extension && n.Dictionary == d.Dictionary && n.Signature == d.Signature && n.Result == d.Result && n.Aggregate == d.Aggregate && n.Origin == d.Origin {
			return d
		}
		return &n
	case *catalog.OperatorClass:
		n := catalog.RemapOperatorClass(d, mapping)
		o := origin(d.Origin)
		if o == d.Origin {
			return n
		}
		if n == d {
			copy := *d
			n = &copy
		}
		n.Origin = o
		return n
	case *catalog.OperatorFamily:
		n := catalog.RemapOperatorFamily(d, mapping).(*catalog.OperatorFamily)
		o := origin(d.Origin)
		if o == d.Origin {
			return n
		}
		if n == d {
			copy := *d
			n = &copy
		}
		n.Origin = o
		return n
	case *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
		return catalog.RemapOperatorFamily(d, mapping)
	case *catalog.Operator:
		n := *d
		n.ObjectAddress = address(n.ObjectAddress)
		n.ObjectNamespace = address(n.ObjectNamespace)
		n.Extension = address(n.Extension)
		n.Origin = origin(d.Origin)
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
		n.Origin = origin(d.Origin)
		n.Init = address(n.Init)
		n.Lexize = address(n.Lexize)
		if n.ObjectAddress == d.ObjectAddress && n.ObjectNamespace == d.ObjectNamespace && n.Extension == d.Extension && n.Origin == d.Origin && n.Init == d.Init && n.Lexize == d.Lexize {
			return d
		}
		return &n
	case *catalog.TSParser:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.ObjectNamespace = address(n.Namespace())
		n.Extension = address(n.Extension)
		n.Start = address(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Start}).ObjectID
		n.Token = address(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Token}).ObjectID
		n.End = address(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.End}).ObjectID
		if d.Headline != 0 {
			n.Headline = address(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Headline}).ObjectID
		}
		n.LexType = address(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.LexType}).ObjectID
		return &n
	case *catalog.TSConfiguration:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.ObjectNamespace = address(n.Namespace())
		n.Extension = address(d.Extension)
		n.Parser = address(n.Parser)
		n.Mappings = catalog.CloneTSMappings(d.Mappings)
		for i := range n.Mappings {
			for j, a := range n.Mappings[i].Dictionaries {
				n.Mappings[i].Dictionaries[j] = address(a)
			}
		}
		return &n
	case *catalog.TSDictionary:
		n := *d
		n.ObjectAddress = address(n.Address())
		n.ObjectNamespace = address(n.Namespace())
		n.Extension = address(n.Extension)
		n.Origin = origin(d.Origin)
		n.Template = address(n.Template)
		n.NormalizerNamespace = address(n.NormalizerNamespace)
		if n.ObjectAddress == d.ObjectAddress && n.ObjectNamespace == d.ObjectNamespace && n.Extension == d.Extension && n.Origin == d.Origin && n.Template == d.Template && n.NormalizerNamespace == d.NormalizerNamespace {
			return d
		}
		return &n
	case *catalog.Namespace:
		e := address(d.Extension)
		if e == d.Extension {
			return d
		}
		n := *d
		n.Extension = e
		return &n
	case *catalog.AccessMethod:
		e := address(d.Extension)
		if e == d.Extension {
			return d
		}
		n := *d
		n.Extension = e
		return &n
	case *catalog.Collation:
		e := address(d.Extension)
		if e == d.Extension {
			return d
		}
		n := *d
		n.Extension = e
		return &n
	case *catalog.Conversion:
		e := address(d.Extension)
		if e == d.Extension {
			return d
		}
		n := *d
		n.Extension = e
		return &n
	case *catalog.ForeignObject:
		e := address(d.Extension)
		if e == d.Extension {
			return d
		}
		n := *d
		n.Extension = e
		return &n
	case *catalog.Cast:
		n := *d
		n.OID = address(d.Address()).ObjectID
		n.Source = sqltype.ID(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Source)}).ObjectID)
		n.Target = sqltype.ID(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Target)}).ObjectID)
		n.Function, n.InputCast, n.ResultCast = address(d.Function), address(d.InputCast), address(d.ResultCast)
		n.GeneratedRange, n.Extension = address(d.GeneratedRange), address(d.Extension)
		n.Origin = origin(d.Origin)
		if n == *d {
			return d
		}
		return &n
	}
	return d
}
