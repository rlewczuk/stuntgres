package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func jsonReferenceMetadata(t *testing.T, rows *sql.Rows, pg bool, nullable bool) {
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
	if typ.Name() != "v" || typ.DatabaseTypeName() != "JSON" || typ.ScanType() != scan {
		t.Fatalf("metadata name=%q type=%q scan=%v; want v/JSON/%v", typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), scan)
	}
	if length, ok := typ.Length(); ok {
		t.Fatalf("unexpected length %d", length)
	}
	if precision, scale, ok := typ.DecimalSize(); ok {
		t.Fatalf("unexpected precision %d,%d", precision, scale)
	}
	n, known := typ.Nullable()
	if pg {
		if n || known {
			t.Fatalf("pq nullable=%v,%v; want false,false", n, known)
		}
	} else if !known || n != nullable {
		t.Fatalf("native nullable=%v,%v; want %v,true", n, known, nullable)
	}
}

func TestReferenceJSONMetadataAndScans(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE json_meta(id INTEGER,v JSON)", 0))
		run(referenceExec("CREATE TABLE json_required(v JSON NOT NULL)", 0))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q        string
				nullable bool
				want     [][]any
			}{
				{"SELECT v FROM json_meta", true, nil},
				{"SELECT v FROM json_required", false, nil},
			} {
				s := referenceSelect(tc.q, []string{"v"}, []string{"JSON"}, tc.want)
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) { jsonReferenceMetadata(t, rows, backend.pg, tc.nullable) }
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
		}
		run(referenceExec("INSERT INTO json_meta VALUES(1,NULL),(2,'null'),(3,'\"null\"'),(4,' {\"x\":1,\"x\":2} ')", 4))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q    string
				want [][]any
			}{
				{"SELECT v FROM json_meta WHERE id=1", [][]any{{nil}}},
				{"SELECT v FROM json_meta WHERE id=99", nil},
				{"SELECT v FROM json_meta ORDER BY id", [][]any{{nil}, {"null"}, {`"null"`}, {` {"x":1,"x":2} `}}},
			} {
				s := referenceSelect(tc.q, []string{"v"}, []string{"JSON"}, tc.want)
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) { jsonReferenceMetadata(t, rows, backend.pg, true) }
				s.checkScan = func(t *testing.T, rows *sql.Rows, i int) {
					var raw any
					if err := rows.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					want := tc.want[i][0]
					if want == nil {
						if raw != nil {
							t.Fatalf("NULL raw=%T %v", raw, raw)
						}
					} else {
						if backend.pg {
							if _, ok := raw.([]byte); !ok {
								t.Fatalf("pq JSON raw=%T; want []byte", raw)
							}
						} else if _, ok := raw.(string); !ok {
							t.Fatalf("native JSON raw=%T; want string", raw)
						}
					}
					var n sql.NullString
					if err := rows.Scan(&n); err != nil {
						t.Fatal(err)
					}
					if n.Valid != (want != nil) || (n.Valid && n.String != want) {
						t.Fatalf("NullString=%#v want=%#v", n, want)
					}
					var bytes []byte
					if err := rows.Scan(&bytes); err != nil {
						t.Fatal(err)
					}
					if want == nil {
						if bytes != nil {
							t.Fatalf("NULL bytes=%v", bytes)
						}
					} else {
						if string(bytes) != want {
							t.Fatalf("bytes=%q want=%q", bytes, want)
						}
						bytes[0] = 'x'
						var text string
						if err := rows.Scan(&text); err != nil || text != want {
							t.Fatalf("detached rescan=%q want=%q err=%v", text, want, err)
						}
					}
				}
				if prepared {
					s = referencePrepared(s)
				}
				run(s)
			}
		}
		run(referenceSelect("SELECT v FROM json_meta WHERE id=4", []string{"v"}, []string{"JSON"}, [][]any{{` {"x":1,"x":2} `}}))
		run(referenceExec("INSERT INTO json_required VALUES('null')", 1))
		want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "json_required" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "json_required", Column: "v"}
		run(referenceErrorDiagnostic("INSERT INTO json_required VALUES(NULL)", want))
		run(referenceSelect("SELECT v FROM json_required", []string{"v"}, []string{"JSON"}, [][]any{{"null"}}))
	}
}
