package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceRowsMetadataAndCommandSemantics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			rows, err := db.Query("CREATE TABLE metadata (i INTEGER NOT NULL,b BIGINT,t TEXT,f BOOLEAN)")
			if err != nil {
				t.Fatal(err)
			}
			if rows.Next() {
				t.Fatal("command produced row")
			}
			rows.Close()
			driverExecSQL(t, db, "INSERT INTO metadata VALUES (1,2,'x',FALSE)")
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
				if (!backend.pg && (!ok || nullable != (i != 0))) || (backend.pg && (ok || nullable)) {
					t.Fatalf("nullable %d: %v %v", i, nullable, ok)
				}
				scan := []reflect.Type{reflect.TypeOf(int64(0)), reflect.TypeOf(int64(0)), reflect.TypeOf(""), reflect.TypeOf(false)}
				if backend.pg && i == 0 {
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
			r := driverExecSQL(t, db, "SELECT * FROM metadata")
			n, err := r.RowsAffected()
			if err != nil || n != 1 {
				t.Fatalf("select affected: %d %v", n, err)
			}
			_, err = r.LastInsertId()
			wantLastID := "LastInsertId is not supported by this driver"
			if backend.pg {
				wantLastID = "LastInsertId is not supported by this driver"
			}
			if err == nil || err.Error() != wantLastID {
				t.Fatal(err)
			}
			r = driverExecSQL(t, db, "-- empty\n")
			n, err = r.RowsAffected()
			if backend.pg {
				driverArgumentError(t, err)
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
			_, err = db.Exec("INSERT INTO metadata(i) VALUES(9); INSERT INTO metadata(i) VALUES(10)")
			if backend.pg {
				if err != nil {
					t.Fatal(err)
				}
				driverWantTuples(t, db, "SELECT i FROM metadata ORDER BY i", [][]any{{int64(1)}, {int64(9)}, {int64(10)}})
			} else {
				driverSQLState(t, err, "0A000")
				driverWantTuples(t, db, "SELECT i FROM metadata", [][]any{{int64(1)}})
			}
		})
	}
}
