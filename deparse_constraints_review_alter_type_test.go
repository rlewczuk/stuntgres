package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestDeparseConstraintsReviewAlterTypeDefaultIdentity(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("failed=%t/prepared=%t", failed, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				conn, e := db.Conn(context.Background())
				if e != nil {
					t.Fatal(e)
				}
				defer conn.Close()
				exec := func(q string, n int64) {
					result, err := transactionExec(context.Background(), conn, q, prepared)
					if err != nil {
						t.Fatal(q, err)
					}
					if got, err := result.RowsAffected(); err != nil || got != n {
						t.Fatal(q, got, n, err)
					}
				}
				rows := func(p bool, q string, names, types []string, want [][]any, args ...any) {
					deparseIdentityRows(t, conn, p, q, names, types, want, args...)
				}
				text := func(q, want string, args ...any) string { return deparseIdentityText(t, conn, q, want, args...) }
				division := func(err error) {
					notNullDiagnostic(t, err, stuntgres.Error{Code: "22012", Message: "division by zero"})
				}

				name := "alter_default"
				typ := "INT8"
				if failed {
					name = "alter_default_fail"
					typ = "INT4"
				}
				exec("CREATE TABLE public."+name+"(id INT,v INTEGER DEFAULT 1)", 0)
				exec("INSERT INTO public."+name+"(id) VALUES(1)", 1)
				exec("INSERT INTO public."+name+" VALUES(2,2),(3,NULL)", 2)
				tableRelation := deparseIdentityRelation(t, conn, name)
				var oldID, relation, attribute int64
				identityQuery := "SELECT oid,adrelid,adnum FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2"
				if err := conn.QueryRowContext(context.Background(), identityQuery, tableRelation).Scan(&oldID, &relation, &attribute); err != nil || oldID <= 0 || relation != tableRelation || attribute != 2 {
					t.Fatal(oldID, relation, attribute, err)
				}
				for _, pretty := range []bool{false, true} {
					text(fmt.Sprintf("SELECT pg_get_expr(adbin,adrelid,%t) AS d FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", pretty), "1", tableRelation)
				}

				var oldData, oldCatalog driver.Rows
				if err := conn.Raw(func(raw any) error {
					var err error
					oldData, err = raw.(driver.QueryerContext).QueryContext(context.Background(), "SELECT id,v FROM public."+name+" ORDER BY id", nil)
					if err != nil {
						return err
					}
					oldCatalog, err = raw.(driver.QueryerContext).QueryContext(context.Background(), "SELECT oid,adrelid,adnum,adbin FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", []driver.NamedValue{{Ordinal: 1, Value: tableRelation}})
					return err
				}); err != nil {
					if oldData != nil {
						oldData.Close()
					}
					t.Fatal(err)
				}
				defer oldData.Close()
				defer oldCatalog.Close()

				query := "ALTER TABLE public." + name + " ALTER COLUMN v TYPE BIGINT"
				if failed {
					query += " USING 10/(v-v)"
				}
				var err error
				if prepared {
					stmt, e := conn.PrepareContext(context.Background(), query)
					if e != nil {
						t.Fatal(e)
					}
					_, err = stmt.ExecContext(context.Background())
					stmt.Close()
				} else {
					_, err = conn.ExecContext(context.Background(), query)
				}
				if failed {
					division(err)
				} else if err != nil {
					t.Fatal(err)
				}
				var newID, newRelation, newAttribute int64
				if err := conn.QueryRowContext(context.Background(), identityQuery, tableRelation).Scan(&newID, &newRelation, &newAttribute); err != nil || newID <= 0 || newRelation != relation || newAttribute != attribute {
					t.Fatal(newID, newRelation, newAttribute, err)
				}
				if failed {
					if newID != oldID {
						t.Fatal("failed type change replaced live default", oldID, newID)
					}
				} else {
					if newID == oldID {
						t.Fatal("changed type retained obsolete default identity", oldID, newID)
					}
					rows(false, "SELECT oid FROM pg_catalog.pg_attrdef WHERE oid=$1", []string{"oid"}, []string{"OID"}, [][]any{}, oldID)
				}
				rows(false, identityQuery, []string{"oid", "adrelid", "adnum"}, []string{"OID", "OID", "INT2"}, [][]any{{newID, relation, attribute}}, tableRelation)
				baseline := [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), nil}}
				rows(false, "SELECT id,v FROM public."+name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", typ}, baseline)
				rows(true, "SELECT id,v FROM public."+name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", typ}, baseline)

				if oldData.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "INT4" || oldCatalog.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(3) != "PG_NODE_TREE" {
					t.Fatal("eager metadata changed")
				}
				for _, want := range [][]driver.Value{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), nil}} {
					got := make([]driver.Value, 2)
					if err := oldData.Next(got); err != nil || !reflect.DeepEqual(got, want) {
						t.Fatal(got, want, err)
					}
				}
				if err := oldData.Next(make([]driver.Value, 2)); err != io.EOF {
					t.Fatal(err)
				}
				old := make([]driver.Value, 4)
				if err := oldCatalog.Next(old); err != nil || old[0] != oldID || old[1] != relation || old[2] != attribute || !strings.Contains(old[3].(string), "type 23 ") {
					t.Fatal(old, err)
				}
				if err := oldCatalog.Next(make([]driver.Value, 4)); err != io.EOF {
					t.Fatal(err)
				}

				exec("INSERT INTO public."+name+"(id) VALUES(4)", 1)
				retained := append(baseline, []any{int64(4), int64(1)})
				rows(false, "SELECT id,v FROM public."+name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", typ}, retained)
				for mode, pretty := range []bool{false, true} {
					actual := text(fmt.Sprintf("SELECT pg_get_expr(adbin,adrelid,%t) AS d FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", pretty), "1", tableRelation)
					declaration := "BIGINT"
					if failed {
						declaration = "INTEGER"
					}
					copyName := fmt.Sprintf("alter_copy%d", mode)
					exec("CREATE TABLE public."+copyName+"(id INT,v "+declaration+" DEFAULT "+actual+")", 0)
					exec("INSERT INTO public."+copyName+"(id) VALUES(12)", 1)
					rows(false, "SELECT id,v FROM public."+copyName, []string{"id", "v"}, []string{"INT4", typ}, [][]any{{int64(12), int64(1)}})
					rows(true, "SELECT id,v FROM public."+copyName, []string{"id", "v"}, []string{"INT4", typ}, [][]any{{int64(12), int64(1)}})
				}

				if !failed {
					var dump bytes.Buffer
					if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Reset(context.Background(), db); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					rows(false, "SELECT id,v FROM public."+name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, retained)
					restoredRelation := deparseIdentityRelation(t, conn, name)
					var restoredID int64
					if err := conn.QueryRowContext(context.Background(), "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", restoredRelation).Scan(&restoredID); err != nil || restoredID <= 0 {
						t.Fatal(restoredID, err)
					}
					for _, pretty := range []bool{false, true} {
						text(fmt.Sprintf("SELECT pg_get_expr(adbin,adrelid,%t) AS d FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", pretty), "1", restoredRelation)
					}
					exec("INSERT INTO public."+name+"(id) VALUES(5)", 1)
					rows(false, "SELECT id,v FROM public."+name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, append(retained, []any{int64(5), int64(1)}))
					for mode := range 2 {
						rows(false, fmt.Sprintf("SELECT id,v FROM public.alter_copy%d", mode), []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(12), int64(1)}})
					}
				}

			})
		}
	}
}
