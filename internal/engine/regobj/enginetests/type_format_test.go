package regobj_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func typeDescribeCatalog() (*catalog.State, catalog.Read, []ids.ObjectAddress) {
	state := catalog.NewState(1)
	catalog.InstallBuiltinNamespaces(state, authorization.PrincipalRef{}, authorization.PrincipalRef{})
	catalog.InstallBuiltinTypes(state, sqltype.Builtins(), authorization.PrincipalRef{})
	return state, catalog.NewRead(nil, state), []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, {ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}
}

func installType(state *catalog.State, typ *sqltype.Descriptor, namespace ids.ObjectAddress) {
	d := &catalog.Type{SQLType: typ, ObjectNamespace: namespace}
	state.SetDescriptor(d.Address(), d)
	state.BindName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: d.Namespace(), Name: d.Name()}, d.Address())
}

func TestTypeDescribeFormatter(t *testing.T) {
	state, view, path := typeDescribeCatalog()
	for _, tc := range []struct {
		id   sqltype.ID
		want string
	}{
		{0, "-"}, {4294967295, "???"}, {16, "boolean"}, {18, `"char"`}, {19, "name"},
		{20, "bigint"}, {21, "smallint"}, {22, "int2vector"}, {23, "integer"}, {25, "text"}, {30, "oidvector"},
		{114, "json"}, {700, "real"}, {701, "double precision"}, {705, "unknown"},
		{1003, "name[]"}, {1006, "int2vector[]"}, {1007, "integer[]"}, {1013, "oidvector[]"},
		{1014, "character[]"}, {1015, "character varying[]"}, {1042, "character"}, {1043, "character varying"},
		{1083, "time without time zone"}, {1266, "time with time zone"},
		{1114, "timestamp without time zone"}, {1184, "timestamp with time zone"},
		{1186, "interval"}, {1560, "bit"}, {1562, "bit varying"}, {1700, "numeric"},
		{2206, "regtype"}, {2276, `"any"`}, {3904, "int4range"}, {4451, "int4multirange"},
		{13243, "information_schema.cardinal_number"}, {13242, "information_schema.cardinal_number[]"},
		{13265, "information_schema.administrable_role_authorizations"},
	} {
		got, err := regobj.FormatType(view, path, tc.id, 7, false)
		if err != nil || got != tc.want {
			t.Fatalf("FormatType(%d,NULL) = %q, %v; want %q", tc.id, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		id   sqltype.ID
		mod  int32
		want string
	}{
		{23, -1, "integer"}, {23, 7, "integer"}, {1042, -1, "bpchar"},
		{1042, 0, "character"}, {1042, 1, "character"}, {1042, 2, "character"}, {1042, 3, "character"}, {1042, 4, "character"}, {1042, 7, "character(3)"},
		{1043, -1, "character varying"}, {1043, 0, "character varying"}, {1043, 4, "character varying"}, {1043, 36, "character varying(32)"},
		{1700, -1, "numeric"}, {1700, 0, "numeric"}, {1700, 1, "numeric"}, {1700, 2, "numeric"}, {1700, 3, "numeric"},
		{1700, 327686, "numeric(5,2)"}, {1700, 329730, "numeric(5,-2)"}, {1700, 328707, "numeric(5,1023)"}, {1700, 328708, "numeric(5,-1024)"},
		{1560, -1, `"bit"`}, {1560, 0, "bit(0)"}, {1560, 7, "bit(7)"}, {1562, -1, "bit varying"}, {1562, 7, "bit varying(7)"},
		{1083, 3, "time(3) without time zone"}, {1266, 7, "time(7) with time zone"},
		{1114, 3, "timestamp(3) without time zone"}, {1184, 3, "timestamp(3) with time zone"}, {1114, 7, "timestamp(7) without time zone"},
		{1114, 2147483647, "timestamp(2147483647) without time zone"}, {25, 7, "text(7)"},
		{1014, -1, "bpchar[]"}, {1014, 7, "character(3)[]"}, {1009, 7, "text(7)[]"},
	} {
		got, err := regobj.FormatType(view, path, tc.id, tc.mod, true)
		if err != nil || got != tc.want {
			t.Fatalf("FormatType(%d,%d) = %q, %v; want %q", tc.id, tc.mod, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		mask   int32
		suffix string
	}{
		{4, " year"}, {2, " month"}, {8, " day"}, {1024, " hour"}, {2048, " minute"}, {4096, " second"},
		{6, " year to month"}, {1032, " day to hour"}, {3080, " day to minute"}, {7176, " day to second"},
		{3072, " hour to minute"}, {7168, " hour to second"}, {6144, " minute to second"}, {32767, ""},
	} {
		for _, precision := range []int32{65535, 3} {
			want := "interval" + tc.suffix
			if precision == 3 {
				want += "(3)"
			}
			got, err := regobj.FormatType(view, path, sqltype.Interval, tc.mask<<16|precision, true)
			if err != nil || got != want {
				t.Fatalf("interval mask %d precision %d = %q, %v; want %q", tc.mask, precision, got, err, want)
			}
		}
	}
	for _, mod := range []int32{0, 1 << 16} {
		_, err := regobj.FormatType(view, path, sqltype.Interval, mod, true)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" {
			t.Fatalf("invalid interval modifier %d: %v", mod, err)
		}
		want := "invalid INTERVAL typmod: 0x0"
		if mod != 0 {
			want = "invalid INTERVAL typmod: 0x10000"
		}
		if diagnostic.Message != want {
			t.Fatalf("interval diagnostic = %q; want %q", diagnostic.Message, want)
		}
	}

	public := path[1]
	odd := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 80000}, ObjectName: "Odd Schema"}
	state.SetDescriptor(odd.Address(), odd)
	array, _ := view.LookupType(1007)
	domain := sqltype.LookupDomain(80001, "Outer \"Mixed", "unused", sqltype.Usage{Type: array, Typmod: -1})
	installType(state, domain, odd.Address())
	installType(state, sqltype.LookupArray(80002, "_outer", domain), odd.Address())
	installType(state, sqltype.LookupOnly(80003, "text", 0), public)
	installType(state, sqltype.LookupOnly(80004, "bool", 0), public)
	installType(state, sqltype.LookupOnly(80005, "bit", 0), public)
	view = catalog.NewRead(nil, state)
	for _, tc := range []struct {
		id        sqltype.ID
		path      []ids.ObjectAddress
		mod       int32
		specified bool
		want      string
	}{
		{80001, path, -1, false, `"Odd Schema"."Outer ""Mixed"`},
		{80001, []ids.ObjectAddress{odd.Address()}, 7, true, `"Outer ""Mixed"(7)`},
		{80002, []ids.ObjectAddress{odd.Address()}, -1, false, `"Outer ""Mixed"[]`},
		{25, []ids.ObjectAddress{public, path[0]}, -1, false, "pg_catalog.text"},
		{16, []ids.ObjectAddress{public, path[0]}, 7, true, "boolean"},
		{1560, []ids.ObjectAddress{public, path[0]}, -1, true, `pg_catalog."bit"`},
		{23, nil, 7, true, "integer"},
	} {
		got, err := regobj.FormatType(view, tc.path, tc.id, tc.mod, tc.specified)
		if err != nil || got != tc.want {
			t.Fatalf("visibility FormatType(%d) = %q, %v; want %q", tc.id, got, err, tc.want)
		}
	}
	// The SQL inquiry's missing fallback and explicit -1 exceptions must not
	// change existing regtype spelling contracts.
	if got := regobj.TypeSpelling(view, path, 4294967295); got != "4294967295" {
		t.Fatal(got)
	}
	if got := regobj.TypeSpelling(view, path, ids.OID(sqltype.BPChar)); got != "character" {
		t.Fatal(got)
	}
}

func TestTypeDescribeBaseType(t *testing.T) {
	state, view, path := typeDescribeCatalog()
	numeric, _ := view.LookupType(sqltype.Numeric)
	inner := sqltype.LookupDomain(81001, "inner_domain", "unused", sqltype.Usage{Type: numeric, Typmod: 327686})
	outer := sqltype.LookupDomain(81002, "outer_domain", "unused", sqltype.Usage{Type: inner, Typmod: -1})
	installType(state, inner, path[1])
	installType(state, outer, path[1])
	array := sqltype.LookupArray(81003, "_outer_domain", outer)
	installType(state, array, path[1])
	arrayDomain := sqltype.LookupDomain(81004, "array_domain", "unused", sqltype.Usage{Type: array, Typmod: -1})
	installType(state, arrayDomain, path[1])
	view = catalog.NewRead(nil, state)
	for _, tc := range []struct {
		id, want sqltype.ID
		found    bool
	}{
		{81002, 1700, true}, {81001, 1700, true}, {81003, 81003, true},
		{81004, 81003, true},
		{23, 23, true}, {1007, 1007, true}, {2276, 2276, true}, {13243, 23, true},
		{0, 0, false}, {4294967295, 0, false},
	} {
		got, found := regobj.BaseType(view, tc.id)
		if got != tc.want || found != tc.found {
			t.Fatalf("BaseType(%d) = %d,%t; want %d,%t", tc.id, got, found, tc.want, tc.found)
		}
	}
	// Actual missing-middle catalog: outer still retains its old physical chain,
	// but the immediate referenced domain has been removed from this snapshot.
	state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 81001})
	view = catalog.NewRead(nil, state)
	if outer.BaseType().ID() != sqltype.Numeric {
		t.Fatal("fixture lost the distinguishing stale physical chain")
	}
	if got, found := regobj.BaseType(view, outer.ID()); got != 0 || found {
		t.Fatalf("missing middle returned stale physical base %d,%t", got, found)
	}
	// Replacing the captured immediate link must use the replacement, rather
	// than the descriptor that the outer domain captured when constructed.
	text, _ := view.LookupType(sqltype.Text)
	replacement := sqltype.LookupDomain(inner.ID(), "inner_domain", "unused", sqltype.Usage{Type: text, Typmod: -1})
	installType(state, replacement, path[1])
	view = catalog.NewRead(nil, state)
	if got, found := regobj.BaseType(view, outer.ID()); got != sqltype.Text || !found {
		t.Fatalf("replacement link = %d,%t; want text,true", got, found)
	}
	state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Text)})
	view = catalog.NewRead(nil, state)
	if got, found := regobj.BaseType(view, outer.ID()); got != 0 || found {
		t.Fatalf("missing terminal base = %d,%t; want invalid", got, found)
	}
}
