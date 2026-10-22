package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAccessMethodDependencies(t *testing.T) {
	amBackends(t, []string{"am_dep_empty", "am_dep_family", "am_dep_class", "am_dep", "am_dep_other"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, conn, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = rec
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, rec, s) }
		noticeExec := func(q string, prepared bool, want ...fixture.Diagnostic) {
			fixture.RoleGrantNoticeExec(t, p, conn, rec, prepared, &notices, q, want...)
		}
		for i, tc := range []struct{ method, family, class string }{
			{"am_dep_empty", "", ""},
			{"am_dep_family", "am_dep_empty_family", ""},
			{"am_dep_class", "am_dep_empty_class_family", "am_dep_empty_ops"},
		} {
			run(fixture.Exec(`CREATE ACCESS METHOD `+tc.method+` TYPE INDEX HANDLER bthandler`, 0))
			if tc.family != "" {
				run(fixture.Exec(`CREATE OPERATOR FAMILY public.`+tc.family+` USING `+tc.method, 0))
			}
			if tc.class != "" {
				run(fixture.Exec(amCreateClass(tc.method, tc.family, tc.class), 0))
			}
			noticeExec(`DROP ACCESS METHOD `+tc.method+` RESTRICT`, i == 1)
			run(amCount(`SELECT count(*) FROM pg_am WHERE amname=$1`, 0, tc.method))
			if tc.family != "" {
				run(amCount(`SELECT count(*) FROM pg_opfamily WHERE opfname=$1`, 0, tc.family))
			}
			if tc.class != "" {
				run(amCount(`SELECT count(*) FROM pg_opclass WHERE opcname=$1`, 0, tc.class))
			}
		}
		for _, q := range []string{
			`CREATE ACCESS METHOD am_dep TYPE INDEX HANDLER bthandler`,
			`CREATE ACCESS METHOD am_dep_other TYPE TABLE HANDLER heap_tableam_handler`,
			`CREATE OPERATOR FAMILY public.am_dep_family USING am_dep`,
			amCreateClass("am_dep", "am_dep_family", "am_dep_ops"),
			`CREATE TABLE am_dep_rows(n integer)`,
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(`INSERT INTO am_dep_rows VALUES(2),(1),(NULL)`, 3))
		run(fixture.Exec(`CREATE INDEX am_dep_i ON am_dep_rows USING am_dep(n)`, 0))
		am := classRoundtripIdentity(t, p.Ctx, conn, `SELECT oid::bigint FROM pg_am WHERE amname='am_dep'`)
		family := classRoundtripIdentity(t, p.Ctx, conn, `SELECT oid::bigint FROM pg_opfamily WHERE opfname='am_dep_family' AND opfmethod=$1::oid`, am)
		class := classRoundtripIdentity(t, p.Ctx, conn, `SELECT oid::bigint FROM pg_opclass WHERE opcname='am_dep_ops' AND opcmethod=$1::oid`, am)
		index := classRoundtripIdentity(t, p.Ctx, conn, `SELECT oid::bigint FROM pg_class WHERE relname='am_dep_i'`)
		graph := []fixture.Step{
			fixture.Select(`SELECT deptype FROM pg_depend WHERE classid='pg_opfamily'::regclass AND objid=$1::oid AND refclassid='pg_am'::regclass AND refobjid=$2::oid`, []string{"deptype"}, []string{"CHAR"}, [][]any{{"a"}}, family, am),
			fixture.Select(`SELECT deptype FROM pg_depend WHERE classid='pg_opclass'::regclass AND objid=$1::oid AND refclassid='pg_opfamily'::regclass AND refobjid=$2::oid`, []string{"deptype"}, []string{"CHAR"}, [][]any{{"a"}}, class, family),
			fixture.Select(`SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass AND objid=$1::oid AND refclassid='pg_opclass'::regclass AND refobjid=$2::oid`, []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}}, index, class),
			amCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_am'::regclass AND objid=$1::oid AND refclassid='pg_proc'::regclass`, 0, am),
			amCount(`SELECT count(*) FROM pg_depend WHERE refclassid='pg_am'::regclass AND refobjid=$1::oid AND classid IN ('pg_opclass'::regclass,'pg_class'::regclass)`, 0, am),
		}
		const data = `SELECT n FROM am_dep_rows ORDER BY n NULLS LAST`
		held := classHeld(t, p, conn, data)
		check := func() {
			for _, s := range graph {
				classRoutes(t, p, b, conn, s)
			}
			amIndexIdentity(t, p.Ctx, conn, run, "am_dep", "am_dep_family", "am_dep_ops", "am_dep_i")
			fixture.RunStep(t, p.Ctx, held, rec, fixture.Select(data, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}))
		}
		check()
		var before bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
		}
		for _, suffix := range []string{"", " RESTRICT"} {
			amError(t, p, b, conn, `DROP ACCESS METHOD am_dep`+suffix, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop access method am_dep because other objects depend on it`, Detail: `index am_dep_i depends on operator class am_dep_ops for access method am_dep`, Hint: `Use DROP ... CASCADE to drop the dependent objects too.`})
			check()
			if !b.PG {
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
					t.Fatalf("RESTRICT mutated dump: %v\nbefore %s\nafter %s", err, before.String(), after.String())
				}
			}
		}
		noticeExec(`DROP ACCESS METHOD am_dep CASCADE`, true, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `drop cascades to index am_dep_i`})
		for _, s := range []fixture.Step{
			amCount(`SELECT count(*) FROM pg_am WHERE amname='am_dep'`, 0),
			amCount(`SELECT count(*) FROM pg_opfamily WHERE oid=$1::oid`, 0, family),
			amCount(`SELECT count(*) FROM pg_opclass WHERE oid=$1::oid`, 0, class),
			amCount(`SELECT count(*) FROM pg_class WHERE oid=$1::oid`, 0, index),
			amCount(`SELECT count(*) FROM pg_amop WHERE amopfamily=$1::oid`, 0, family),
			amCount(`SELECT count(*) FROM pg_amproc WHERE amprocfamily=$1::oid`, 0, family),
			amCount(`SELECT count(*) FROM pg_am WHERE amname='am_dep_other'`, 1),
		} {
			run(s)
		}
		fixture.RunStep(t, p.Ctx, held, rec, fixture.Select(data, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}))
		run(fixture.Exec(`INSERT INTO am_dep_rows VALUES(3)`, 1))
		run(amCount(`SELECT count(*) FROM am_dep_rows`, 4))
		noticeExec(`DROP ACCESS METHOD IF EXISTS am_dep`, false, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `access method "am_dep" does not exist, skipping`})
	})
}
