package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

// Preserve the original non-Context database/sql consumer methods alongside
// the recorded Context witnesses in IdentifierKeywords/PublicContractRoutes.
func TestReferenceIdentifierKeywordSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			if err := db.Ping(); err != nil {
				t.Fatal(err)
			}
			exec := func(q string, count int64, args ...any) {
				t.Helper()
				r, err := db.Exec(q, args...)
				deleteReferenceCount(t, r, err, count)
			}
			exec("CREATE TABLE abort (by INT, between TEXT, values BOOL, integer BIGINT, char TEXT)", 0)
			exec("INSERT INTO public.abort (by,between,values,integer,char) VALUES (1,'one',TRUE,10,'a'),(2,'two',FALSE,20,'b')", 2)
			columns := []string{"by", "between", "values", "integer", "char"}
			types := []string{"INT4", "TEXT", "BOOL", "INT8", "TEXT"}
			want := [][]any{{int64(2), "two", false, int64(20), "b"}}
			const q = "SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC"
			rows, err := db.Query(q)
			deleteReferenceConsume(t, backend.pg, rows, err, referenceSelect(q, columns, types, want))
			held, err := db.Prepare("INSERT INTO abort (by,between,values,integer,char) VALUES ($1,$2,$3,$4,$5)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			r, err := held.Exec(3, "three", true, 30, "c")
			deleteReferenceCount(t, r, err, 1)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			held, err = db.Prepare("SELECT by,between,values,integer,char FROM abort WHERE by=$1 ORDER BY by DESC")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			rows, err = held.Query(2)
			deleteReferenceConsume(t, backend.pg, rows, err, referenceSelect(q, columns, types, want))
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			exec(q, 1)
			held, err = db.Prepare("CREATE TABLE insert (by INT)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			r, err = held.Exec()
			deleteReferenceCount(t, r, err, 0)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			exec("INSERT INTO insert (by) VALUES (7)", 1)
			exec(`CREATE TABLE "select" ("join" INT, "authorization" TEXT, "a""b" BOOL)`, 0)
			exec(`INSERT INTO "select" VALUES (7,'kept',TRUE)`, 1)
			for _, s := range []referenceStep{
				referenceSelect("SELECT by FROM insert ORDER BY by", []string{"by"}, []string{"INT4"}, [][]any{{int64(7)}}),
				referenceSelect(`SELECT "join","authorization","a""b" FROM "select" ORDER BY "join"`, []string{"join", "authorization", "a\"b"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "kept", true}}),
			} {
				rows, err := db.Query(s.query)
				deleteReferenceConsume(t, backend.pg, rows, err, s)
			}
			// Host replacement has no PostgreSQL API analogue. Retain the actual
			// native Query cursor across failed Restore and Reset/Restore.
			if !backend.pg {
				var dump, after, redump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				old, err := db.Query(q)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				err = stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString("CREATE TABLE staged (x INT); CREATE TABLE select (x INT)"))
				wantError := controlsReferenceDiagnostic("42601", `syntax error at or near "select"`)
				wantError.Position = "43"
				referenceAssertDiagnostic(t, err, &wantError)
				if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump.Bytes(), after.Bytes()) {
					t.Fatal("failed restore changed catalog")
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				deleteReferenceConsume(t, false, old, nil, referenceSelect(q, columns, types, want))
				rows, err := db.Query(q)
				deleteReferenceConsume(t, false, rows, err, referenceSelect(q, columns, types, want))
				if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
					t.Fatal("redump differs")
				}
			}
		})
	}
}
