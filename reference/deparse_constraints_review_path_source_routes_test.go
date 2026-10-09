package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewCurrentSchemasConsumersSourceConsumerRoutes(t *testing.T) {
	schemaReferenceCleanup(t, "dc_path1", "dc_path2")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			db := b.db
			db.SetMaxOpenConns(1)
			postgres := b.pg
			run := func(s referenceStep) referenceObserved {
				s.nonContext = true
				if s.selectRows && s.diagnostic == nil {
					previous := s.checkMetadata
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						deparseReviewMetadata(t, b.pg, rows)
						if previous != nil {
							previous(t, rows)
						}
					}
				}
				return referenceRunStep(t, p.ctx, db, b.recorder, s)
			}
			run(referenceExec("CREATE SCHEMA dc_path1", 0))
			run(referenceExec("CREATE SCHEMA dc_path2", 0))
			run(referenceExec("CREATE TABLE public.original(id INT,b BOOL,CONSTRAINT c CHECK(current_schemas(b) IS NOT NULL))", 0))
			for mode, expr := range []string{"(current_schemas(b) IS NOT NULL)", "current_schemas(b) IS NOT NULL"} {
				q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)
				step := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (" + expr + ")"}})
				var actual string
				if err := db.QueryRow(step.query, step.args...).Scan(&actual); err != nil || actual != step.rows[0][0] {
					t.Fatal(actual, step.rows, err)
				}
				run(step)
				run(referencePrepared(step))
				run(referenceExec(fmt.Sprintf("CREATE TABLE public.copy%d(id INT,b BOOL,CONSTRAINT c%d %s)", mode, mode, actual), 0))
			}
			for i, name := range []string{"original", "copy0", "copy1"} {
				constraint := "c"
				if i > 0 {
					constraint = fmt.Sprintf("c%d", i-1)
				}
				for _, prepared := range []bool{false, true} {
					step := referenceExec("INSERT INTO public."+name+" VALUES(1,true),(2,false)", 2)
					step.prepared = prepared
					run(step)
					for _, op := range []struct{ query, detail string }{
						{"INSERT INTO public." + name + " VALUES(3,NULL)", "3, null"},
						{"INSERT INTO public." + name + " VALUES(4,true),(3,NULL),(5,false)", "3, null"},
						{"UPDATE public." + name + " SET b=NULL WHERE id=1", "1, null"},
					} {
						step := referenceImplicitError(op.query, referenceCheckViolation(name, constraint, op.detail))
						step.prepared = prepared
						run(step)
						run(referenceSelect("SELECT id,b FROM public."+name+" ORDER BY id", []string{"id", "b"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}}))
					}
					run(referenceExec("DELETE FROM public."+name, 2))
				}
				run(referenceExec("INSERT INTO public."+name+" VALUES(1,true),(2,false)", 2))
				held, err := db.PrepareContext(context.Background(), "SELECT current_schemas(b) AS schemas FROM public."+name+" WHERE id=$1")
				if err != nil {
					t.Fatal(err)
				}
				for _, path := range []struct{ setting, noCatalog, withCatalog string }{
					{"dc_path1,dc_path2", "{dc_path1,dc_path2}", "{pg_catalog,dc_path1,dc_path2}"},
					{"dc_path2,dc_path1", "{dc_path2,dc_path1}", "{pg_catalog,dc_path2,dc_path1}"},
				} {
					run(referenceExec("SET search_path TO "+path.setting, 0))
					for id, want := range []string{path.withCatalog, path.noCatalog} {
						rows, err := held.QueryContext(context.Background(), id+1)
						if err != nil {
							t.Fatal(err)
						}
						types, err := rows.ColumnTypes()
						scan := reflect.TypeOf("")
						if postgres {
							scan = reflect.TypeOf((*any)(nil)).Elem()
						}
						if err != nil || len(types) != 1 || types[0].Name() != "schemas" || types[0].DatabaseTypeName() != "_NAME" || types[0].ScanType() != scan {
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
				step := referenceSelect("SELECT current_schemas($1) AS schemas", []string{"schemas"}, []string{"_NAME"}, [][]any{{nil}}, nil)
				step.prepared = prepared
				run(step)
			}
		})
	}
}
