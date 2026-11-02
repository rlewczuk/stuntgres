package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Released identities are independent of the provider's recipe and allocation.
var temporalPrivateCases = []networkPrivateCase{
	{"date", "-infinity", "2000-01-01", "infinity", "2000-01-03", 1082, 1082, 1092, [5]ids.OID{1095, 1096, 1093, 1098, 1097}},
	{"time", "00:00", "12:00", "24:00", "12:00:00.000002", 1083, 1083, 1107, [5]ids.OID{1110, 1111, 1108, 1113, 1112}},
	{"timetz", "00:00+15:59:59", "09:00+01", "24:00-15:59:59", "09:00:00.000002+01", 1266, 1266, 1358, [5]ids.OID{1552, 1553, 1550, 1555, 1554}},
	{"timestamp", "-infinity", "2000-01-01 00:00", "infinity", "2000-01-01 00:00:00.000002", 1114, 1114, 2045, [5]ids.OID{2062, 2063, 2060, 2065, 2064}},
	{"timestamptz", "-infinity", "2000-01-01 00:00+00", "infinity", "2000-01-01 00:00:00.000002+00", 1184, 1184, 1314, [5]ids.OID{1322, 1323, 1320, 1325, 1324}},
	{"interval", "-infinity", "1 mon", "infinity", "31 days", 1186, 1186, 1315, [5]ids.OID{1332, 1333, 1330, 1335, 1334}},
}

func temporalEngine(t *testing.T, populated bool) (*core.Engine, *catalog.Relation, *storage.IndexRoot) {
	return ginFamilyEngine(t, "temporal_private", temporalPrivateCases, populated)
}

func temporalPrivateTexts(tc networkPrivateCase) []string {
	switch tc.typ {
	case "date":
		return []string{"-infinity", "1999-12-31", "2000-01-01", "2000-01-01", "2000-01-02", "infinity", "", "4714-11-24 BC", "5874897-12-31", "0001-12-31 BC", "0001-01-01", "2000-02-29"}
	case "time":
		return []string{"00:00", "11:59:59.999999", "12:00", "12:00", "12:00:00.000001", "24:00", "", "00:00:00.000001", "23:59:59.999999"}
	case "timetz":
		return []string{"00:00+15:59:59", "08:59:59.999999+01", "09:00+01", "09:00+01", "08:00+00", "24:00-15:59:59", "", "00:00+00", "24:00+00"}
	case "timestamp":
		return []string{"-infinity", "1999-12-31 23:59:59.999999", "2000-01-01 00:00", "2000-01-01 00:00+05", "2000-01-01 00:00:00.000001", "infinity", "", "4714-11-24 00:00 BC", "294276-12-31 23:59:59.999999", "0001-12-31 23:59:59.999999 BC", "0001-01-01", "2000-02-29"}
	case "timestamptz":
		return []string{"-infinity", "1999-12-31 23:59:59.999999+00", "2000-01-01 00:00+00", "2000-01-01 01:00+01", "2000-01-01 00:00:00.000001+00", "infinity", "", "4714-11-24 00:00+00 BC", "294276-12-31 23:59:59.999999+00", "0001-12-31 23:59:59.999999+00 BC", "0001-01-01 00:00+00", "2000-02-29 00:00+00"}
	case "interval":
		return []string{"-infinity", "29 days", "1 mon", "30 days", "30 days 0.000001 seconds", "infinity", "", "720 hours", "-2147483648 months -2147483648 days -9223372036854775807 microseconds", "-9223372036854775808 microseconds", "9223372036854775807 microseconds", "2147483647 months 2147483647 days 9223372036854775806 microseconds", "2147483647 months -2147483648 days", "2147483646 months -2147483618 days"}
	default:
		panic("unknown temporal fixture")
	}
}

