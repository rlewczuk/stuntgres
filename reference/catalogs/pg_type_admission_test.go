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
		if b.PG {
			return
		} // PostgreSQL genuinely implements additional families.
		expected := make(map[int64]bool)
		for _, r := range pgTypeBuiltinFacts {
			expected[r[0].(int64)] = true
		}
		expected[13254], expected[13253] = true, true
		expected[13248], expected[13247] = true, true
		expected[13256], expected[13255] = true, true
		expected[13243], expected[13242] = true, true
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
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT oid FROM pg_type WHERE oid IN (22,30,142,143,600,1017,1006,1013,1790,2201,4533,4534,4535,6152,6153,6155) ORDER BY oid", []string{"oid"}, []string{"OID"}, nil))
		// Lookup identity is deliberately broader than SQL catalog/codec admission.
		for _, id := range []int64{142, 600, 4533} {
			literal := fmt.Sprint(id)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT '"+literal+"'::regtype::bigint AS identity", []string{"identity"}, []string{"INT8"}, [][]any{{id}}))
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT rngmultitypid FROM pg_range WHERE rngtypid IN (3908,3910,3912) ORDER BY rngtypid", []string{"rngmultitypid"}, []string{"OID"}, [][]any{{int64(4533)}, {int64(4534)}, {int64(4535)}}))
	})
}
