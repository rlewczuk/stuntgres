package reference_test

import (
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// alterAddReferenceScenario shares the public scenario in quick and oracle modes.
func alterAddReferenceScenario(t *testing.T, scenario func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		scenario(p, backend)
	}
}

func TestReferenceAlterAddColumnCore(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1),(2),(2)", 3))
				run(fixture.Exec("ALTER TABLE t ADD note TEXT NULL", 0))
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), nil}, {int64(2), nil}}))
				for _, queryPrepared := range []bool{false, true} {
					fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, queryPrepared, "SELECT * FROM t", []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{true, true}, 3)
				}
				for _, target := range []string{"t", "ONLY t", "ONLY(t)", "public.t*"} {
					run(fixture.Exec("DROP TABLE t", 0))
					run(fixture.Exec("CREATE TABLE t(id INT)", 0))
					run(fixture.Exec("ALTER TABLE "+target+" ADD COLUMN b BIGINT DEFAULT 8, ADD c BOOL DEFAULT TRUE", 0))
					run(fixture.Exec("INSERT INTO t(id) VALUES(3)", 1))
					run(fixture.Select("SELECT * FROM t", []string{"id", "b", "c"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(3), int64(8), true}}))
				}
				run(fixture.Exec(`CREATE TABLE "名.dot"("旧" INT)`, 0))
				run(fixture.Exec(`INSERT INTO "名.dot" VALUES(4)`, 1))
				run(fixture.Exec(`ALTER TABLE public."名.dot" ADD "新.dot" TEXT DEFAULT 'value'`, 0))
				run(fixture.Select(`SELECT * FROM "名.dot"`, []string{"旧", "新.dot"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "value"}}))
				run(fixture.Exec("CREATE TABLE empty_shape()", 0))
				run(fixture.Exec("INSERT INTO empty_shape DEFAULT VALUES", 1))
				run(fixture.Exec("INSERT INTO empty_shape DEFAULT VALUES", 1))
				run(fixture.Exec("ALTER TABLE empty_shape ADD n INT DEFAULT 7", 0))
				run(fixture.Select("SELECT * FROM empty_shape", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
			})
		})
	}
}

func TestReferenceAlterAddColumnSkip(t *testing.T) {
	alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
		var notices []fixture.Diagnostic
		_, conn, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,b INT DEFAULT 3 CHECK(b>0))", 0))
		run(fixture.Exec("INSERT INTO t(id) VALUES(1)", 1))
		for _, definition := range []string{"b TEXT DEFAULT 'different' CHECK(FALSE) NOT NULL", "b nonexistent_type", "b INT DEFAULT 'oops'", "b INT CHECK(missing>0)", "b INT NULL NOT NULL", "b INT DEFAULT 1 DEFAULT 2"} {
			notices = nil
			run(fixture.Prepared(fixture.Exec("ALTER TABLE t ADD COLUMN IF NOT EXISTS "+definition, 0)))
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: `column "b" of relation "t" already exists, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices=%#v want=%#v", notices, want)
			}
		}
		run(fixture.Exec("INSERT INTO t(id) VALUES(2)", 1))
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(3)}}))
		run(fixture.ImplicitError("ALTER TABLE t ADD b TEXT", fixture.Diagnostic{Code: "42701", Message: `column "b" of relation "t" already exists`}))
		for _, name := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
			run(fixture.ImplicitError("ALTER TABLE t ADD IF NOT EXISTS "+name+" INT", fixture.Diagnostic{Code: "42701", Message: fmt.Sprintf(`column name "%s" conflicts with a system column name`, name)}))
		}
		run(fixture.Exec("ALTER TABLE t ADD oid INT DEFAULT 9", 0))
	})
}

func TestReferenceAlterAddColumnSmoke(t *testing.T) {
	alterAddReferenceScenario(t, func(p *fixture.Pair, backend fixture.Backend) {
		var notices []fixture.Diagnostic
		_, conn, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
		star, err := conn.PrepareContext(p.Ctx, "SELECT * FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer star.Close()
		run(fixture.Exec("ALTER TABLE t ADD note TEXT, ADD n INT DEFAULT 7 NOT NULL", 0))
		run(fixture.Exec("ALTER TABLE t ALTER n SET DEFAULT 9", 0))
		run(fixture.Prepared(fixture.Exec("INSERT INTO t(id) VALUES(3)", 1)))
		want := fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "note", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), nil, int64(7)}, {int64(2), nil, int64(7)}, {int64(3), nil, int64(9)}})
		run(want)
		run(fixture.ImplicitError("ALTER TABLE t ADD bad INT DEFAULT 0 CONSTRAINT positive CHECK(bad>0)", fixture.Diagnostic{Code: "23514", Message: `check constraint "positive" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "positive"}))
		run(want)
		notices = nil
		run(fixture.Exec("ALTER TABLE t ADD IF NOT EXISTS n TEXT DEFAULT 'changed'", 0))
		if !reflect.DeepEqual(notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: `column "n" of relation "t" already exists, skipping`}}) {
			t.Fatal(notices)
		}
		for _, prepared := range []bool{false, true} {
			fixture.TableDDLMetadata(t, p.Ctx, conn, backend.PG, prepared, want.Query, want.Columns, want.Types, []bool{true, true, false}, 3)
		}
		failure := fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "0A000", Message: "cached plan must not change result type"})
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{conn, star, "SELECT * FROM t"}, record, failure)
	})
}
