package reference_test

import (
	"fmt"
	"reflect"
	"testing"
)

// alterAddReferenceScenario shares the public scenario in quick and oracle modes.
func alterAddReferenceScenario(t *testing.T, scenario func(*referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		scenario(p, backend)
	}
}

func TestReferenceAlterAddColumnCore(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
				run := func(s referenceStep) {
					s.prepared = prepared
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1),(2),(2)", 3))
				run(referenceExec("ALTER TABLE t ADD note TEXT NULL", 0))
				run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), nil}, {int64(2), nil}}))
				for _, queryPrepared := range []bool{false, true} {
					referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, queryPrepared, "SELECT * FROM t", []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{true, true}, 3)
				}
				for _, target := range []string{"t", "ONLY t", "ONLY(t)", "public.t*"} {
					run(referenceExec("DROP TABLE t", 0))
					run(referenceExec("CREATE TABLE t(id INT)", 0))
					run(referenceExec("ALTER TABLE "+target+" ADD COLUMN b BIGINT DEFAULT 8, ADD c BOOL DEFAULT TRUE", 0))
					run(referenceExec("INSERT INTO t(id) VALUES(3)", 1))
					run(referenceSelect("SELECT * FROM t", []string{"id", "b", "c"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(3), int64(8), true}}))
				}
				run(referenceExec(`CREATE TABLE "名.dot"("旧" INT)`, 0))
				run(referenceExec(`INSERT INTO "名.dot" VALUES(4)`, 1))
				run(referenceExec(`ALTER TABLE public."名.dot" ADD "新.dot" TEXT DEFAULT 'value'`, 0))
				run(referenceSelect(`SELECT * FROM "名.dot"`, []string{"旧", "新.dot"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "value"}}))
				run(referenceExec("CREATE TABLE empty_shape()", 0))
				run(referenceExec("INSERT INTO empty_shape DEFAULT VALUES", 1))
				run(referenceExec("INSERT INTO empty_shape DEFAULT VALUES", 1))
				run(referenceExec("ALTER TABLE empty_shape ADD n INT DEFAULT 7", 0))
				run(referenceSelect("SELECT * FROM empty_shape", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}))
			})
		})
	}
}

func TestReferenceAlterAddColumnSkip(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		var notices []referenceDiagnostic
		_, conn, record := noticeReferencePool(t, p, backend.pg, &notices)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, record, s) }
		run(referenceExec("CREATE TABLE t(id INT,b INT DEFAULT 3 CHECK(b>0))", 0))
		run(referenceExec("INSERT INTO t(id) VALUES(1)", 1))
		for _, definition := range []string{"b TEXT DEFAULT 'different' CHECK(FALSE) NOT NULL", "b nonexistent_type", "b INT DEFAULT 'oops'", "b INT CHECK(missing>0)", "b INT NULL NOT NULL", "b INT DEFAULT 1 DEFAULT 2"} {
			notices = nil
			run(referencePrepared(referenceExec("ALTER TABLE t ADD COLUMN IF NOT EXISTS "+definition, 0)))
			want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: `column "b" of relation "t" already exists, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices=%#v want=%#v", notices, want)
			}
		}
		run(referenceExec("INSERT INTO t(id) VALUES(2)", 1))
		run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}, {int64(2), int64(3)}}))
		run(referenceImplicitError("ALTER TABLE t ADD b TEXT", referenceDiagnostic{Code: "42701", Message: `column "b" of relation "t" already exists`}))
		for _, name := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
			run(referenceImplicitError("ALTER TABLE t ADD IF NOT EXISTS "+name+" INT", referenceDiagnostic{Code: "42701", Message: fmt.Sprintf(`column name "%s" conflicts with a system column name`, name)}))
		}
		run(referenceExec("ALTER TABLE t ADD oid INT DEFAULT 9", 0))
	})
}

func TestReferenceAlterAddColumnSmoke(t *testing.T) {
	alterAddReferenceScenario(t, func(p *referencePair, backend referenceBackend) {
		var notices []referenceDiagnostic
		_, conn, record := noticeReferencePool(t, p, backend.pg, &notices)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, record, s) }
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
		star, err := conn.PrepareContext(p.ctx, "SELECT * FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer star.Close()
		run(referenceExec("ALTER TABLE t ADD note TEXT, ADD n INT DEFAULT 7 NOT NULL", 0))
		run(referenceExec("ALTER TABLE t ALTER n SET DEFAULT 9", 0))
		run(referencePrepared(referenceExec("INSERT INTO t(id) VALUES(3)", 1)))
		want := referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "note", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), nil, int64(7)}, {int64(2), nil, int64(7)}, {int64(3), nil, int64(9)}})
		run(want)
		run(referenceImplicitError("ALTER TABLE t ADD bad INT DEFAULT 0 CONSTRAINT positive CHECK(bad>0)", referenceDiagnostic{Code: "23514", Message: `check constraint "positive" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "positive"}))
		run(want)
		notices = nil
		run(referenceExec("ALTER TABLE t ADD IF NOT EXISTS n TEXT DEFAULT 'changed'", 0))
		if !reflect.DeepEqual(notices, []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: `column "n" of relation "t" already exists, skipping`}}) {
			t.Fatal(notices)
		}
		for _, prepared := range []bool{false, true} {
			referenceTableDDLMetadata(t, p.ctx, conn, backend.pg, prepared, want.query, want.columns, want.types, []bool{true, true, false}, 3)
		}
		failure := referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "0A000", Message: "cached plan must not change result type"})
		failure.selectRows = true
		failure.errorStage = "execute"
		referenceRunStep(t, p.ctx, referenceHeldStatement{conn, star, "SELECT * FROM t"}, record, failure)
	})
}
