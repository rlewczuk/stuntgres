package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowsMetadataAndCommandSemantics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			rows, err := db.Query("CREATE TABLE metadata (i INTEGER NOT NULL,b BIGINT,t TEXT,f BOOLEAN)")
			if err != nil {
				t.Fatal(err)
			}
			if rows.Next() {
				t.Fatal("command produced row")
			}
			rows.Close()
			fixture.DriverExecSQL(t, db, "INSERT INTO metadata VALUES (1,2,'x',FALSE)")
			rows, err = db.Query("SELECT * FROM metadata")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cols, []string{"i", "b", "t", "f"}) {
				t.Fatal(cols)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range []string{"INT4", "INT8", "TEXT", "BOOL"} {
				if types[i].DatabaseTypeName() != want {
					t.Fatal(types[i].DatabaseTypeName())
				}
				nullable, ok := types[i].Nullable()
				if (!backend.PG && (!ok || nullable != (i != 0))) || (backend.PG && (ok || nullable)) {
					t.Fatalf("nullable %d: %v %v", i, nullable, ok)
				}
				scan := []reflect.Type{reflect.TypeOf(int64(0)), reflect.TypeOf(int64(0)), reflect.TypeOf(""), reflect.TypeOf(false)}
				if backend.PG && i == 0 {
					scan[i] = reflect.TypeFor[int32]()
				}
				if types[i].ScanType() != scan[i] {
					t.Fatal(types[i].ScanType())
				}
			}
			rows.Close()
			if rows.Next() {
				t.Fatal("closed cursor produced row")
			}
			r := fixture.DriverExecSQL(t, db, "SELECT * FROM metadata")
			n, err := r.RowsAffected()
			if err != nil || n != 1 {
				t.Fatalf("select affected: %d %v", n, err)
			}
			_, err = r.LastInsertId()
			wantLastID := "LastInsertId is not supported by this driver"
			if backend.PG {
				wantLastID = "LastInsertId is not supported by this driver"
			}
			if err == nil || err.Error() != wantLastID {
				t.Fatal(err)
			}
			r = fixture.DriverExecSQL(t, db, "-- empty\n")
			n, err = r.RowsAffected()
			if backend.PG {
				fixture.DriverArgumentError(t, err)
				if n != 0 || err.Error() != "no RowsAffected available after the empty statement" {
					t.Fatalf("pq empty affected: %d %v", n, err)
				}
			} else if err != nil || n != 0 {
				t.Fatalf("native empty affected: %d %v", n, err)
			}
			rows, err = db.Query("")
			if err != nil {
				t.Fatal(err)
			}
			if rows.Next() {
				t.Fatal("empty query produced row")
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			r = fixture.DriverExecSQL(t, db, "INSERT INTO metadata(i) VALUES(9); INSERT INTO metadata(i) VALUES(10)")
			n, err = r.RowsAffected()
			if err != nil || n != 1 {
				t.Fatalf("batch last count: %d %v", n, err)
			}
			fixture.DriverWantTuples(t, db, "SELECT i FROM metadata ORDER BY i", [][]any{{int64(1)}, {int64(9)}, {int64(10)}})
		})
	}
}
