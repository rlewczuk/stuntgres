package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func xml2RenamedReplay(t *testing.T, p *fixture.Pair, source fixture.Backend, c *sql.Conn, destinationControl *sql.DB, destination fixture.Backend, dst *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"ALTER FUNCTION xpath_string(text,text) RENAME TO xml2_text",
		"ALTER EXTENSION xml2 DROP FUNCTION xml2_text(text,text)",
	} {
		fixture.RunStep(t, p.Ctx, c, source.Recorder, fixture.Exec(q, 0))
	}
	check := func(b fixture.Backend, x *sql.Conn) {
		t.Helper()
		xml2Both(t, p, b, x, fixture.Select("SELECT xml2_text('<r>12</r>','.') AS s", []string{"s"}, []string{"TEXT"}, [][]any{{"12"}}))
		xml2Both(t, p, b, x, fixture.Select("SELECT p.prosrc,p.probin FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.proname='xml2_text'", []string{"prosrc", "probin"}, []string{"TEXT", "TEXT"}, [][]any{{"xpath_string", "$libdir/pgxml"}}))
		xml2Both(t, p, b, x, fixture.Select("SELECT p.proname FROM pg_proc p JOIN pg_depend d ON d.classid=1255 AND d.objid=p.oid AND d.refclassid=3079 AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='xml2' AND p.proname='xml2_text'", []string{"proname"}, []string{"NAME"}, nil))
		xml2Saved(t, p, b, x)
		xml2Guard(t, p, b, x, "public", "xml2_text")
		xml2ACLCheck(t, p, b, x)
	}
	check(source, c)
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	check(source, c)
	if err := stuntgres.Restore(p.Ctx, destinationControl, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	check(destination, dst)
}
