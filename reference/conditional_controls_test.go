package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceConditionalNativeDefaultsAndSnapshots(t *testing.T) {
	ctx := context.Background()
	p := newReferenceScenario(t)
	t.Run("native-controls", func(t *testing.T) {
		db := p.native
		scalarNativeExec(t, db, "CREATE TABLE t(id INT,v INT,b BOOL)")
		scalarNativeExec(t, db, "INSERT INTO t VALUES(1,0,TRUE),(2,NULL,FALSE)")
		scalarNativeExec(t, db, "CREATE TABLE defaults(a INT DEFAULT CASE WHEN TRUE THEN COALESCE(NULL,7) ELSE 1/0 END,b INT DEFAULT NULLIF(2,3),c INT DEFAULT GREATEST(NULL,4),d INT DEFAULT LEAST(NULL,5))")
		scalarNativeExec(t, db, "CREATE TABLE deferred(v INT DEFAULT CASE WHEN FALSE THEN 1 ELSE 1/0 END)")
		scalarNativeExec(t, db, "CREATE TABLE wide(v BIGINT DEFAULT CASE 2147483648 WHEN 2147483648 THEN NULLIF(2147483649,2147483650) ELSE 2147483651 END)")
		scalarNativeExec(t, db, "INSERT INTO deferred VALUES(9)")
		scalarNativeExec(t, db, "INSERT INTO defaults VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)")
		// A retained TEMP sequence forces Int8/Bool default provider retagging when
		// Restore installs its new permanent catalog.
		scalarNativeExec(t, db, "CREATE TEMP SEQUENCE held_seq")
		scalarNativeExec(t, db, "INSERT INTO defaults VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)")
		var dump bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &dump); err != nil {
			t.Fatal("Dump evaluated a dormant default", err)
		}
		scalarNativeRaw(t, p, func(raw driver.QueryerContext) {
			owned := []byte("owned")
			cursor, err := raw.QueryContext(ctx, "SELECT CASE WHEN b THEN v ELSE COALESCE(v,3) END,COALESCE($1,'fallback') FROM t ORDER BY id", []driver.NamedValue{{Ordinal: 1, Value: owned}})
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			owned[0] = 'X'
			if err := stuntgres.Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var redump bytes.Buffer
			if err := stuntgres.Dump(ctx, db, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal("native default serialization changed on replay")
			}
			scalarNativeExec(t, db, "INSERT INTO defaults VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)")
			scalarNativeTuples(t, db, "SELECT a,b,c,d FROM defaults", [][]any{{int64(7), int64(2), int64(4), int64(5)}, {int64(7), int64(2), int64(4), int64(5)}, {int64(7), int64(2), int64(4), int64(5)}})
			scalarNativeExec(t, db, "INSERT INTO wide VALUES(DEFAULT)")
			scalarNativeTuples(t, db, "SELECT v FROM wide", [][]any{{int64(2147483649)}})
			_, err = db.Exec("INSERT INTO deferred VALUES(DEFAULT)")
			scalarNativeDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
			scalarNativeTuples(t, db, "SELECT v FROM deferred", [][]any{{int64(9)}})
			if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(v INT); INSERT INTO replacement VALUES(CASE WHEN TRUE THEN 1 ELSE $1 END)")); err == nil {
				t.Fatal("hidden restore parameter accepted")
			}
			scalarNativeTuples(t, db, "SELECT v FROM deferred", [][]any{{int64(9)}})
			if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(v TEXT); INSERT INTO replacement VALUES('new')")); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cursor.Columns(), []string{"coalesce", "coalesce"}) {
				t.Fatal(cursor.Columns())
			}
			dst := make([]driver.Value, 2)
			for _, want := range [][]driver.Value{{int64(0), "owned"}, {int64(3), "owned"}} {
				if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
					t.Fatal(dst, want, err)
				}
			}
			if err := cursor.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
		})
	})
	for _, backend := range p.backends() {
		if !backend.pg {
			continue
		}
		t.Run(backend.name, func(t *testing.T) {
			run := func(step referenceStep) { scalarRunStep(t, p.ctx, backend.db, backend.recorder, step) }
			run(referenceExec("CREATE TABLE t(id INT,v INT,b BOOL)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,0,TRUE),(2,NULL,FALSE)", 2))
			run(referenceExec("CREATE TABLE defaults(a INT DEFAULT CASE WHEN TRUE THEN COALESCE(NULL,7) ELSE 1/0 END,b INT DEFAULT NULLIF(2,3),c INT DEFAULT GREATEST(NULL,4),d INT DEFAULT LEAST(NULL,5))", 0))
			run(referenceExec("CREATE TABLE deferred(v INT DEFAULT CASE WHEN FALSE THEN 1 ELSE 1/0 END)", 0))
			run(referenceExec("INSERT INTO deferred VALUES(9)", 1))
			run(referenceExec("CREATE TABLE wide(v BIGINT DEFAULT CASE 2147483648 WHEN 2147483648 THEN NULLIF(2147483649,2147483650) ELSE 2147483651 END)", 0))
			for range 3 {
				run(referenceExec("INSERT INTO defaults VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1))
			}
			row := []any{int64(7), int64(2), int64(4), int64(5)}
			run(referenceSelect("SELECT a,b,c,d FROM defaults", []string{"a", "b", "c", "d"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{row, row, row}))
			run(referenceExec("INSERT INTO wide VALUES(DEFAULT)", 1))
			run(referenceSelect("SELECT v FROM wide", []string{"v"}, []string{"INT8"}, [][]any{{int64(2147483649)}}))
			failure := referenceImplicitError("INSERT INTO deferred VALUES(DEFAULT)", referenceDiagnostic{Code: "22012", Message: "division by zero"})
			failure.errorStage = "execute"
			run(failure)
			run(referenceSelect("SELECT v FROM deferred", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
			owned := []byte("owned")
			rows, err := backend.db.QueryContext(p.ctx, "SELECT CASE WHEN b THEN v ELSE COALESCE(v,3) END,COALESCE($1,'fallback') FROM t ORDER BY id", owned)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			owned[0] = 'X'
			scalarRows(t, true, rows, []string{"coalesce", "coalesce"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "owned"}, {int64(3), "owned"}}, false)
		})
	}
}