// Explicitly sorted equivalence groups specify dictionary order and postings,
// including nonidentical interval representations and timetz's offset tie.
func temporalPrivateKeys(tc networkPrivateCase) []scalarDictionaryKey {
	texts := temporalPrivateTexts(tc)
	var groups [][]int
	switch tc.typ {
	case "date", "timestamp", "timestamptz":
		groups = [][]int{{0}, {7}, {9}, {10}, {1}, {2, 3}, {4}, {11}, {8}, {5}}
	case "time", "timetz":
		groups = [][]int{{0}, {7}, {1}, {2, 3}, {4}, {8}, {5}}
	case "interval":
		groups = [][]int{{0}, {8}, {9}, {1}, {2, 3, 7}, {4}, {10}, {12, 13}, {11}, {5}}
	}
	keys := make([]scalarDictionaryKey, 0, len(groups))
	for _, group := range groups {
		keys = append(keys, scalarDictionaryKey{text: texts[group[0]], positions: group})
	}
	return keys
}

const temporalPhysicalRows = 14

func temporalPhysical(t *testing.T) (*core.Engine, *catalog.Relation, *catalog.Relation, *storage.Relation, *storage.IndexRoot) {
	t.Helper()
	e, idx, _ := temporalEngine(t, false)
	view := e.CatalogRead()
	parent := view.MustRelation(idx.SQLIndex.Parent)
	integer, _ := view.LookupType(sqltype.Int4)
	store := &storage.Relation{}
	for row := range temporalPhysicalRows {
		id, _ := sqltype.Integer(integer, int64(row+1))
		values := []sqltype.Value{id}
		for col, tc := range temporalPrivateCases {
			typ := parent.Columns[col+1].Usage.Type
			value := sqltype.Null(typ)
			texts := temporalPrivateTexts(tc)
			if row < len(texts) && texts[row] != "" {
				value = ginInput(t, typ, texts[row])
			}
			values = append(values, value)
		}
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(101 + row), Version: ids.VersionID(201 + row), Born: 1, Values: values})
	}
	root := networkBuild(t, view, parent, idx, store)
	ginAssertOwners(t, root, store)
	return e, idx, parent, store, root
}

