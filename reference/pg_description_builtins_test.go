package reference_test

import (
	"fmt"
	"testing"
)

type pgDescriptionSeed struct {
	oid  int64
	text string
}

// Independent REL_18_0 pg_type.dat and pg_collation.dat descr fields.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_type.dat
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_collation.dat
var pgDescriptionScalarTypes = []pgDescriptionSeed{
	{16, "boolean, format 't'/'f'"},
	{17, "variable-length string, binary values escaped"},
	{18, "single character"},
	{19, "63-byte type for storing system identifiers"},
	{20, "~18 digit integer, 8-byte storage"},
	{21, "-32 thousand to 32 thousand, 2-byte storage"},
	{23, "-2 billion to 2 billion integer, 4-byte storage"},
	{25, "variable-length string, no limit specified"},
	{26, "object identifier(oid), maximum 4 billion"},
	{114, "JSON stored as text"},
	{194, "string representing an internal node tree"},
	{700, "single-precision floating point number, 4-byte storage"},
	{701, "double-precision floating point number, 8-byte storage"},
	{705, "pseudo-type representing an undetermined type"},
	{790, "monetary amounts, $d,ddd.cc"},
	{1042, "'char(length)' blank-padded string, fixed storage length"},
	{1043, "'varchar(length)' non-blank-padded string, variable storage length"},
	{1082, "date"},
	{1083, "time of day"},
	{1114, "date and time"},
	{1184, "date and time with time zone"},
	{1186, "time interval, format 'number units ...'"},
	{1266, "time of day with time zone"},
	{1700, "'numeric(precision, scale)' arbitrary precision number"},
	{2205, "registered class"},
	{2278, "pseudo-type for the result of a function with no real result"},
	{2950, "UUID"},
}

var pgDescriptionCollations = []pgDescriptionSeed{
	{100, "database's default collation"},
	{950, "standard C collation"},
	{951, "standard POSIX collation"},
	{962, "sorts by Unicode code point, C character semantics"},
	{963, "sorts using the Unicode Collation Algorithm with default settings"},
	{811, "sorts by Unicode code point; Unicode and POSIX character semantics"},
	{6411, "sorts by Unicode code point; Unicode character semantics"},
}

