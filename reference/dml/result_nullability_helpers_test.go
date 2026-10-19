package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The parameters are shared by the SELECT and four RETURNING statements:
// key, enabled, include implicit schemas, inquiry OID, index property, ACL privileges.
// OID-to-int is an admitted, nonredundant coercion of a known-nonnullable
// inquiry. Its wrapper must remain unknown rather than inheriting that claim;
// COLLATE separately wraps current_schema's known-nullable claim.
const resultNullabilityProjection = `current_schema AS schema_name,
 current_schemas($3::bool) AS paths,
 pg_my_temp_schema() AS my_temp,
 pg_is_other_temp_schema($4::oid) AS other_temp,
 pg_has_role(NULL::oid,NULL::oid,'USAGE') AS has_role,
 row_security_active(NULL::oid) AS row_security,
 pg_get_acl(NULL::oid,NULL::oid,NULL::int) AS object_acl,
 pg_get_acl(1259::oid,0::oid,0::int) AS missing_acl,
 acldefault(NULL::"char",0::oid) AS default_acl,
 makeaclitem(0::oid,0::oid,$6::text,false) AS acl_item,
 NULL::aclitem[] @> NULL::aclitem AS acl_contains,
 pg_blocking_pids(NULL::int) AS blockers,
 pg_safe_snapshot_blocking_pids(NULL::int) AS snapshot_blockers,
 pg_get_indexdef(0::oid) AS index_def,
 pg_indexam_has_property($4::oid,$5::text) AS am_property,
 pg_index_has_property(0::oid,'clusterable') AS index_property,
 pg_index_column_has_property(0::oid,1,'asc') AS column_property,
 pg_indexam_has_property(NULL::oid,'can_order') AS folded_property,
 id+1 AS computed,
 pg_my_temp_schema()::int AS coerced_temp,
 current_schema COLLATE "C" AS collated_schema,
 current_database() AS database_name`

var resultNullabilityNames = []string{
	"schema_name", "paths", "my_temp", "other_temp", "has_role", "row_security",
	"object_acl", "missing_acl", "default_acl", "acl_item", "acl_contains",
	"blockers", "snapshot_blockers", "index_def", "am_property", "index_property",
	"column_property", "folded_property", "computed", "coerced_temp", "collated_schema", "database_name",
}

var resultNullabilityTypes = []string{
	"NAME", "_NAME", "OID", "BOOL", "BOOL", "BOOL", "_ACLITEM", "_ACLITEM",
	"_ACLITEM", "ACLITEM", "BOOL", "_INT4", "_INT4", "TEXT", "BOOL", "BOOL", "BOOL",
	"BOOL", "INT4", "INT4", "NAME", "NAME",
}

type resultNullabilityPair struct{ nullable, known bool }

func resultNullabilityPairs(returning bool) []resultNullabilityPair {
	pairs := make([]resultNullabilityPair, len(resultNullabilityNames))
	for i := range 17 {
		pairs[i] = resultNullabilityPair{true, true}
	}
	pairs[2] = resultNullabilityPair{false, true}
	if returning {
		for i := 13; i <= 16; i++ {
			pairs[i] = resultNullabilityPair{}
		}
	}
	return pairs
}

// CheckMetadata runs before fixture iteration, including on empty results. Keep
// actual ColumnType objects, not just copied answers, to check description ownership.
func resultNullabilityMetadata(t *testing.T, pg bool, names, types []string, pairs []resultNullabilityPair) (func(*testing.T, *sql.Rows), func()) {
	t.Helper()
	var retained []*sql.ColumnType
	check := func(columns []*sql.ColumnType) {
		t.Helper()
		if len(columns) != len(names) {
			t.Fatalf("metadata width got %d want %d", len(columns), len(names))
		}
		for i, col := range columns {
			if col.Name() != names[i] || col.DatabaseTypeName() != types[i] {
				t.Fatalf("column %d got %s/%s want %s/%s", i, col.Name(), col.DatabaseTypeName(), names[i], types[i])
			}
			want := pairs[i]
			if pg {
				want = resultNullabilityPair{}
			}
			n, k := col.Nullable()
			if (resultNullabilityPair{n, k}) != want {
				t.Fatalf("%s Nullable got (%v,%v) want (%v,%v)", names[i], n, k, want.nullable, want.known)
			}
		}
	}
	return func(t *testing.T, rows *sql.Rows) {
			t.Helper()
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			check(columns)
			if retained == nil && !pg {
				retained = columns
			}
		}, func() {
			if retained != nil {
				check(retained)
			}
		}
}

func resultNullabilityValues(t *testing.T, b fixture.Backend, id int, schema, paths, other, property, acl any) []any {
	t.Helper()
	var oid any = int64(0)
	database := "reference/" + strings.TrimSuffix(t.Name(), "/"+b.Name)
	database = database[:min(len(database), 63)]
	if b.PG {
		oid = strconv.FormatInt(0, 10)
		database = "stuntgres_reference"
	}
	return []any{
		schema, paths, oid, other, nil, nil, nil, nil, nil, acl, nil, nil, nil,
		nil, property, nil, nil, nil, int64(id + 1), int64(0), schema, database,
	}
}
