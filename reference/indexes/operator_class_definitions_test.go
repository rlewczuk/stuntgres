package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceOperatorClassDefinitions(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "class_def_a", "class_def_b")
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		for _, q := range []string{
			`CREATE SCHEMA class_def_a`, `CREATE SCHEMA class_def_b`,
			`CREATE OPERATOR FAMILY public.class_def_explicit USING btree`,
			classCreate("public.class_def_explicit", "public.class_def_explicit", "btint4cmp"),
			`CREATE OPERATOR CLASS public.class_def_implicit FOR TYPE integer USING btree AS ` + classItems,
			`CREATE OPERATOR FAMILY class_def_a.class_def_reuse USING btree`,
			`CREATE OPERATOR FAMILY class_def_b.class_def_reuse USING btree`,
			`SET search_path=class_def_b,public`,
			`CREATE OPERATOR CLASS class_def_a.class_def_reuse FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`,
			`RESET search_path`,
			`CREATE OPERATOR CLASS class_def_a."Mixed Class" FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer), STORAGE integer`,
			`CREATE OPERATOR CLASS class_def_b."Mixed Class" FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`,
			`CREATE OPERATOR CLASS public.class_def_same FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`,
			`CREATE OPERATOR CLASS public.class_def_same FOR TYPE integer USING hash AS OPERATOR 1 pg_catalog.=, FUNCTION 1 pg_catalog.hashint4(integer), FUNCTION 2 pg_catalog.hashint4extended(integer,bigint)`,
			`CREATE OPERATOR CLASS public.class_def_incomplete FOR TYPE integer USING btree AS OPERATOR 3 pg_catalog.=`,
			`CREATE OPERATOR CLASS public.class_def_assoc FOR TYPE integer USING btree AS FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`,
			`CREATE OPERATOR CLASS public.class_def_gist FOR TYPE inet USING gist AS STORAGE bigint`,
		} {
			run(fixture.Exec(q, 0))
		}
		const q = `SELECT c.opcname,n.nspname,c.opcmethod::bigint,c.opcintype::bigint,c.opcdefault,c.opckeytype::bigint,f.opfname,fn.nspname,c.opcmethod=f.opfmethod,c.opcowner=f.opfowner FROM pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace JOIN pg_opfamily f ON f.oid=c.opcfamily JOIN pg_namespace fn ON fn.oid=f.opfnamespace WHERE c.opcname LIKE 'class_def_%' OR c.opcname='Mixed Class' ORDER BY c.opcname,n.nspname,c.opcmethod`
		want := [][]any{
			{"Mixed Class", "class_def_a", int64(403), int64(23), false, int64(0), "Mixed Class", "class_def_a", true, true},
			{"Mixed Class", "class_def_b", int64(403), int64(23), false, int64(0), "Mixed Class", "class_def_b", true, true},
			{"class_def_assoc", "public", int64(403), int64(23), false, int64(0), "class_def_assoc", "public", true, true},
			{"class_def_explicit", "public", int64(403), int64(23), false, int64(0), "class_def_explicit", "public", true, true},
			{"class_def_gist", "public", int64(783), int64(869), false, int64(20), "class_def_gist", "public", true, true},
			{"class_def_implicit", "public", int64(403), int64(23), false, int64(0), "class_def_implicit", "public", true, true},
			{"class_def_incomplete", "public", int64(403), int64(23), false, int64(0), "class_def_incomplete", "public", true, true},
			{"class_def_reuse", "class_def_a", int64(403), int64(23), false, int64(0), "class_def_reuse", "class_def_a", true, true},
			{"class_def_same", "public", int64(403), int64(23), false, int64(0), "class_def_same", "public", true, true},
			{"class_def_same", "public", int64(405), int64(23), false, int64(0), "class_def_same", "public", true, true},
		}
		s := fixture.Select(q, []string{"opcname", "nspname", "opcmethod", "opcintype", "opcdefault", "opckeytype", "opfname", "nspname", "?column?", "?column?"}, []string{"NAME", "NAME", "INT8", "INT8", "BOOL", "INT8", "NAME", "NAME", "BOOL", "BOOL"}, want)
		classRoutes(t, p, b, conn, s)
		fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, q), b.Recorder, s)
		// All nine released fields: generated IDs are asserted relationally, not golden OIDs.
		const nine = `SELECT c.* FROM pg_opclass c WHERE c.opcname='class_def_explicit'`
		var oid, ns, owner, family int64
		if err := conn.QueryRowContext(p.Ctx, `SELECT oid::bigint,opcnamespace::bigint,opcowner::bigint,opcfamily::bigint FROM pg_opclass WHERE opcname='class_def_explicit'`).Scan(&oid, &ns, &owner, &family); err != nil {
			t.Fatal(err)
		}
		var publicNS, currentOwner, explicitFamily int64
		if err := conn.QueryRowContext(p.Ctx, `SELECT n.oid::bigint,r.oid::bigint,f.oid::bigint FROM pg_namespace n JOIN pg_roles r ON r.rolname=current_user JOIN pg_opfamily f ON f.opfnamespace=n.oid WHERE n.nspname='public' AND f.opfname='class_def_explicit' AND f.opfmethod=403`).Scan(&publicNS, &currentOwner, &explicitFamily); err != nil {
			t.Fatal(err)
		}
		if oid == 0 || ns != publicNS || owner != currentOwner || family != explicitFamily {
			t.Fatal(oid, ns, owner, family, publicNS, currentOwner, explicitFamily)
		}
		// pq transports OID cells as text; native exposes its documented integer cells.
		nineRow := []any{oid, int64(403), "class_def_explicit", ns, owner, family, int64(23), false, int64(0)}
		if b.PG {
			nineRow = []any{strconv.FormatInt(oid, 10), "403", "class_def_explicit", strconv.FormatInt(ns, 10), strconv.FormatInt(owner, 10), strconv.FormatInt(family, 10), "23", false, "0"}
		}
		classRoutes(t, p, b, conn, fixture.Select(nine, []string{"oid", "opcmethod", "opcname", "opcnamespace", "opcowner", "opcfamily", "opcintype", "opcdefault", "opckeytype"}, []string{"OID", "OID", "NAME", "OID", "OID", "OID", "OID", "BOOL", "OID"}, [][]any{nineRow}))
		run(fixture.Select(`SELECT p.amproclefttype::bigint,p.amprocrighttype::bigint,p.amprocnum,p.amproc::text FROM pg_amproc p JOIN pg_opclass c ON c.opcfamily=p.amprocfamily WHERE c.opcname='class_def_assoc'`, []string{"amproclefttype", "amprocrighttype", "amprocnum", "amproc"}, []string{"INT8", "INT8", "INT2", "TEXT"}, [][]any{{int64(23), int64(23), int64(2), "btint4sortsupport"}}))
		t.Run("enum_default", func(t *testing.T) { classDefaultDefinition(t, p, b, conn) })
	})
}