func TestReferencePGDescriptionBuiltins(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			direct, prepared, expected := [][]any{}, [][]any{}, [][]any{}
			for _, group := range []struct {
				class int64
				seeds []pgDescriptionSeed
			}{
				{2615, []pgDescriptionSeed{{11, "system catalog schema"}}},
				{1247, pgDescriptionScalarTypes},
				{3456, pgDescriptionCollations},
				{2607, pgDescriptionConversions},
			} {
				for _, seed := range group.seeds {
					// Do not filter subid: unexpected subobjects and duplicate
					// keys must fail the independently specified singleton bag.
					query := fmt.Sprintf("SELECT * FROM pg_catalog.pg_description WHERE classoid=%d AND objoid=%d", group.class, seed.oid)
					s := pgDescriptionSelect(query, [][]any{{seed.oid, group.class, int64(0), seed.text}})
					direct = append(direct, pgDescriptionRows(t, p, backend, s)...)
					prepared = append(prepared, pgDescriptionRows(t, p, backend, referencePrepared(s))...)
					expected = append(expected, []any{seed.oid, group.class, int64(0), seed.text})
				}
			}
			if len(expected) != 163 {
				t.Fatal("represented baseline inventory", len(expected))
			}
			for _, observed := range [][][]any{direct, prepared} {
				if _, matches := referenceCompareRows(observed, expected, true); !matches {
					t.Fatal("complete represented seed bag", observed, expected)
				}
			}
			for _, query := range []string{
				"SELECT * FROM pg_catalog.pg_description WHERE classoid=1247 AND (objoid=1000 OR objoid=1001 OR objoid=1002 OR objoid=1003 OR objoid=1016 OR objoid=1005 OR objoid=1007 OR objoid=1009 OR objoid=1028 OR objoid=199 OR objoid=1021 OR objoid=1022 OR objoid=1014 OR objoid=1015 OR objoid=1182 OR objoid=1183 OR objoid=1115 OR objoid=1185 OR objoid=1187 OR objoid=1270 OR objoid=1231 OR objoid=2210 OR objoid=2951)",
				"SELECT * FROM pg_catalog.pg_description WHERE classoid=2607 AND (objoid=4450 OR objoid=4451)",
				"SELECT * FROM pg_catalog.pg_description WHERE objoid=114 AND classoid=1259",
				"SELECT * FROM pg_catalog.pg_description WHERE objoid=950 AND classoid=2607",
				"SELECT * FROM pg_catalog.pg_description WHERE objoid=4402 AND classoid=3456",
			} {
				s := pgDescriptionSelect(query, [][]any{})
				pgDescriptionRows(t, p, backend, s)
				pgDescriptionRows(t, p, backend, referencePrepared(s))
			}
			// Migrated complete native-only builtin inquiry assertions. Each
			// description and wrong-class NULL is now checked against both backends.
			for _, seed := range append(append([]pgDescriptionSeed{}, pgDescriptionScalarTypes...), pgDescriptionSeed{3802, "Binary JSON"}) {
				for _, prepared := range []bool{false, true} {
					inquiry := referenceSelect("SELECT obj_description($1,'pg_type')", []string{"obj_description"}, []string{"TEXT"}, [][]any{{seed.text}}, seed.oid)
					wrong := referenceSelect("SELECT obj_description($1,'pg_namespace')", []string{"obj_description"}, []string{"TEXT"}, [][]any{{nil}}, seed.oid)
					inquiry.prepared, wrong.prepared = prepared, prepared
					commentInquiryStep(t, p, backend, backend.db, inquiry)
					commentInquiryStep(t, p, backend, backend.db, wrong)
				}
			}
			for _, class := range []string{"pg_type", "pg_namespace"} {
				inquiry := referenceSelect("SELECT obj_description(1003,'"+class+"')", []string{"obj_description"}, []string{"TEXT"}, [][]any{{nil}})
				commentInquiryStep(t, p, backend, backend.db, inquiry)
				commentInquiryStep(t, p, backend, backend.db, referencePrepared(inquiry))
			}
			for _, tc := range []struct {
				oid           int64
				catalog, text string
			}{
				{950, "pg_collation", "standard C collation"},
				{4402, "pg_conversion", "conversion for KOI8R to MULE_INTERNAL"},
			} {
				// Projecting these seeds does not widen native inquiry routing.
				var want any
				if backend.pg {
					want = tc.text
				}
				s := referenceSelect("SELECT obj_description($1,$2)", []string{"obj_description"}, []string{"TEXT"}, [][]any{{want}}, tc.oid, tc.catalog)
				commentInquiryStep(t, p, backend, backend.db, s)
				commentInquiryStep(t, p, backend, backend.db, referencePrepared(s))
				if !backend.pg {
					legacy := referenceSelect("SELECT obj_description($1)", []string{"obj_description"}, []string{"TEXT"}, [][]any{{nil}}, tc.oid)
					commentInquiryStep(t, p, backend, backend.db, legacy)
					commentInquiryStep(t, p, backend, backend.db, referencePrepared(legacy))
				}
			}
			// The real fixture recreates public. Discover its current address after
			// an explicit comment, rather than asserting bootstrap OID 2200.
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("COMMENT ON SCHEMA public IS 'pgd builtin public witness'", 0))
			var oid int64
			if err := backend.db.QueryRowContext(p.ctx, "SELECT objoid FROM pg_catalog.pg_description WHERE classoid=2615 AND description='pgd builtin public witness'").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal("public namespace identity", oid, err)
			}
			s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=2615", [][]any{{oid, int64(2615), int64(0), "pgd builtin public witness"}}, oid)
			pgDescriptionRows(t, p, backend, s)
			pgDescriptionRows(t, p, backend, referencePrepared(s))
			inquiry := referenceSelect("SELECT obj_description($1,'pg_namespace')", []string{"obj_description"}, []string{"TEXT"}, [][]any{{"pgd builtin public witness"}}, oid)
			commentInquiryStep(t, p, backend, backend.db, inquiry)
			commentInquiryStep(t, p, backend, backend.db, referencePrepared(inquiry))
		})
	}
}
