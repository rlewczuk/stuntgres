package enginetests_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

type networkPrivateCase struct {
	typ, lower, boundary, upper, absent string
	input, search, comparator           ids.OID
	operators                           [5]ids.OID
}

var networkPrivateCases = []networkPrivateCase{
	{"inet", "0.0.0.0/0", "10.0.0.0/24", "::/0", "10.0.1.0/24", 869, 869, 926, [5]ids.OID{1203, 1204, 1201, 1206, 1205}},
	{"cidr", "0.0.0.0/0", "10.0.0.0/24", "::/0", "10.0.1.0/24", 650, 869, 926, [5]ids.OID{1203, 1204, 1201, 1206, 1205}},
	{"macaddr", "00:00:00:00:00:00", "80:00:00:00:00:00", "ff:ff:ff:ff:ff:ff", "80:00:00:00:00:01", 829, 829, 836, [5]ids.OID{1222, 1223, 1220, 1225, 1224}},
	{"macaddr8", "00:00:00:00:00:00:00:00", "80:00:00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff:ff:ff", "80:00:00:00:00:00:00:01", 774, 774, 4119, [5]ids.OID{3364, 3365, 3362, 3367, 3366}},
	{"uuid", "00000000-0000-0000-0000-000000000000", "80000000-0000-0000-0000-000000000000", "ffffffff-ffff-ffff-ffff-ffffffffffff", "80000000-0000-0000-0000-000000000001", 2950, 2950, 2960, [5]ids.OID{2974, 2976, 2972, 2977, 2975}},
	{"oid", "0", "2147483648", "4294967295", "2147483649", 26, 26, 356, [5]ids.OID{609, 611, 607, 612, 610}},
}

func networkPrivateInsert(id int, boundary bool) string {
	values := []string{fmt.Sprint(id)}
	for _, tc := range networkPrivateCases {
		text := tc.lower
		if boundary {
			text = tc.boundary
		}
		values = append(values, "'"+text+"'")
	}
	return "INSERT INTO network_private VALUES(" + strings.Join(values, ",") + ")"
}

// Like compoundEngine, SQL only constructs actual catalog/storage owners here;
// every assertion inspects bindings, generations or physical posting ownership.
func networkEngine(t *testing.T, populated bool) (*core.Engine, *catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	return ginFamilyEngine(t, "network_private", networkPrivateCases, populated)
}

func ginFamilyEngine(t *testing.T, table string, cases []networkPrivateCase, populated bool) (*core.Engine, *catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	e := engine.New()
	columns := []string{"id integer"}
	var keys []string
	for _, tc := range cases {
		columns = append(columns, tc.typ+" "+tc.typ)
		keys = append(keys, tc.typ+" "+tc.typ+"_ops")
	}
	for _, q := range []string{"CREATE EXTENSION btree_gin VERSION '1.3'", "CREATE TABLE " + table + "(" + strings.Join(columns, ",") + ")", "CREATE INDEX " + table + "_i ON " + table + " USING gin(" + strings.Join(keys, ",") + ")"} {
		enginetest.Run(t, e, q)
	}
	if populated {
		for row := range 5 {
			values := []string{fmt.Sprint(row + 1)}
			for _, tc := range cases {
				text := tc.lower
				if row == 1 || row == 2 {
					text = tc.boundary
				}
				if row == 3 {
					text = tc.upper
				}
				value := "'" + text + "'"
				if row == 4 {
					value = "NULL"
				}
				values = append(values, value)
			}
			enginetest.Run(t, e, "INSERT INTO "+table+" VALUES("+strings.Join(values, ",")+")")
		}
	}
	idx, root := ginInstalledRoot(t, e, table+"_i")
	if populated && (len(root.Gin.Pending) != len(cases) || storage.GinPendingIncidences(root.Gin) == 0) {
		t.Fatal("populated fixture lacks genuine deferred postings")
	}
	if populated {
		for _, column := range root.Gin.Columns {
			if len(column.Tokens) != 0 || len(column.NonNull) != 0 {
				t.Fatal("DML fixture unexpectedly populated bulk/main inversion")
			}
		}
	}
	return e, idx, root
}
func networkPhysical(t *testing.T) (*core.Engine, *catalog.Relation, *catalog.Relation, *storage.Relation, *storage.IndexRoot) {
	t.Helper()
	e, idx, _ := networkEngine(t, false)
	view := e.CatalogRead()
	parent := view.MustRelation(idx.SQLIndex.Parent)
	integer, _ := view.LookupType(sqltype.Int4)
	store := &storage.Relation{}
	for row := range 5 {
		id, _ := sqltype.Integer(integer, int64(row+1))
		values := []sqltype.Value{id}
		for col, tc := range networkPrivateCases {
			typ := parent.Columns[col+1].Usage.Type
			text := tc.lower
			if row == 1 || row == 2 {
				text = tc.boundary
			}
			if row == 3 {
				text = tc.upper
			}
			value := sqltype.Null(typ)
			if row != 4 {
				value = ginInput(t, typ, text)
			}
			values = append(values, value)
		}
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(101 + row), Version: ids.VersionID(201 + row), Born: 1, Values: values})
	}
	eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	root, err := index.BuildIndexRoot(eval, parent, idx.SQLIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	ginAssertOwners(t, root, store)
	return e, idx, parent, store, root
}
