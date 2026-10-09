package reference_test

import (
	"bytes"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceStringCoreLifecycleSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	nativeDSN := "reference/" + t.Name()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := p.ctx
			db := backend.db
			stringCoreControlExec(t, db, `CREATE TABLE string_core_source(id INT,raw CHAR(8),form TEXT)`)
			stringCoreControlExec(t, db, `INSERT INTO string_core_source VALUES(1,' é ','NFC'),(2,' x ','NFC'),(3,NULL,'NFC')`)
			stringCoreControlExec(t, db, `CREATE TABLE string_core_dest(id INT,v TEXT DEFAULT normalize(trim(' é ')),CHECK (v IS NORMALIZED))`)
			stringCoreControlExec(t, db, `INSERT INTO string_core_dest(id) VALUES(0)`)
			result, err := db.Exec(`INSERT INTO string_core_dest SELECT id,"normalize"(btrim(raw),form)||'!' FROM string_core_source`)
			if err != nil {
				t.Fatal(err)
			}
			n, err := result.RowsAffected()
			if err != nil || n != 3 {
				t.Fatal(n, err)
			}
			stringCoreControlTuples(t, db, "SELECT id,v FROM string_core_dest ORDER BY id", [][]any{{int64(0), "é"}, {int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}})
			stringCoreControlExec(t, db, `CREATE VIEW string_core_view AS SELECT id,normalize(btrim(raw))||'!' AS v FROM string_core_source`)
			for _, q := range []string{
				"SELECT id,v FROM string_core_view WHERE v IS NORMALIZED ORDER BY id",
				"SELECT s.id,d.v FROM string_core_source s JOIN string_core_dest d ON normalize(btrim(s.raw))||'!'=d.v ORDER BY s.id",
			} {
				stringCoreControlTuples(t, db, q, [][]any{{int64(1), "é!"}, {int64(2), "x!"}})
			}
			stringCoreControlTuples(t, db, "SELECT btrim(raw) AS v,count(*) AS n FROM string_core_source GROUP BY btrim(raw) HAVING btrim(raw) IS NORMALIZED ORDER BY v", [][]any{{"x", int64(1)}})
			rows, err := db.Query("UPDATE string_core_dest SET v=normalize(v||'a') WHERE id=2 RETURNING id,v")
			stringCoreRetainedRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "x!a"}}, false)
			rows, err = db.Query("DELETE FROM string_core_dest WHERE btrim(v)='x!a' RETURNING id,v")
			stringCoreRetainedRows(t, rows, err, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "x!a"}}, false)
			// Runtime body failure on the second source row is statement-atomic.
			stringCoreControlExec(t, db, "UPDATE string_core_source SET form='bad' WHERE id=2")
			_, err = db.Exec(`INSERT INTO string_core_dest SELECT id+10,"normalize"(raw,form) FROM string_core_source`)
			stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "22023", Message: "invalid normalization form: bad"})
			stringCoreControlTuples(t, db, "SELECT id FROM string_core_dest ORDER BY id", [][]any{{int64(0)}, {int64(1)}, {int64(3)}})
			stringCoreControlExec(t, db, "UPDATE string_core_source SET form='NFC' WHERE id=2")
			// A failed CHECK also leaves publication untouched.
			_, err = db.Exec("INSERT INTO string_core_dest VALUES(20,'é')")
			referenceState(t, err, "23514")
			if !backend.pg {
				raw, err := p.nativeConnector.Driver().Open(nativeDSN)
				if err != nil {
					t.Fatal(err)
				}
				defer raw.Close()
				held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT id,v FROM string_core_view ORDER BY id", nil)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				var dump bytes.Buffer
				if err = stuntgres.Dump(ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err = stuntgres.Reset(ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err = stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				want := [][]any{{int64(1), "é!"}, {int64(2), "x!"}, {int64(3), nil}}
				stringCoreControlTuples(t, db, "SELECT id,v FROM string_core_view ORDER BY id", want)
				var after bytes.Buffer
				if err = stuntgres.Dump(ctx, p.native, &after); err != nil || after.String() != dump.String() {
					t.Fatal("redump mismatch", err)
				}
				dst := make([]driver.Value, 2)
				for _, row := range want {
					if err = held.Next(dst); err != nil || !reflect.DeepEqual([]any{dst[0], dst[1]}, row) {
						t.Fatal(dst, row, err)
					}
				}
				if err = held.Next(dst); !errors.Is(err, io.EOF) {
					t.Fatal(err)
				}
				err = stuntgres.Restore(ctx, p.native, strings.NewReader(`CREATE TABLE staged(v TEXT); INSERT INTO staged VALUES("normalize"('x','bad'))`))
				stringCoreControlDiagnostic(t, err, referenceDiagnostic{Code: "22023", Message: "invalid normalization form: bad"})
				stringCoreControlTuples(t, db, "SELECT id,v FROM string_core_view ORDER BY id", want)
				after.Reset()
				if err = stuntgres.Dump(ctx, p.native, &after); err != nil || after.String() != dump.String() {
					t.Fatal("failed restore changed publication", err)
				}
			}
			stringCoreControlExec(t, db, "INSERT INTO string_core_dest(id) VALUES(21)")
			stringCoreControlTuples(t, db, "SELECT v FROM string_core_dest WHERE id=21", [][]any{{"é"}})
		})
	}
}
