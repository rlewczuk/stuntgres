package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func familyReviewScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	familyBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		body(t, p, b, conn, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) })
	})
}

func TestReferenceOperatorFamilyReviewVolatility(t *testing.T) {
	// Independently observed support_declarations, pinned 18.6 oracle. Cast OIDs
	// only to avoid conflating wire codecs with these bounded declaration facts.
	familyReviewScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, _ *sql.Conn, run func(fixture.Step)) {
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(`SELECT oid::bigint,pronamespace::bigint,proname,proowner::bigint,provolatile,proisstrict,pronargs,prorettype::bigint FROM pg_proc WHERE oid IN (2357,2383,2526,2533) ORDER BY oid`,
				[]string{"oid", "pronamespace", "proname", "proowner", "provolatile", "proisstrict", "pronargs", "prorettype"},
				[]string{"INT8", "INT8", "NAME", "INT8", "CHAR", "BOOL", "INT2", "INT8"}, [][]any{
					{int64(2357), int64(11), "date_cmp_timestamptz", int64(10), "s", true, int64(2), int64(23)},
					{int64(2383), int64(11), "timestamptz_cmp_date", int64(10), "s", true, int64(2), int64(23)},
					{int64(2526), int64(11), "timestamp_cmp_timestamptz", int64(10), "s", true, int64(2), int64(23)},
					{int64(2533), int64(11), "timestamptz_cmp_timestamp", int64(10), "s", true, int64(2), int64(23)},
				})
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
		}
	})
}

func TestReferenceOperatorFamilyReviewLookupMetadata(t *testing.T) {
	var emitted string
	familyReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		// REL_18_6 pg_proc.dat: 399 hashmacaddr(macaddr)->int4 and
		// 1219 int8inc(int8)->int8. Admission is not scalar executability or a class.
		run(fixture.Exec(`CREATE OPERATOR FAMILY public.family_review_lookup USING hash`, 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec(`ALTER OPERATOR FAMILY public.family_review_lookup USING hash ADD FUNCTION 1 pg_catalog.hashmacaddr(macaddr)`, 0)
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
			check := fixture.Select(`SELECT f.opfmethod::bigint,p.amproclefttype::bigint,p.amprocrighttype::bigint,p.amprocnum,p.amproc::oid::bigint FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='family_review_lookup' AND f.opfnamespace='public'::regnamespace`, []string{"opfmethod", "amproclefttype", "amprocrighttype", "amprocnum", "amproc"}, []string{"INT8", "INT8", "INT8", "INT2", "INT8"}, [][]any{{int64(405), int64(829), int64(829), int64(1), int64(399)}})
			run(check)
			bad := fixture.ImplicitError(`ALTER OPERATOR FAMILY public.family_review_lookup USING hash ADD FUNCTION 1 pg_catalog.int8inc(bigint)`, fixture.Diagnostic{Code: "42P17", Message: "hash function 1 must return integer"})
			if prepared {
				bad = fixture.Prepared(bad)
			}
			run(bad)
			run(check)
			if !prepared {
				run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_review_lookup USING hash DROP FUNCTION 1(macaddr)`, 0))
			}
		}
		check := func() {
			run(fixture.Select(`SELECT p.amproclefttype::bigint,p.amprocrighttype::bigint,p.amprocnum,p.amproc::oid::bigint FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='family_review_lookup' AND f.opfnamespace='public'::regnamespace`, []string{"amproclefttype", "amprocrighttype", "amprocnum", "amproc"}, []string{"INT8", "INT8", "INT2", "INT8"}, [][]any{{int64(829), int64(829), int64(1), int64(399)}}))
		}
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			if !strings.Contains(emitted, `"pg_catalog"."hashmacaddr"`) {
				t.Fatalf("lookup-only routine missing from emitted SQL:\n%s", emitted)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
		} else if emitted != "" {
			run(fixture.Exec(`DROP OPERATOR FAMILY public.family_review_lookup USING hash`, 0))
			if _, err := conn.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal("PostgreSQL replay of actual native bytes", err)
			}
		}
		check()
	})
}
