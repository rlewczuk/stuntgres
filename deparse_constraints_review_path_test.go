package stuntgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres"
	"testing"
)

func TestDeparseConstraintsReviewCurrentSchemasConsumers(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	tableCoreExec(t, db, false, "CREATE SCHEMA dc_path1", 0)
	tableCoreExec(t, db, false, "CREATE SCHEMA dc_path2", 0)
	tableCoreExec(t, db, false, "CREATE TABLE public.original(id INT,b BOOL,CONSTRAINT c CHECK(current_schemas(b) IS NOT NULL))", 0)
	for mode, expr := range []string{"(current_schemas(b) IS NOT NULL)", "current_schemas(b) IS NOT NULL"} {
		q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
		deparseReviewTypedValue(t, db, q, "TEXT", "CHECK ("+expr+")")
		var actual string
		if err := db.QueryRow(q).Scan(&actual); err != nil {
			t.Fatal(err)
		}
		tableCoreExec(t, db, false, fmt.Sprintf("CREATE TABLE public.copy%d(id INT,b BOOL,CONSTRAINT c%d %s)", mode, mode, actual), 0)
	}
	for i, name := range []string{"original", "copy0", "copy1"} {
		constraint := "c"
		if i > 0 {
			constraint = fmt.Sprintf("c%d", i-1)
		}
		for _, prepared := range []bool{false, true} {
			tableCoreExec(t, db, prepared, "INSERT INTO public."+name+" VALUES(1,true),(2,false)", 2)
			for _, op := range []struct{ query, detail string }{
				{"INSERT INTO public." + name + " VALUES(3,NULL)", "3, null"},
				{"INSERT INTO public." + name + " VALUES(4,true),(3,NULL),(5,false)", "3, null"},
				{"UPDATE public." + name + " SET b=NULL WHERE id=1", "1, null"},
			} {
				checkExecError(t, db, prepared, op.query, checkViolation(name, constraint, op.detail))
				wantTuples(t, db, "SELECT id,b FROM public."+name+" ORDER BY id", [][]any{{int64(1), true}, {int64(2), false}})
			}
			tableCoreExec(t, db, false, "DELETE FROM public."+name, 2)
		}
		tableCoreExec(t, db, false, "INSERT INTO public."+name+" VALUES(1,true),(2,false)", 2)
		held, err := db.PrepareContext(context.Background(), "SELECT current_schemas(b) AS schemas FROM public."+name+" WHERE id=$1")
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []struct{ setting, noCatalog, withCatalog string }{
			{"dc_path1,dc_path2", "{dc_path1,dc_path2}", "{pg_catalog,dc_path1,dc_path2}"},
			{"dc_path2,dc_path1", "{dc_path2,dc_path1}", "{pg_catalog,dc_path2,dc_path1}"},
		} {
			tableCoreExec(t, db, false, "SET search_path TO "+path.setting, 0)
			for id, want := range []string{path.withCatalog, path.noCatalog} {
				rows, err := held.QueryContext(context.Background(), id+1)
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].Name() != "schemas" || types[0].DatabaseTypeName() != "_NAME" || types[0].ScanType() != reflect.TypeOf("") {
					rows.Close()
					t.Fatal(types, err)
				}
				if !rows.Next() {
					rows.Close()
					t.Fatal("missing schemas", rows.Err())
				}
				var got string
				if err := rows.Scan(&got); err != nil || got != want {
					rows.Close()
					t.Fatal(got, want, err)
				}
				if rows.Next() || rows.Err() != nil {
					rows.Close()
					t.Fatal("extra schemas", rows.Err())
				}
				rows.Close()
			}
		}
		held.Close()
	}
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		if prepared {
			s, e := db.Prepare("SELECT current_schemas($1) AS schemas")
			if e != nil {
				t.Fatal(e)
			}
			rows, err = s.Query(nil)
			defer s.Close()
		} else {
			rows, err = db.Query("SELECT current_schemas($1) AS schemas", nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var got any
		if err := rows.Scan(&got); err != nil || got != nil {
			t.Fatal(got, err)
		}
		rows.Close()
	}

}
