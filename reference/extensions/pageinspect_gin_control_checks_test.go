package reference_test

import (
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var ginMovedNames = []string{"meta_info", "opaque_info", "leaf_items"}
var ginViews = []string{"gin_meta_view", "gin_opaque_view", "gin_leaf_view"}

func ginViewStep(i int, rows [][]any) fixture.Step {
	q := "SELECT * FROM " + ginViews[i]
	if i == 2 {
		q += " ORDER BY first_tid"
	}
	return fixture.Select(q, fixture.GINNames[i], fixture.GINTypes[i], rows)
}
func ginChanged(i int) ([]byte, [][]any) {
	switch i {
	case 0:
		p := fixture.GINMeta()
		for _, f := range []struct {
			off   int
			value uint32
		}{{24, 1}, {28, 2}, {32, 3}, {36, 4}, {48, 6}, {52, 7}, {56, 8}, {72, 1}} {
			binary.LittleEndian.PutUint32(p[f.off:], f.value)
		}
		binary.LittleEndian.PutUint64(p[40:], 5)
		binary.LittleEndian.PutUint64(p[64:], 9)
		return p, [][]any{{int64(1), int64(2), int64(3), int64(4), int64(5), int64(6), int64(7), int64(8), int64(9), int64(1)}}
	case 1:
		p := fixture.GINPage(0)
		binary.LittleEndian.PutUint32(p[8184:], 7)
		binary.LittleEndian.PutUint16(p[8188:], 9)
		return p, [][]any{{int64(7), int64(9), "{}"}}
	default:
		return fixture.GINLeaf(fixture.GINSegment{77, 3, []byte{1}}), [][]any{{"(77,3)", int64(1), `{"(77,3)","(77,4)"}`}}
	}
}
func ginStoredInputs(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held []fixture.HeldStatement) {
	t.Helper()
	for i, column := range []string{"meta", "opaque", "leaf"} {
		raw, rows := ginChanged(i)
		null := fixture.GINNullRows(i)
		if i == 2 {
			null = nil
		}
		steps := []struct {
			raw  []byte
			rows [][]any
		}{{raw, rows}, {nil, null}, {make([]byte, 8192), fixture.GINNullRows(i)}}
		if i == 2 {
			steps = append(steps, struct {
				raw  []byte
				rows [][]any
			}{fixture.GINLeaf(), nil})
		}
		steps = append(steps, struct {
			raw  []byte
			rows [][]any
		}{ginOrdinary(i), ginOrdinaryRows(i)})
		for _, tc := range steps {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE gin_images SET "+column+"=$1", 1, tc.raw))
			s := fixture.GINMetadata(ginViewStep(i, tc.rows), b.PG, true)
			ginBoth(t, p, b, c, s)
			if held != nil {
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
			}
		}
	}
}
func ginControlCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, moved bool) {
	t.Helper()
	principal := "postgres"
	if b.PG {
		principal = "stuntgres"
	}
	for i, name := range fixture.GINMembers {
		schema, owner := "gin_part", principal
		if moved {
			name, schema, owner = ginMovedNames[i], "gin_moved", "gin_owner"
		}
		ginBoth(t, p, b, c, fixture.GINDeclaration(i, name, schema, owner))
		ginBoth(t, p, b, c, fixture.GINMetadata(ginViewStep(i, ginOrdinaryRows(i)), b.PG, true))
		empty := ginViewStep(i, nil)
		empty.Query = "SELECT * FROM " + ginViews[i] + " WHERE false"
		ginBoth(t, p, b, c, fixture.GINMetadata(empty, b.PG, true))
		null := fixture.GINNullRows(i)
		if i == 2 {
			null = nil
		}
		steps := []fixture.Step{ginStep(i, ginOrdinary(i), ginOrdinaryRows(i)), ginStep(i, nil, null), ginStep(i, make([]byte, 8192), fixture.GINNullRows(i))}
		raw, want := ginChanged(i)
		steps = append(steps, ginStep(i, raw, want))
		if i == 2 {
			steps = append(steps, ginStep(i, fixture.GINLeaf(), nil))
		}
		for _, s := range steps {
			s.Query = "SELECT * FROM " + schema + "." + name + "(page => $1)"
			if i == 2 {
				s.Query += " ORDER BY first_tid"
			}
			ginBoth(t, p, b, c, fixture.GINMetadata(s, b.PG, false))
		}
		if i < 2 {
			s := fixture.Select("SELECT "+schema+"."+name+"(page => $1)", []string{name}, []string{"RECORD"}, [][]any{{nil}}, nil)
			ginBoth(t, p, b, c, fixture.GINMetadata(s, b.PG, false))
		}
	}
	ginBoth(t, p, b, c, fixture.Select("SELECT meta,opaque,leaf FROM gin_images", []string{"meta", "opaque", "leaf"}, []string{"BYTEA", "BYTEA", "BYTEA"}, [][]any{{string(fixture.GINMeta()), string(fixture.GINPage(0xffff)), string(fixture.GINPosting())}}))
	ginBoth(t, p, b, c, fixture.Select("SELECT n.nspname::text AS target,r.rolname::text AS owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='pageinspect'", []string{"target", "owner"}, []string{"TEXT", "TEXT"}, [][]any{{"gin_part", principal}}))
	if !b.PG {
		rows := [][]any{{"brin_page_type", "gin_part", principal}, {"brin_metapage_info", "gin_part", principal}, {"brin_revmap_data", "gin_part", principal}, {"brin_page_items", "gin_part", principal}, {"bt_page_items", "gin_part", principal}, {"fsm_page_contents", "gin_part", principal}, {"page_header", "gin_part", principal}, {"page_checksum", "gin_part", principal}}
		for _, name := range fixture.HashMembers {
			rows = append(rows, []any{name, "gin_part", principal})
		}
		rows = append(rows, []any{"heap_page_items", "gin_part", principal}, []any{"heap_tuple_infomask_flags", "gin_part", principal})
		for i, name := range fixture.GINMembers {
			schema, owner := "gin_part", principal
			if moved {
				name, schema, owner = ginMovedNames[i], "gin_moved", "gin_owner"
			}
			rows = append(rows, []any{name, schema, owner})
		}
		s := fixture.Select("SELECT p.proname::text AS name,n.nspname::text AS schema,r.rolname::text AS owner FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='pageinspect'", []string{"name", "schema", "owner"}, []string{"TEXT", "TEXT", "TEXT"}, rows)
		s.Bag = true
		ginBoth(t, p, b, c, s)
	}
}
func ginRetainViews(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func() {
	t.Helper()
	snapshots := make([]*sql.Rows, len(fixture.GINMembers))
	for i := range fixture.GINMembers {
		r, err := c.QueryContext(p.Ctx, ginViewStep(i, nil).Query)
		if err != nil {
			t.Fatal(err)
		}
		snapshots[i] = r
		t.Cleanup(func() { r.Close() })
	}
	return func() {
		t.Helper()
		for i, r := range snapshots {
			columns, err := r.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.GINCheckColumns(t, columns, fixture.GINNames[i], fixture.GINTypes[i], false, true)
			var rows [][]any
			for r.Next() {
				row := make([]any, len(fixture.GINNames[i]))
				dest := make([]any, len(row))
				for j := range row {
					dest[j] = &row[j]
				}
				if err := r.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				for j := range row {
					row[j] = fixture.Normalize(row[j])
				}
				rows = append(rows, row)
			}
			if err := r.Err(); err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rows, ginOrdinaryRows(i)) {
				t.Fatal("retained GIN rows changed", i, rows, ginOrdinaryRows(i))
			}
			fixture.GINCheckColumns(t, columns, fixture.GINNames[i], fixture.GINTypes[i], false, true)
		}
	}
}
func ginBadOwner(t *testing.T, dump string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v3 "
	start, end, payload := fixture.CatalogExtensionOwnerDirective(t, dump, "pageinspect", true)
	var record map[string]json.RawMessage
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatal(err)
	}
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"], &members); err != nil {
		t.Fatal(err)
	}
	selected := -1
	for i, member := range members {
		var kind, schema, name, signature, version string
		for key, target := range map[string]*string{"kind": &kind, "schema": &schema, "name": &name, "signature": &signature, "version": &version} {
			if err := json.Unmarshal(member[key], target); err != nil {
				t.Fatal(err)
			}
		}
		if kind == "procedure" && schema == "gin_moved" && name == "leaf_items" && signature == "pg_catalog.bytea" && version == "1.13" && member["declaration"] != nil {
			if selected != -1 {
				t.Fatal("duplicate symbolic GIN leaf declaration")
			}
			selected = i
		}
	}
	if selected == -1 {
		t.Fatal("missing symbolic GIN leaf declaration")
	}
	// Preserve every other identity, ACL and immutable-origin field.
	members[selected]["owner"] = json.RawMessage(`"gin_absent_owner"`)
	var err error
	record["members"], err = json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	payload, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return dump[:start] + marker + base64.StdEncoding.EncodeToString(payload) + dump[end:]
}
