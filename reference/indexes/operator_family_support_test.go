package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceOperatorFamilySupportRoutines(t *testing.T) {
	familyScenario(t, func(t *testing.T, _ context.Context, _ *sql.Conn, run func(fixture.Step)) {
		for _, prepared := range []bool{false, true} {
			// The scalar contract adds these three support routines, not the
			// operator implementation's otherwise unexposed int4mi SQL spelling.
			// The package root test proves selected routine 181 versus 351.
			s := fixture.Select(`SELECT btint4cmp(1,2),btint4cmp(2,2),btint4cmp(2,1),btint4cmp(1,3),1-3 AS subtraction,btint4cmp(NULL,1)`, []string{"btint4cmp", "btint4cmp", "btint4cmp", "btint4cmp", "subtraction", "btint4cmp"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{
				{int64(-1), int64(0), int64(1), int64(-1), int64(-2), nil},
			})
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			s = fixture.Select(`SELECT n,hashint4(n),hashint4extended(n,0),hashint4extended(n,1),hashint4extended(n,42) FROM (VALUES (-2147483648),(-1),(0),(1),(2),(42),(2147483647),(NULL::integer)) v(n) ORDER BY n NULLS LAST`, []string{"n", "hashint4", "hashint4extended", "hashint4extended", "hashint4extended"}, []string{"INT4", "INT4", "INT8", "INT8", "INT8"}, [][]any{
				{int64(-2147483648), int64(-1524351049), int64(1977219185673256887), int64(3044001184559810835), int64(2517197663604715469)},
				{int64(-1), int64(385747274), int64(-1888257769727981238), int64(3987656643895367148), int64(-2218543911767281624)},
				{int64(0), int64(-272711505), int64(4154612158245552303), int64(434346878670890809), int64(-8162688053457160230)},
				{int64(1), int64(-1905060026), int64(-3670598878359251130), int64(-7620678293940249823), int64(2207685931686633351)},
				{int64(2), int64(1134484726), int64(3647684041653542134), int64(7846366767813865367), int64(403280637362415369)},
				{int64(42), int64(1509752520), int64(8010225493015854792), int64(3812564155960986560), int64(826591455424871831)},
				{int64(2147483647), int64(-96758253), int64(-9109110466470504941), int64(2560836655869490746), int64(-4216469540329842178)},
				{nil, nil, nil, nil, nil},
			})
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			s = fixture.Select(`SELECT pg_catalog.btint4cmp(1,NULL::integer),pg_catalog.hashint4(NULL::integer),pg_catalog.hashint4extended(1,NULL::bigint)`, []string{"btint4cmp", "hashint4", "hashint4extended"}, []string{"INT4", "INT4", "INT8"}, [][]any{{nil, nil, nil}})
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
		}
		// Assert the existing bounded pg_proc surface; family support does not
		// implement the separately scoped full pg_proc catalog projection.
		run(fixture.Select(`SELECT oid,pronamespace,proname,proowner,provolatile,proisstrict,pronargs,prorettype FROM pg_proc WHERE oid IN (351,425,450) ORDER BY oid`, []string{"oid", "pronamespace", "proname", "proowner", "provolatile", "proisstrict", "pronargs", "prorettype"}, []string{"OID", "OID", "NAME", "OID", "CHAR", "BOOL", "INT2", "OID"}, [][]any{
			{int64(351), int64(11), "btint4cmp", int64(10), "i", true, int64(2), int64(23)},
			{int64(425), int64(11), "hashint4extended", int64(10), "i", true, int64(2), int64(20)},
			{int64(450), int64(11), "hashint4", int64(10), "i", true, int64(1), int64(23)},
		}))
	})
}
