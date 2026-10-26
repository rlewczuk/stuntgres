package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func amDump(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal("Dump", err)
	}
	return dump.String()
}

func amControlAtomicity(t *testing.T, p *fixture.Pair, emitted string, check func()) {
	t.Helper()
	before := amDump(t, p)
	err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+`CREATE ACCESS METHOD am_control_bad TYPE TABLE HANDLER pg_catalog.bthandler;`))
	want := fixture.ControlsReferenceDiagnostic("42809", `function bthandler must return type table_am_handler`)
	fixture.AssertDiagnostic(t, err, &want)
	if after := amDump(t, p); after != before {
		t.Fatalf("late failed Restore changed incumbent\nbefore %s\nafter %s", before, after)
	}
	check()
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Restore: %v", err)
	}
	if after := amDump(t, p); after != before {
		t.Fatalf("canceled Restore changed incumbent\nbefore %s\nafter %s", before, after)
	}
	check()
}

func TestReferenceAccessMethodControls(t *testing.T) {
	var emitted string
	amBackends(t, []string{"AM Control", "am_control_heap", "am_control_bad"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		for _, q := range []string{
			`CREATE ACCESS METHOD "AM Control" TYPE INDEX HANDLER pg_catalog.bthandler`,
			`CREATE ACCESS METHOD am_control_heap TYPE TABLE HANDLER pg_catalog.heap_tableam_handler`,
			`CREATE OPERATOR FAMILY public.am_control_family USING "AM Control"`,
			amCreateClass(`"AM Control"`, "am_control_family", "am_control_ops"),
			`CREATE TABLE am_control_rows(n integer)`,
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(`INSERT INTO am_control_rows VALUES(2),(1),(NULL)`, 3))
		run(fixture.Exec(`CREATE INDEX am_control_i ON am_control_rows USING "AM Control"(n)`, 0))
		const data = `SELECT n FROM am_control_rows ORDER BY n NULLS LAST`
		const method = `SELECT amname,amhandler::oid::bigint,amtype FROM pg_am WHERE amname IN ('AM Control','am_control_heap') ORDER BY amname`
		dh := classHeld(t, p, conn, data)
		mh := classHeld(t, p, conn, method)
		check := func() {
			amIndexIdentity(t, p.Ctx, conn, run, "AM Control", "am_control_family", "am_control_ops", "am_control_i")
			s := fixture.Select(data, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}})
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, dh, b.Recorder, s)
			s = fixture.Select(method, []string{"amname", "amhandler", "amtype"}, []string{"NAME", "INT8", "CHAR"}, [][]any{{"AM Control", int64(330), "i"}, {"am_control_heap", int64(3), "t"}})
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, mh, b.Recorder, s)
			run(amProperties(amPropertySQL, [][]any{{true, true, true, true, true, nil}}, "AM Control"))
			run(amCount(`SELECT count(*) FROM pg_am WHERE amname='am_control_bad'`, 0))
		}
		check()
		if !b.PG {
			emitted = amDump(t, p)
			indexDDL := `CREATE ACCESS METHOD "AM Control" TYPE INDEX HANDLER "pg_catalog"."bthandler";`
			tableDDL := `CREATE ACCESS METHOD "am_control_heap" TYPE TABLE HANDLER "pg_catalog"."heap_tableam_handler";`
			for _, q := range []string{indexDDL, tableDDL} {
				if strings.Count(emitted, q) != 1 {
					t.Fatalf("expected exactly one %s\n%s", q, emitted)
				}
			}
			if strings.Count(emitted, "CREATE ACCESS METHOD ") != 2 || strings.Contains(emitted, "ALTER ACCESS METHOD") {
				t.Fatalf("builtin or owner method declarations in dump\n%s", emitted)
			}
			familyDDL := `CREATE OPERATOR FAMILY "public"."am_control_family" USING "AM Control";`
			classDDL := `CREATE OPERATOR CLASS "public"."am_control_ops"`
			indexDef := `CREATE INDEX "am_control_i"`
			last := -1
			for _, q := range []string{indexDDL, familyDDL, classDDL, indexDef} {
				pos := strings.Index(emitted, q)
				if pos < 0 || pos <= last {
					t.Fatalf("missing/out-of-order %q\n%s", q, emitted)
				}
				last = pos
			}
			if strings.Count(emitted, `USING "AM Control"`) < 3 {
				t.Fatalf("method not quoted in family/class/index\n%s", emitted)
			}
			// Eager rows keep their captured method values/metadata across replacement.
			rows, err := p.Native.QueryContext(p.Ctx, method)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 3 || types[0].DatabaseTypeName() != "NAME" || types[1].DatabaseTypeName() != "INT8" || types[2].DatabaseTypeName() != "CHAR" {
				t.Fatal("captured metadata", types, err)
			}
			amControlAtomicity(t, p, emitted, check)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal("same-engine Restore", err)
			}
			check()

			// Destination has different allocation history and retained TEMP objects.
			// SQL checks rediscover all restored identities; package units prove the
			// actual collision remint and immutable support/root captures.
			foreign := fixture.NewFixture(t, false, true)
			fc, err := foreign.Memory.Conn(foreign.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fc.Close()
			fr := func(s fixture.Step) { fixture.RunStep(t, foreign.Ctx, fc, foreign.MemoryRecord, s) }
			fr(fixture.Exec(`CREATE TEMP TABLE am_control_kept(n integer)`, 0))
			fr(fixture.Exec(`INSERT INTO am_control_kept VALUES(91)`, 1))
			fr(fixture.Exec(`CREATE INDEX am_control_kept_i ON am_control_kept(n)`, 0))
			fr(fixture.Exec(`CREATE ACCESS METHOD am_control_occupied TYPE INDEX HANDLER bthandler`, 0))
			fr(fixture.Exec(`CREATE OPERATOR FAMILY public.am_control_occupied_f USING am_control_occupied`, 0))
			fr(fixture.Exec(`CREATE TABLE am_control_old(n integer)`, 0))
			fh := classHeld(t, foreign, fc, `SELECT n FROM am_control_kept`)
			if err := stuntgres.Restore(foreign.Ctx, foreign.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal("foreign Restore", err)
			}
			foreignCheck := func() {
				amIndexIdentity(t, foreign.Ctx, fc, fr, "AM Control", "am_control_family", "am_control_ops", "am_control_i")
				fr(fixture.Select(data, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}))
				fixture.RunStep(t, foreign.Ctx, fh, foreign.MemoryRecord, fixture.Select(fh.SQL, []string{"n"}, []string{"INT4"}, [][]any{{int64(91)}}))
				fr(amCount(`SELECT count(*) FROM pg_am WHERE amname='am_control_occupied'`, 0))
				fr(amCount(`SELECT count(*) FROM pg_class WHERE relname='am_control_old'`, 0))
			}
			foreignCheck()
			amControlAtomicity(t, foreign, emitted, foreignCheck)
			fr(fixture.Exec(`INSERT INTO am_control_rows VALUES(0)`, 1))
			fr(fixture.Select(data, []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}, {nil}}))
			fr(fixture.Exec(`INSERT INTO am_control_kept VALUES(92)`, 1))
			fr(amCount(`SELECT count(*) FROM am_control_kept`, 2))
			check() // destination inserts must not mutate source rows/catalog/roots
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal("Reset", err)
			}
			run(amCount(`SELECT count(*) FROM pg_am WHERE amname IN ('AM Control','am_control_heap')`, 0))
			run(amCount(`SELECT count(*) FROM pg_class WHERE relname IN ('am_control_i','am_control_rows')`, 0))
			run(amCount(`SELECT count(*) FROM pg_am`, 5))
			run(fixture.Select(`SELECT amname,amhandler::oid::bigint,amtype FROM pg_am WHERE amname IN ('btree','heap') ORDER BY amname`, []string{"amname", "amhandler", "amtype"}, []string{"NAME", "INT8", "CHAR"}, [][]any{{"btree", int64(330), "i"}, {"heap", int64(3), "t"}}))
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal("Restore after Reset", err)
			}
			for _, want := range []struct {
				name    string
				handler int64
				kind    string
			}{{"AM Control", 330, "i"}, {"am_control_heap", 3, "t"}} {
				if !rows.Next() {
					t.Fatal("captured row missing", rows.Err())
				}
				var name, kind string
				var handler int64
				if err := rows.Scan(&name, &handler, &kind); err != nil || name != want.name || handler != want.handler || kind != want.kind {
					t.Fatal("captured row changed", name, handler, kind, err)
				}
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("captured rows", rows.Err())
			}
			rows.Close()
			if types[0].DatabaseTypeName() != "NAME" {
				t.Fatal("retained metadata changed")
			}
		} else if emitted != "" {
			// Final both-mode proof consumes the actual native stream, not a
			// reconstructed approximation. Discovery has no native bytes yet.
			run(fixture.Exec(`DROP ACCESS METHOD "AM Control" CASCADE`, 0))
			run(fixture.Exec(`DROP ACCESS METHOD am_control_heap`, 0))
			run(fixture.Exec(`DROP TABLE am_control_rows`, 0))
			if _, err := conn.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal("PostgreSQL rejected actual native access-method dump", err)
			}
		}
		check()
		run(fixture.Exec(`INSERT INTO am_control_rows VALUES(0)`, 1))
		s := fixture.Select(data, []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}, {nil}})
		classRoutes(t, p, b, conn, s)
		fixture.RunStep(t, p.Ctx, dh, b.Recorder, s)
		amIndexIdentity(t, p.Ctx, conn, run, "AM Control", "am_control_family", "am_control_ops", "am_control_i")
	})
}
