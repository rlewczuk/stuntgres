package reference_test

import (
	"fmt"
	"sort"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTypeAdmission(t *testing.T) {
	pgTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// These real represented catalogs must expose reciprocal row/array addresses
		// on both backends. Physical index/TOAST relations are not synthesized.
		q := "SELECT c.oid,t.oid=c.reltype AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,a.typrelid=0 AS array_relation FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid IN (1247,1249,1255,1259) ORDER BY c.oid"
		var links [][]any
		for _, id := range []int64{1247, 1249, 1255, 1259} {
			links = append(links, []any{id, true, true, true, true})
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select(q, []string{"oid", "row_link", "relation_link", "element_link", "array_relation"}, []string{"OID", "BOOL", "BOOL", "BOOL", "BOOL"}, links))
		// The selected predefined domain and canonical companion are real
		// catalog identities, independently of executable array admission.
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT t.typtype,t.typbasetype::integer AS base,t.typtypmod,t.typcollation::integer AS collation,(a.typelem=t.oid AND a.typnamespace=t.typnamespace) AS companion FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE n.nspname='information_schema' AND t.typname='yes_or_no'", []string{"typtype", "base", "typtypmod", "collation", "companion"}, []string{"CHAR", "INT4", "INT4", "INT4", "BOOL"}, [][]any{{"d", int64(1043), int64(7), int64(950), true}}))
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT t.typtype,t.typbasetype::integer AS base,t.typtypmod,t.typcollation::integer AS collation,(a.typelem=t.oid AND a.typnamespace=t.typnamespace) AS companion FROM pg_catalog.pg_type t JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE n.nspname='information_schema' AND t.typname='cardinal_number'", []string{"typtype", "base", "typtypmod", "collation", "companion"}, []string{"CHAR", "INT4", "INT4", "INT4", "BOOL"}, [][]any{{"d", int64(23), int64(-1), int64(0), true}}))
		// Raw geometric typelem facts are distinct from companion SQL-array
		// metadata. Native companions remain lookup-only, not executable arrays.
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid,typelem,typarray FROM pg_type WHERE oid IN (600,601,603,1017,1018,1020) ORDER BY oid", []string{"oid", "typelem", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{
			{int64(600), int64(701), int64(1017)}, {int64(601), int64(600), int64(1018)}, {int64(603), int64(600), int64(1020)},
			{int64(1017), int64(600), int64(0)}, {int64(1018), int64(601), int64(0)}, {int64(1020), int64(603), int64(0)},
		}))
		// A released canonical companion is metadata even when native array
		// datums remain lookup-only. Scalar circle admission must preserve it.
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT t.oid,t.typname,t.typarray,a.oid AS array_oid,a.typname AS array_name,a.typelem FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=718", []string{"oid", "typname", "typarray", "array_oid", "array_name", "typelem"}, []string{"OID", "NAME", "OID", "OID", "NAME", "OID"}, [][]any{{int64(718), "circle", int64(719), int64(719), "_circle", int64(718)}}))
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT t.oid,t.typname,t.typarray,a.oid AS array_oid,a.typname AS array_name,a.typelem FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=628", []string{"oid", "typname", "typarray", "array_oid", "array_name", "typelem"}, []string{"OID", "NAME", "OID", "OID", "NAME", "OID"}, [][]any{{int64(628), "line", int64(629), int64(629), "_line", int64(628)}}))
		// Polygon has no typelem/subscript authority; its companion remains
		// visible metadata without admitting executable polygon arrays.
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT t.oid,t.typname,t.typelem,t.typarray,a.oid AS array_oid,a.typname AS array_name,a.typelem FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=604", []string{"oid", "typname", "typelem", "typarray", "array_oid", "array_name", "typelem"}, []string{"OID", "NAME", "OID", "OID", "OID", "NAME", "OID"}, [][]any{{int64(604), "polygon", int64(0), int64(1027), int64(1027), "_polygon", int64(604)}}))
		// The selected scalar has its genuine released identity and companion
		// OID in both modes; this does not claim companion codec admission.
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT t.oid,t.typname,t.typarray,t.typcategory,t.typcollation,a.oid AS array_oid,a.typname AS array_name,a.typelem FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=1790", []string{"oid", "typname", "typarray", "typcategory", "typcollation", "array_oid", "array_name", "typelem"}, []string{"OID", "NAME", "OID", "CHAR", "OID", "OID", "NAME", "OID"}, [][]any{{int64(1790), "refcursor", int64(2201), "U", int64(0), int64(2201), "_refcursor", int64(1790)}}))
		// OIDVECTOR is concrete; its real canonical companion is metadata only.
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT t.oid,t.typname,t.typelem,t.typarray,a.oid AS array_oid,a.typname AS array_name,a.typelem FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=30", []string{"oid", "typname", "typelem", "typarray", "array_oid", "array_name", "typelem"}, []string{"OID", "NAME", "OID", "OID", "OID", "NAME", "OID"}, [][]any{{int64(30), "oidvector", int64(26), int64(1013), int64(1013), "_oidvector", int64(30)}}))
		// The exact pg_statistic row/array description is catalog metadata
		// with NULL-only result authority, not a pg_statistic reader or codec.
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid::bigint AS oid,typtype,typrelid::bigint AS relation,typelem::bigint AS element,typarray::bigint AS array FROM pg_type WHERE oid IN(10028,10029) ORDER BY oid", []string{"oid", "typtype", "relation", "element", "array"}, []string{"INT8", "CHAR", "INT8", "INT8", "INT8"}, [][]any{{int64(10028), "b", int64(0), int64(10029), int64(0)}, {int64(10029), "c", int64(2619), int64(0), int64(10028)}}))
		if b.PG {
			return
		} // PostgreSQL genuinely implements additional families.
		expected := make(map[int64]bool)
		for _, r := range pgTypeBuiltinFacts {
			expected[r[0].(int64)] = true
		}
		// Released pseudo-type declaration identities are catalog metadata,
		// not blanket storage, codec or callable-provider authority. Include
		// only the selected finite inventory and its canonical companions.
		for _, id := range []int64{
			32, 269, 325, 705, 1263, 2249, 2275, 2276, 2277, 2278,
			2279, 2280, 2281, 2283, 2287, 2776, 3115, 3310, 3500,
			3831, 3838, 4537, 4538, 5077, 5078, 5079, 5080,
		} {
			expected[id] = true
		}
		expected[13254], expected[13253] = true, true
		expected[13248], expected[13247] = true, true
		expected[13256], expected[13255] = true, true
		expected[13243], expected[13242] = true, true
		expected[13246], expected[13245] = true, true
		// MACADDR's scalar metadata and associated lookup identity are visible;
		// catalog presence does not make the companion an executable array.
		expected[829], expected[1040] = true, true
		// MACADDR8's scalar admission likewise exposes only companion metadata.
		expected[774], expected[775] = true, true
		expected[600], expected[603], expected[1017], expected[1020] = true, true, true, true
		// LSEG is concrete; its canonical companion remains metadata-only.
		expected[601], expected[1018] = true, true
		// Circle's executable scalar metadata exposes its canonical companion;
		// this catalog identity does not admit circle array values.
		expected[718], expected[719] = true, true
		// LINE's scalar admission exposes metadata, not companion array execution.
		expected[628], expected[629] = true, true
		// PATH is concrete, but its companion is catalog identity only.
		expected[602], expected[1019] = true, true
		// Polygon604 is concrete; companion1027 has metadata but no codec.
		expected[604], expected[1027] = true, true
		// REFCURSOR is now a concrete scalar. Its canonical companion is
		// advertised catalog metadata, not executable array admission.
		expected[1790], expected[2201] = true, true
		expected[22], expected[30], expected[1006], expected[1013] = true, true, true, true
		expected[10028], expected[10029] = true, true
		// Independently discover actual represented relation addresses from pg_class,
		// and their companion identities from REGTYPE, never pg_type rows/facts.
		rows, err := b.DB.QueryContext(p.Ctx, "SELECT c.reltype,n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.reltype<>0 ORDER BY c.oid")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		type relationIdentity struct {
			id           int64
			schema, name string
		}
		var relations []relationIdentity
		for rows.Next() {
			var r relationIdentity
			if err := rows.Scan(&r.id, &r.schema, &r.name); err != nil {
				t.Fatal(err)
			}
			relations = append(relations, r)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		quote := func(s string) string { return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\"" }
		for _, r := range relations {
			declared := quote(r.schema) + "." + quote(r.name)
			if got := pgTypeID(t, p, b, declared); got != r.id {
				t.Fatal("relation/REGTYPE identity disagreement", r, got)
			}
			array := pgTypeID(t, p, b, declared+"[]")
			if array == r.id {
				t.Fatal("row/array identity alias", r)
			}
			expected[r.id], expected[array] = true, true
		}
		ids := make([]int64, 0, len(expected))
		for id := range expected {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var want [][]any
		for _, id := range ids {
			want = append(want, []any{id})
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid FROM pg_type ORDER BY oid", []string{"oid"}, []string{"OID"}, want))
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid FROM pg_type WHERE oid IN (142,143,4533,4534,4535,6152,6153,6155) ORDER BY oid", []string{"oid"}, []string{"OID"}, nil))
		// Lookup identity is deliberately broader than SQL catalog/codec admission.
		for _, id := range []int64{142, 4533} {
			literal := fmt.Sprint(id)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT '"+literal+"'::regtype::bigint AS identity", []string{"identity"}, []string{"INT8"}, [][]any{{id}}))
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT rngmultitypid FROM pg_range WHERE rngtypid IN (3908,3910,3912) ORDER BY rngtypid", []string{"rngmultitypid"}, []string{"OID"}, [][]any{{int64(4533)}, {int64(4534)}, {int64(4535)}}))
	})
}
