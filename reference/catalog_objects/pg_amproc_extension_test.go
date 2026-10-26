package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAmprocExtension(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "amproc_cube")
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(q string) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0)) }
		run("CREATE SCHEMA amproc_cube")
		run("CREATE EXTENSION cube SCHEMA amproc_cube")
		const q = "SELECT a.oid,a.amprocfamily,a.amproclefttype,a.amprocrighttype,a.amprocnum,a.amproc FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily JOIN pg_namespace n ON n.oid=f.opfnamespace WHERE n.nspname='amproc_cube' AND f.opfname='cube_ops' AND f.opfmethod=403 AND a.amprocnum=1"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		var family, typ, proc int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT c.opcfamily,c.opcintype,p.oid FROM pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace JOIN pg_proc p ON p.pronamespace=n.oid WHERE n.nspname='amproc_cube' AND c.opcname='cube_ops' AND c.opcmethod=403 AND p.proname='cube_cmp'").Scan(&family, &typ, &proc); err != nil {
			t.Fatal(err)
		}
		for _, id := range []int64{family, typ, proc} {
			if id <= 0 || id > 4294967295 {
				t.Fatal("dynamic extension identity", id)
			}
		}
		rows, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		pgAmprocMetadata(t, rows, b.PG)
		got := pgAmprocConsume(t, rows)
		rows.Close()
		if len(got) != 1 {
			t.Fatal("installed comparator membership", got)
		}
		oid := got[0][0].(int64)
		if oid <= 0 || oid > 4294967295 {
			t.Fatal("member identity", oid)
		}
		// REGPROC output is schema-qualified when the extension is outside search_path.
		want := [][]any{{oid, family, typ, typ, int64(1), "amproc_cube.cube_cmp"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("installed comparator semantics", got, want)
		}
		for _, prepared := range []bool{false, true} {
			pgAmprocRows(t, p, b, b.DB, q, want, prepared)
		}
		pgAmprocRows(t, p, b, held, q, want, false)
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT a.amproc::oid,p.oid,t.oid,c.opcfamily FROM pg_amproc a JOIN pg_proc p ON p.oid=a.amproc::oid JOIN pg_type t ON t.oid=a.amproclefttype JOIN pg_opclass c ON c.opcfamily=a.amprocfamily WHERE a.oid=$1::oid", []string{"amproc", "oid", "oid", "opcfamily"}, []string{"OID", "OID", "OID", "OID"}, [][]any{{proc, proc, typ, family}}, oid))
		run("DROP EXTENSION cube")
		pgAmprocRows(t, p, b, held, q, nil, false)
		pgAmprocRows(t, p, b, b.DB, pgAmprocProbe, nil, false, oid)
		run("DROP SCHEMA amproc_cube")
	})
}
