package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func jsonbReferenceMetadata(t *testing.T, rows *sql.Rows, pg, nullable bool) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 {
		t.Fatal(types)
	}
	typ := types[0]
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if typ.Name() != "v" || typ.DatabaseTypeName() != "JSONB" || typ.ScanType() != scan {
		t.Fatalf("metadata %q/%q/%v want v/JSONB/%v", typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), scan)
	}
	if length, ok := typ.Length(); ok {
		t.Fatalf("unexpected length %d", length)
	}
	if precision, scale, ok := typ.DecimalSize(); ok {
		t.Fatalf("unexpected numeric metadata %d,%d", precision, scale)
	}
	n, known := typ.Nullable()
	if pg {
		if n || known {
			t.Fatalf("pq nullable=%v,%v", n, known)
		}
	} else if !known || n != nullable {
		t.Fatalf("native nullable=%v,%v want %v,true", n, known, nullable)
	}
}
func TestReferenceJSONBNullAndMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE jb_meta(id INT,v JSONB)", 0))
		run(referenceExec("CREATE TABLE jb_required(v JSONB NOT NULL)", 0))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q        string
				nullable bool
			}{{"SELECT v FROM jb_meta", true}, {"SELECT v FROM jb_required", false}} {
				s := referenceSelect(tc.q, []string{"v"}, []string{"JSONB"}, nil)
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) { jsonbReferenceMetadata(t, rows, backend.pg, tc.nullable) }
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
		}
		run(referenceExec(`INSERT INTO jb_meta VALUES(1,NULL),(2,'null'),(3,'"null"'),(4,' {"x":1,"x":2} ')`, 4))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q    string
				want [][]any
			}{
				{"SELECT v FROM jb_meta WHERE id=1", [][]any{{nil}}}, {"SELECT v FROM jb_meta WHERE id=99", nil},
				{"SELECT v FROM jb_meta ORDER BY id", [][]any{{nil}, {"null"}, {`"null"`}, {`{"x": 2}`}}},
			} {
				s := referenceSelect(tc.q, []string{"v"}, []string{"JSONB"}, tc.want)
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) { jsonbReferenceMetadata(t, rows, backend.pg, true) }
				s.checkScan = func(t *testing.T, rows *sql.Rows, i int) {
					want := tc.want[i][0]
					var raw any
					if err := rows.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					referenceAssertRawValue(t, backend.pg, "JSONB", raw)
					if !reflect.DeepEqual(referenceNormalize(raw), want) {
						t.Fatalf("raw %#v want %#v", raw, want)
					}
					var n sql.NullString
					if err := rows.Scan(&n); err != nil {
						t.Fatal(err)
					}
					if n.Valid != (want != nil) || (n.Valid && n.String != want) {
						t.Fatalf("NullString %#v want %#v", n, want)
					}
					var bytes []byte
					if err := rows.Scan(&bytes); err != nil {
						t.Fatal(err)
					}
					if want == nil {
						if bytes != nil {
							t.Fatal(bytes)
						}
					} else {
						if string(bytes) != want {
							t.Fatalf("bytes %q want %q", bytes, want)
						}
						bytes[0] = 'x'
						var text string
						if err := rows.Scan(&text); err != nil || text != want {
							t.Fatalf("rescan %q want %q err=%v", text, want, err)
						}
					}
				}
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
		}
		run(referenceSelect("SELECT id FROM jb_meta WHERE v IS NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(referencePrepared(referenceSelect("SELECT id FROM jb_meta WHERE v IS NOT NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(4)}})))
		run(referenceSelect("SELECT count(v) AS n,count(*) AS all_rows FROM jb_meta", []string{"n", "all_rows"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(4)}}))
		run(referenceExec("INSERT INTO jb_required VALUES('null')", 1))
		want := controlsReferenceDiagnostic("23502", `null value in column "v" of relation "jb_required" violates not-null constraint`)
		want.Detail = "Failing row contains (null)."
		want.Schema = "public"
		want.Table = "jb_required"
		want.Column = "v"
		run(referenceErrorDiagnostic("INSERT INTO jb_required VALUES(NULL)", want))
		run(referenceSelect("SELECT v FROM jb_required", []string{"v"}, []string{"JSONB"}, [][]any{{"null"}}))
	}
}

func TestReferenceJSONBArraysAndDomains(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE DOMAIN jb_document AS jsonb", 0))
		run(referenceExec("CREATE TABLE jb_array(id INT,v JSONB[],d jb_document)", 0))
		run(referenceExec(`INSERT INTO jb_array VALUES(1,NULL,NULL),(2,'{NULL,"null",1.00}',' {"a":1,"a":2} ')`, 2))
		run(referencePrepared(referenceExec("INSERT INTO jb_array VALUES(3,$1,$2)", 1, `{"{\"b\":2,\"a\":1}",NULL}`, `[1,true]`)))
		var nilBytes []byte
		run(referencePrepared(referenceExec("INSERT INTO jb_array VALUES(4,$1,$2)", 1, nil, nilBytes)))
		for _, prepared := range []bool{false, true} {
			s := referenceSelect("SELECT id,v,d FROM jb_array ORDER BY id", []string{"id", "v", "d"}, []string{"INT4", "_JSONB", "JSONB"}, [][]any{{int64(1), nil, nil}, {int64(2), `{NULL,"null",1.00}`, `{"a": 2}`}, {int64(3), `{"{\"a\": 1, \"b\": 2}",NULL}`, `[1, true]`}, {int64(4), nil, nil}})
			s.rawTypes = []string{"INT4", "_JSONB", "JSONB"}
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
		}
	}
}