func TestBtreeGINTemporalBinding(t *testing.T) {
	e, idx, root := temporalEngine(t, false)
	foreign, other, _ := temporalEngine(t, false)
	ginFamilyBindingProof(t, e, idx, root, foreign, other, temporalPrivateCases)
	view := e.CatalogRead()
	installed, _ := view.OperatorClass(ids.OID(idx.SQLIndex.Keys[0].Class.OID()))
	if installed.Origin == nil || len(installed.Origin.Installation.Members) != 139 {
		t.Fatal("bounded installed recipe inventory")
	}
	// This is the implemented native recipe, not PostgreSQL's wider stock module.
	counts := make(map[ids.OID]int)
	for _, member := range installed.Origin.Installation.Members {
		counts[member.ClassID]++
	}
	if counts[ids.ProcedureClassID] != 83 || counts[ids.OperatorFamilyClassID] != 28 || counts[ids.OperatorClassCatalogID] != 28 {
		t.Fatal("installed declaration kinds", counts)
	}
	for col, tc := range temporalPrivateCases {
		typ, _ := view.LookupType(sqltype.ID(tc.input))
		for other := range temporalPrivateCases {
			if col == other {
				continue
			}
			if _, err := sqltype.BindGinTokens(typ.Usage(), idx.SQLIndex.Keys[other].Class); err == nil {
				t.Fatal("cross temporal class admitted", col, other)
			}
		}
		class := idx.SQLIndex.Keys[col].Class
		for strategy := 1; strategy <= 5; strategy++ {
			member, _ := view.FamilyOperator(ids.OID(class.FamilyOID()), tc.search, tc.search, int16(strategy), 's')
			stamp := class.Search(strategy)
			if stamp.Member != uint32(member.Address().ObjectID) || stamp.MemberRevision != uint64(member.Revision()) {
				t.Fatal("search effective member revision", tc.typ, strategy, stamp)
			}
			op, _ := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: member.Operator})
			proc := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: op.Function})
			for _, changedDescriptor := range []catalog.Descriptor{op, proc} {
				state := catalog.CloneState(view.State())
				switch d := changedDescriptor.(type) {
				case *catalog.Operator:
					changed := *d
					changed.ObjectRevision = d.Revision() + 1
					state.SetDescriptor(changed.Address(), &changed)
				case *catalog.Procedure:
					changed := *d
					changed.ObjectRevision = d.Revision() + 1
					state.SetDescriptor(changed.Address(), &changed)
				}
				if _, err := routine.BindClassSupport(view.WithState(state), class); err == nil {
					t.Fatal("stale executable search capture admitted", tc.typ, strategy)
				}
			}
		}
		for slot := 1; slot <= 5; slot++ {
			member, _ := view.FamilySupport(ids.OID(class.FamilyOID()), tc.input, tc.input, int16(slot))
			state := catalog.CloneState(view.State())
			changed := *member
			changed.ObjectRevision = member.Revision() + 1
			state.SetDescriptor(changed.Address(), &changed)
			if _, err := routine.BindClassSupport(view.WithState(state), class); err == nil {
				t.Fatal("stale support member admitted", tc.typ, slot)
			}
		}
		// Extra search or support slots cannot confer unsupported strategies.
		for _, number := range []int16{6, 15} {
			member, _ := view.FamilyOperator(ids.OID(class.FamilyOID()), tc.search, tc.search, 5, 's')
			state := catalog.CloneState(view.State())
			extra := *member
			extra.ObjectAddress.ObjectID = ids.OID(990000 + col)
			extra.Number = number
			state.SetDescriptor(extra.Address(), &extra)
			state.BindName(catalog.DescriptorNameKey(&extra), extra.Address())
			malformed := view.WithState(state)
			if discovered, ok := malformed.FamilyOperator(ids.OID(class.FamilyOID()), tc.search, tc.search, number, 's'); !ok || discovered != &extra {
				t.Fatal("extra strategy fixture is not discoverable", tc.typ, number)
			}
			if _, err := routine.BindClassSupport(malformed, class); err == nil {
				t.Fatal("extra strategy admitted", tc.typ, number)
			}
		}
		member, _ := view.FamilySupport(ids.OID(class.FamilyOID()), tc.input, tc.input, 5)
		state := catalog.CloneState(view.State())
		extra := *member
		extra.ObjectAddress.ObjectID = ids.OID(991000 + col)
		extra.Number = 6
		state.SetDescriptor(extra.Address(), &extra)
		state.BindName(catalog.DescriptorNameKey(&extra), extra.Address())
		malformed := view.WithState(state)
		if discovered, ok := malformed.FamilySupport(ids.OID(class.FamilyOID()), tc.input, tc.input, 6); !ok || discovered != &extra {
			t.Fatal("extra support fixture is not discoverable", tc.typ)
		}
		if _, err := routine.BindClassSupport(malformed, class); err == nil {
			t.Fatal("extra support admitted", tc.typ)
		}
	}
	old := e.Storage.IndexRoots[idx.Address()]
	for _, q := range []string{"CREATE INDEX temporal_bad ON temporal_private USING gin(date time_ops)", "CREATE INDEX temporal_bad ON temporal_private USING gin(date date_ops,timestamp time_ops)"} {
		if _, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil); err == nil {
			t.Fatal("bad binding admitted", q)
		}
		if e.Storage.IndexRoots[idx.Address()] != old || e.CatalogRead().State() != view.State() {
			t.Fatal("failed binding published catalog or root")
		}
	}
	enginetest.Run(t, e, "CREATE INDEX temporal_bad ON temporal_private USING gin(date date_ops)")
	populated, populatedIndex, populatedRoot := temporalEngine(t, true)
	if len(populatedRoot.Entries) != 5 || len(populatedRoot.Gin.Columns) != 6 {
		t.Fatal("populated temporal root shape")
	}
	for col, key := range populatedIndex.SQLIndex.Keys {
		current, err := routine.BindClassSupport(populated.CatalogRead(), key.Class)
		if err != nil || current != populatedRoot.Gin.Columns[col].Binding.Class {
			t.Fatal("populated root lost actual capture", col, err)
		}
	}
}
