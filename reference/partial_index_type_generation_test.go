package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestReferencePartialIndexTypeGeneration(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[held], func(t *testing.T) {
			regtypeDisplacementScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn, recorder *referenceRecorder) {
				run := func(s referenceStep) {
					t.Helper()
					s.prepared = held
					if len(s.columns) == 3 && s.columns[2] == "a" {
						s.checkRaw = func(t *testing.T, column, row int, value any) bool {
							if column != 2 {
								return false
							}
							regtypeRaw(t, b.pg, value)
							return true
						}
					}
					referenceRunStep(t, p.ctx, conn, recorder, s)
				}
				run(referenceExec("CREATE DOMAIN regtype_displacement.payload AS integer", 0))
				run(referenceExec("CREATE TABLE regtype_displacement.u(id INT,k INT,a regtype_displacement.payload[])", 0))
				// The changed generation is consumed only by the predicate, never a key,
				// default, generated expression, CHECK or view that could mask its omission.
				run(referenceExec("CREATE UNIQUE INDEX pu ON regtype_displacement.u(k) WHERE a IS NULL", 0))
				run(referenceExec("INSERT INTO regtype_displacement.u VALUES(1,7,NULL),(2,7,'{1}'),(3,7,'{}')", 3))
				arrayID := regtypeDisplacementOID(t, p, conn, "payload[]")
				check := func(displaced bool) {
					family := "_PAYLOAD"
					if displaced {
						family = "__PAYLOAD"
					}
					if b.pg {
						family = ""
					}
					run(referenceSelect("SELECT id,k,a FROM regtype_displacement.u ORDER BY id", []string{"id", "k", "a"}, []string{"INT4", "INT4", family}, [][]any{{int64(1), int64(7), nil}, {int64(2), int64(7), "{1}"}, {int64(3), int64(7), "{}"}}))
					run(referenceSelect("SELECT id FROM regtype_displacement.u WHERE a IS NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				}
				check(false)
				var retained *sql.Rows
				if !b.pg {
					var err error
					retained, err = b.db.QueryContext(p.ctx, "SELECT a FROM regtype_displacement.u ORDER BY id")
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
				}
				run(referenceExec("CREATE DOMAIN regtype_displacement._payload AS integer", 0))
				if got := regtypeDisplacementOID(t, p, conn, "__payload"); got != arrayID {
					t.Fatalf("displaced predicate array identity: got %d, want %d", got, arrayID)
				}
				check(true)
				dup := uniqueReferenceDuplicate("u", "pu", "Key (k)=(7) already exists.")
				dup.Schema = "regtype_displacement"
				run(referenceErrorDiagnostic("INSERT INTO regtype_displacement.u VALUES(4,7,NULL)", dup))
				check(true)
				run(referenceExec("INSERT INTO regtype_displacement.u VALUES(4,7,'{NULL}'),(5,8,NULL)", 2))
				run(referenceSelect("SELECT id,k,a IS NULL AS member FROM regtype_displacement.u ORDER BY id", []string{"id", "k", "member"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(7), true}, {int64(2), int64(7), false}, {int64(3), int64(7), false}, {int64(4), int64(7), false}, {int64(5), int64(8), true}}))
				run(referenceErrorDiagnostic("UPDATE regtype_displacement.u SET a=NULL WHERE id=4", dup))
				run(referenceSelect("SELECT id FROM regtype_displacement.u WHERE a IS NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}}))
				family := "__PAYLOAD"
				if b.pg {
					family = ""
				}
				run(referenceSelect("SELECT id,k,a FROM regtype_displacement.u ORDER BY id", []string{"id", "k", "a"}, []string{"INT4", "INT4", family}, [][]any{{int64(1), int64(7), nil}, {int64(2), int64(7), "{1}"}, {int64(3), int64(7), "{}"}, {int64(4), int64(7), "{NULL}"}, {int64(5), int64(8), nil}}))
				if retained != nil {
					columns, err := retained.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if len(columns) != 1 || columns[0].DatabaseTypeName() != "_PAYLOAD" || columns[0].ScanType() != reflect.TypeFor[string]() {
						t.Fatal("retained predicate-array metadata", columns)
					}
					for _, want := range []sql.NullString{{}, {String: "{1}", Valid: true}, {String: "{}", Valid: true}} {
						if !retained.Next() {
							t.Fatal("missing retained array row", retained.Err())
						}
						var got sql.NullString
						if err := retained.Scan(&got); err != nil {
							t.Fatal(err)
						}
						if got != want {
							t.Fatalf("retained array: got %#v, want %#v", got, want)
						}
					}
					if retained.Next() || retained.Err() != nil {
						t.Fatal("retained array tail", retained.Err())
					}
				}
			})
		})
	}
}
