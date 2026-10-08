//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDeparseConstraintsReviewAlterTypeDefaultIdentity(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("failed=%t/prepared=%t", failed, prepared), func(t *testing.T) {
				deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
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

				})
			})
		}
	}
}
