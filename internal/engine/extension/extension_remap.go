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

func RemapDescriptor(d catalog.Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress) catalog.Descriptor {
	address := func(a ids.ObjectAddress) ids.ObjectAddress {
		if next, ok := mapping[a]; ok {
			return next
		}
		return a
	}
	switch d := d.(type) {
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
			n.Result = owned.Definition.TransitionType
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
			n.Aggregate = remapOwnedAggregate(d.Aggregate, n.Address(), n.Namespace(), mapping)
		}
		if CubeProviderOf(&n) != nil {
			n.Result = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n.Result}).ObjectID
			parts := strings.Split(string(n.Signature), ",")
			for i, part := range parts {
				if oid, err := strconv.ParseUint(part, 10, 32); err == nil {
					parts[i] = strconv.FormatUint(uint64(address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oid)}).ObjectID), 10)
				}
			}
			n.Signature = catalog.ProcedureSignature(strings.Join(parts, ","))
		}
		if IntarrayProviderOf(&n) != nil || CitextProviderOf(&n) != nil {
			if n.Name() == "bqarr_in" || n.Name() == "citextin" {
				n.Result = address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: n.Result}).ObjectID
			} else {
				if oid, err := strconv.ParseUint(string(n.Signature), 10, 32); err == nil {
					next := address(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(oid)})
					if next.ObjectID != ids.OID(oid) {
						n.Signature = QueryIntSignature(sqltype.ID(next.ObjectID))
					}
				}
			}
		}
		if n.Address() == d.Address() && n.Namespace() == d.Namespace() && n.Extension == d.Extension && n.Dictionary == d.Dictionary && n.Signature == d.Signature && n.Result == d.Result && n.Aggregate == d.Aggregate {
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
