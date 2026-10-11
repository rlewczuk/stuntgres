package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommentInquiryBuiltins(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			// REL_18_0 pg_namespace.dat seeds public2200 with "standard public
			// schema". The shared oracle fixture deliberately drops that namespace
			// and creates an uncommented public with a new OID before every pair.
			// Native Reset instead restores its seeded bootstrap namespace.
			var publicWant any = "standard public schema"
			if backend.PG {
				publicWant = nil
				var publicOID int64
				if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&publicOID); err != nil {
					t.Fatal(err)
				}
				if publicOID == 2200 {
					t.Fatal("oracle fixture did not recreate public")
				}
				s := fixture.Select("SELECT obj_description($1,'pg_namespace')", []string{"obj_description"}, []string{"TEXT"}, [][]any{{nil}}, publicOID)
				commentInquiryStep(t, p, backend, db, s)
				commentInquiryStep(t, p, backend, db, fixture.Prepared(s))
			}
			for _, tc := range []struct {
				oid     int
				catalog string
				want    any
			}{
				{11, "pg_namespace", "system catalog schema"}, {2200, "pg_namespace", publicWant},
				{16, "pg_type", "boolean, format 't'/'f'"}, {17, "pg_type", "variable-length string, binary values escaped"},
				{18, "pg_type", "single character"}, {19, "pg_type", "63-byte type for storing system identifiers"},
				{20, "pg_type", "~18 digit integer, 8-byte storage"}, {21, "pg_type", "-32 thousand to 32 thousand, 2-byte storage"},
				{23, "pg_type", "-2 billion to 2 billion integer, 4-byte storage"}, {25, "pg_type", "variable-length string, no limit specified"},
				{26, "pg_type", "object identifier(oid), maximum 4 billion"}, {700, "pg_type", "single-precision floating point number, 4-byte storage"},
				{114, "pg_type", "JSON stored as text"}, {194, "pg_type", "string representing an internal node tree"},
				{701, "pg_type", "double-precision floating point number, 8-byte storage"}, {705, "pg_type", "pseudo-type representing an undetermined type"},
				{1042, "pg_type", "'char(length)' blank-padded string, fixed storage length"}, {1043, "pg_type", "'varchar(length)' non-blank-padded string, variable storage length"},
				{1082, "pg_type", "date"}, {1083, "pg_type", "time of day"}, {1114, "pg_type", "date and time"}, {1184, "pg_type", "date and time with time zone"},
				{1186, "pg_type", "time interval, format 'number units ...'"}, {1266, "pg_type", "time of day with time zone"},
				{1700, "pg_type", "'numeric(precision, scale)' arbitrary precision number"}, {2205, "pg_type", "registered class"},
				{2278, "pg_type", "pseudo-type for the result of a function with no real result"}, {2950, "pg_type", "UUID"}, {1003, "pg_type", nil},
			} {
				s := fixture.Select("SELECT obj_description($1,$2)", []string{"obj_description"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.oid, tc.catalog)
				commentInquiryStep(t, p, backend, db, s)
				commentInquiryStep(t, p, backend, db, fixture.Prepared(s))
			}
		})
	}
}
