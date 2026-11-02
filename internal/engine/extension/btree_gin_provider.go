package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// BtreeGinProvider owns the released scalar GIN installation, not a C loader.
// Nonzero size makes independently constructed providers distinct authorities.
type BtreeGinProvider struct{ identity byte }

var StockBtreeGin = &BtreeGinProvider{}

func (*BtreeGinProvider) Control() catalog.ExtensionControl {
	return catalog.ExtensionControl{Version: "1.3", Superuser: true, Trusted: true, Relocatable: true, Comment: new("support for indexing common datatypes in GIN")}
}
func (*BtreeGinProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.3"}}
}
func (p *BtreeGinProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	if p != StockBtreeGin {
		return InvalidProviderDescriptor()
	}
	return ValidateCurrentInstallation(v, e, p)
}
func (p *BtreeGinProvider) ValidateProcedure(v catalog.Read, d *catalog.Procedure) error {
	if p != StockBtreeGin {
		return InvalidProviderDescriptor()
	}
	return ValidateProcedureOrigin(v, d, p)
}
func (p *BtreeGinProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockBtreeGin || !currentManifest(v, e) || e.Version != "1.3" || len(e.Members) != btreeGinMemberCount {
		return InvalidProviderDescriptor()
	}
	return p.ValidateExtension(v, e)
}

// Released C declarations precede family/class pairs. The recipe order is stable
// across symbolic replay; family members retain their exact dependency policy.
const btreeGinRoutineCount = len(btreeGinRoutines)
const btreeGinMemberCount = btreeGinRoutineCount + 2*len(btreeGinClasses)

var btreeGinRoutines = [...]struct {
	name      string
	signature catalog.ProcedureSignature
	result    ids.OID
}{
	{"gin_btree_consistent", "2281,21,2283,23,2281,2281", 16},
	{"gin_enum_cmp", "3500,3500", 23},
	{"gin_extract_value_bit", "1560,2281", 2281},
	{"gin_extract_query_bit", "1560,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_bit", "1560,1560,21,2281", 23},
	{"gin_extract_value_varbit", "1562,2281", 2281},
	{"gin_extract_query_varbit", "1562,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_varbit", "1562,1562,21,2281", 23},
	{"gin_extract_value_anyenum", "3500,2281", 2281},
	{"gin_extract_query_anyenum", "3500,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_anyenum", "3500,3500,21,2281", 23},
	{"gin_extract_value_money", "790,2281", 2281},
	{"gin_extract_query_money", "790,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_money", "790,790,21,2281", 23},
	{"gin_extract_value_inet", "869,2281", 2281},
	{"gin_extract_query_inet", "869,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_inet", "869,869,21,2281", 23},
	{"gin_extract_value_cidr", "650,2281", 2281},
	{"gin_extract_query_cidr", "650,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_cidr", "650,650,21,2281", 23},
	{"gin_extract_value_macaddr", "829,2281", 2281},
	{"gin_extract_query_macaddr", "829,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_macaddr", "829,829,21,2281", 23},
	{"gin_extract_value_macaddr8", "774,2281", 2281},
	{"gin_extract_query_macaddr8", "774,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_macaddr8", "774,774,21,2281", 23},
	{"gin_extract_value_uuid", "2950,2281", 2281},
	{"gin_extract_query_uuid", "2950,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_uuid", "2950,2950,21,2281", 23},
	{"gin_extract_value_oid", "26,2281", 2281},
	{"gin_extract_query_oid", "26,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_oid", "26,26,21,2281", 23},
	{"gin_extract_value_int2", "21,2281", 2281},
	{"gin_extract_query_int2", "21,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_int2", "21,21,21,2281", 23},
	{"gin_extract_value_int4", "23,2281", 2281},
	{"gin_extract_query_int4", "23,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_int4", "23,23,21,2281", 23},
	{"gin_extract_value_int8", "20,2281", 2281},
	{"gin_extract_query_int8", "20,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_int8", "20,20,21,2281", 23},
	{"gin_extract_value_float4", "700,2281", 2281},
	{"gin_extract_query_float4", "700,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_float4", "700,700,21,2281", 23},
	{"gin_extract_value_float8", "701,2281", 2281},
	{"gin_extract_query_float8", "701,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_float8", "701,701,21,2281", 23},
	{"gin_extract_value_bool", "16,2281", 2281},
	{"gin_extract_query_bool", "16,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_bool", "16,16,21,2281", 23},
	{"gin_extract_value_date", "1082,2281", 2281},
	{"gin_extract_query_date", "1082,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_date", "1082,1082,21,2281", 23},
	{"gin_extract_value_time", "1083,2281", 2281},
	{"gin_extract_query_time", "1083,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_time", "1083,1083,21,2281", 23},
	{"gin_extract_value_timetz", "1266,2281", 2281},
	{"gin_extract_query_timetz", "1266,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_timetz", "1266,1266,21,2281", 23},
	{"gin_extract_value_timestamp", "1114,2281", 2281},
	{"gin_extract_query_timestamp", "1114,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_timestamp", "1114,1114,21,2281", 23},
	{"gin_extract_value_timestamptz", "1184,2281", 2281},
	{"gin_extract_query_timestamptz", "1184,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_timestamptz", "1184,1184,21,2281", 23},
	{"gin_extract_value_interval", "1186,2281", 2281},
	{"gin_extract_query_interval", "1186,2281,21,2281,2281", 2281},
	{"gin_compare_prefix_interval", "1186,1186,21,2281", 23},
}

