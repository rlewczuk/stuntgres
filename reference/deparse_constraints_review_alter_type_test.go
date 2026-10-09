package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceDeparseConstraintsReviewAlterTypeDefaultIdentity(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("failed=%t/prepared=%t", failed, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						conn, err := backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						postgres := backend.pg
						run := func(s referenceStep) referenceObserved {
							if s.selectRows && s.diagnostic == nil {
								s.checkMetadata = func(t *testing.T, rows *sql.Rows) { deparseReviewMetadata(t, postgres, rows) }
							}
							return referenceRunStep(t, p.ctx, conn, backend.recorder, s)
						}
						exec := func(q string, n int64) { step := referenceExec(q, n); step.prepared = prepared; run(step) }
						rows := func(p bool, q string, names, types []string, want [][]any, args ...any) {
							step := referenceSelect(q, names, types, want, args...)
							step.prepared = p
							run(step)
						}
						text := func(q, want string, args ...any) string {
							step := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{want}}, args...)
							actual := run(step).rows[0][0].(string)
							run(referencePrepared(step))
							return actual
						}
						division := func(err error) {
							want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
							referenceAssertDiagnostic(t, err, &want)
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

						var oldRows func()
						if !postgres {
							oldRows = deparseAlterTypeRetainedRaw(t, conn, name, oldID, relation, attribute)
						}

						query := "ALTER TABLE public." + name + " ALTER COLUMN v TYPE BIGINT"
						if failed {
							query += " USING 10/(v-v)"
						}
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
						if oldRows != nil {
							oldRows()
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
						_, expectedDefaultID := deparseReviewTypedExpected(t, postgres, "OID", fmt.Sprintf("%d", newID))
						_, expectedRelation := deparseReviewTypedExpected(t, postgres, "OID", fmt.Sprintf("%d", relation))
						rows(false, identityQuery, []string{"oid", "adrelid", "adnum"}, []string{"OID", "OID", "INT2"}, [][]any{{expectedDefaultID, expectedRelation, attribute}}, tableRelation)
						baseline := [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), nil}}
						rows(false, "SELECT id,v FROM public."+name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", typ}, baseline)
						rows(true, "SELECT id,v FROM public."+name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", typ}, baseline)

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
							deparseAlterTypeReplay(t, p, conn, postgres, name, retained, exec, rows, text)
						}

					})
				}
			})
		}
	}
}

func deparseAlterTypeRetainedRaw(t *testing.T, conn *sql.Conn, name string, oldID, relation, attribute int64) func() {
	t.Helper()
	var data, catalog driver.Rows
	t.Cleanup(func() {
		if data != nil {
			data.Close()
		}
		if catalog != nil {
			catalog.Close()
		}
	})
	if err := conn.Raw(func(raw any) error {
		query := raw.(driver.QueryerContext)
		var err error
		data, err = query.QueryContext(context.Background(), "SELECT id,v FROM public."+name+" ORDER BY id", nil)
		if err != nil {
			return err
		}
		catalog, err = query.QueryContext(context.Background(), "SELECT oid,adrelid,adnum,adbin FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", []driver.NamedValue{{Ordinal: 1, Value: relation}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return func() {
		metadata := data.(driver.RowsColumnTypeDatabaseTypeName)
		if metadata.ColumnTypeDatabaseTypeName(0) != "INT4" || metadata.ColumnTypeDatabaseTypeName(1) != "INT4" {
			t.Fatal("old data descriptor changed")
		}
		if data.(driver.RowsColumnTypeScanType).ColumnTypeScanType(1) != reflect.TypeFor[int64]() {
			t.Fatal("old data scan type changed")
		}
		for _, want := range [][]driver.Value{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), nil}} {
			got := make([]driver.Value, 2)
			if err := data.Next(got); err != nil || !reflect.DeepEqual(got, want) {
				t.Fatal(got, want, err)
			}
		}
		if err := data.Next(make([]driver.Value, 2)); err != io.EOF {
			t.Fatal(err)
		}
		if err := data.Close(); err != nil {
			t.Fatal(err)
		}
		data = nil
		if catalog.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(3) != "PG_NODE_TREE" || catalog.(driver.RowsColumnTypeScanType).ColumnTypeScanType(3) != reflect.TypeFor[string]() {
			t.Fatal("old tree descriptor changed")
		}
		got := make([]driver.Value, 4)
		if err := catalog.Next(got); err != nil {
			t.Fatal(err)
		}
		if tree, ok := got[3].(string); !ok || !strings.Contains(tree, "type 23 ") || got[0] != oldID || got[1] != relation || got[2] != attribute {
			t.Fatal(got)
		}
		if err := catalog.Next(got); err != io.EOF {
			t.Fatal(err)
		}
		if err := catalog.Close(); err != nil {
			t.Fatal(err)
		}
		catalog = nil
	}
}

func deparseAlterTypeReplay(t *testing.T, p *referencePair, conn *sql.Conn, postgres bool, name string, retained [][]any, exec func(string, int64), rows func(bool, string, []string, []string, [][]any, ...any), text func(string, string, ...any) string) {
	if !postgres {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
	}
	rows(false, "SELECT id,v FROM public."+name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, retained)
	relation := deparseIdentityRelation(t, conn, name)
	var restoredID int64
	if err := conn.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", relation).Scan(&restoredID); err != nil || restoredID <= 0 {
		t.Fatal(restoredID, err)
	}
	for _, pretty := range []bool{false, true} {
		text(fmt.Sprintf("SELECT pg_get_expr(adbin,adrelid,%t) AS d FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", pretty), "1", relation)
	}
	exec("INSERT INTO public."+name+"(id) VALUES(5)", 1)
	rows(false, "SELECT id,v FROM public."+name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, append(retained, []any{int64(5), int64(1)}))
	for mode := range 2 {
		rows(false, fmt.Sprintf("SELECT id,v FROM public.alter_copy%d", mode), []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(12), int64(1)}})
	}
}