// BtreeGinRoutineName is a discovery hint only. Executable selection still
// authenticates the actual immutable installed declaration, never this spelling.
func BtreeGinRoutineName(name string) bool {
	for _, declaration := range btreeGinRoutines {
		if declaration.name == name {
			return true
		}
	}
	return false
}

type btreeGinClass struct {
	name                      string
	input, search, comparator ids.OID
	operators                 [5]ids.OID
	releasedDependencies      bool
}

var btreeGinClasses = [...]btreeGinClass{
	{"bit_ops", 1560, 1560, 1596, [5]ids.OID{1786, 1788, 1784, 1789, 1787}, false},
	{"varbit_ops", 1562, 1562, 1672, [5]ids.OID{1806, 1808, 1804, 1809, 1807}, false},
	{"enum_ops", 3500, 3500, 0, [5]ids.OID{3518, 3520, 3516, 3521, 3519}, false},
	{"money_ops", 790, 790, 377, [5]ids.OID{902, 904, 900, 905, 903}, false},
	{"inet_ops", 869, 869, 926, [5]ids.OID{1203, 1204, 1201, 1206, 1205}, true},
	{"cidr_ops", 650, 869, 926, [5]ids.OID{1203, 1204, 1201, 1206, 1205}, true},
	{"macaddr_ops", 829, 829, 836, [5]ids.OID{1222, 1223, 1220, 1225, 1224}, true},
	{"macaddr8_ops", 774, 774, 4119, [5]ids.OID{3364, 3365, 3362, 3367, 3366}, true},
	{"uuid_ops", 2950, 2950, 2960, [5]ids.OID{2974, 2976, 2972, 2977, 2975}, true},
	{"oid_ops", 26, 26, 356, [5]ids.OID{609, 611, 607, 612, 610}, true},
	{"int2_ops", 21, 21, 350, [5]ids.OID{95, 522, 94, 524, 520}, true},
	{"int4_ops", 23, 23, 351, [5]ids.OID{97, 523, 96, 525, 521}, true},
	{"int8_ops", 20, 20, 842, [5]ids.OID{412, 414, 410, 415, 413}, true},
	{"float4_ops", 700, 700, 354, [5]ids.OID{622, 624, 620, 625, 623}, true},
	{"float8_ops", 701, 701, 355, [5]ids.OID{672, 673, 670, 675, 674}, true},
	{"bool_ops", 16, 16, 1693, [5]ids.OID{58, 1694, 91, 1695, 59}, true},
	{"date_ops", 1082, 1082, 1092, [5]ids.OID{1095, 1096, 1093, 1098, 1097}, true},
	{"time_ops", 1083, 1083, 1107, [5]ids.OID{1110, 1111, 1108, 1113, 1112}, true},
	{"timetz_ops", 1266, 1266, 1358, [5]ids.OID{1552, 1553, 1550, 1555, 1554}, true},
	{"timestamp_ops", 1114, 1114, 2045, [5]ids.OID{2062, 2063, 2060, 2065, 2064}, true},
	{"timestamptz_ops", 1184, 1184, 1314, [5]ids.OID{1322, 1323, 1320, 1325, 1324}, true},
	{"interval_ops", 1186, 1186, 1315, [5]ids.OID{1332, 1333, 1330, 1335, 1334}, true},
}

// Zero InternalOwner means AUTO dependency on the actual family. Preserve the
// incumbent compound policy; selected recipes use ginadjustmembers' hard slots.
func (spec btreeGinClass) memberOwner(class ids.ObjectAddress, number int, support bool) ids.ObjectAddress {
	if !spec.releasedDependencies || support && (number == 2 || number == 3) {
		return class
	}
	return ids.ObjectAddress{}
}

type BtreeGinBindings struct {
	Support, Search [5]ids.OID
	SearchType      ids.OID
}

// BtreeGinClassBindings authenticates the original installation and its exact
// support/search graph independently of current names, owners and membership.
// The returned IDs are mapped destination declarations, never source handles.
func BtreeGinClassBindings(v catalog.Read, c *catalog.OperatorClass) (BtreeGinBindings, error) {
	invalid := func() (BtreeGinBindings, error) {
		return BtreeGinBindings{}, fmt.Errorf("stuntgres: invalid btree_gin class binding")
	}
	if c == nil || catalog.ValidateInstalledClassOrigin(v, c) != nil || !c.Origin.Valid(c.Address()) {
		return invalid()
	}
	installation := c.Origin.Installation
	if installation.Implementation != StockBtreeGin || installation.Provider != "btree_gin" || installation.Version != "1.3" || len(installation.Members) != btreeGinMemberCount || c.Method != 2742 || c.StorageType != 0 || !c.Default {
		return invalid()
	}
	column := (c.Origin.Declaration - btreeGinRoutineCount - 1) / 2
	if c.Origin.Declaration < btreeGinRoutineCount+1 || column >= len(btreeGinClasses) || c.Origin.Declaration != btreeGinRoutineCount+column*2+1 {
		return invalid()
	}
	spec := btreeGinClasses[column]
	if c.InputType != spec.input || c.Family != installation.Members[btreeGinRoutineCount+column*2].ObjectID {
		return invalid()
	}
	family, ok := v.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: c.Family})
	if !ok || family.Method != c.Method || !catalog.SameInstallation(family, installation) || catalog.ValidateInstalledClassOrigin(v, family) != nil {
		return invalid()
	}
	routines := [5]ids.OID{spec.comparator, installation.Members[2+column*3].ObjectID, installation.Members[3+column*3].ObjectID, installation.Members[0].ObjectID, installation.Members[4+column*3].ObjectID}
	if spec.comparator == 0 {
		routines[0] = installation.Members[1].ObjectID
	}
	for i, expected := range routines {
		support, ok := v.FamilySupport(c.Family, c.InputType, c.InputType, int16(i+1))
		if !ok || support.Function != expected || support.InternalOwner != spec.memberOwner(c.Address(), i+1, true) || support.Revision() == 0 || !support.CatalogVisible {
			return invalid()
		}
		procedure, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: expected})
		if !ok {
			return invalid()
		}
		if i != 0 || spec.comparator == 0 {
			if !catalog.SameInstallation(procedure, installation) || procedure.Implementation != StockBtreeGin {
				return invalid()
			}
		}
		provider := v.Owner().Provider(ids.ProcedureClassID)
		if provider == nil {
			return invalid()
		}
		if err := provider.Validate(v, procedure); err != nil {
			return BtreeGinBindings{}, err
		}
	}
	if _, ok := v.FamilySupport(c.Family, c.InputType, c.InputType, 6); ok {
		return invalid()
	}
	for i, expected := range spec.operators {
		member, ok := v.FamilyOperator(c.Family, spec.search, spec.search, int16(i+1), 's')
		if !ok || member.Operator != expected || member.Method != c.Method || member.SortFamily != 0 || member.InternalOwner != spec.memberOwner(c.Address(), i+1, false) || member.Revision() == 0 {
			return invalid()
		}
	}
	for _, strategy := range []int16{6, 15} {
		if _, ok := v.FamilyOperator(c.Family, spec.search, spec.search, strategy, 's'); ok {
			return invalid()
		}
	}
	return BtreeGinBindings{Support: routines, Search: spec.operators, SearchType: spec.search}, nil
}
