package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type deparseIdentityControlObject struct {
	caseIndex int
	tc        deparseIdentityCase
	name      string
	insert    *sql.Stmt
}

func deparseIdentityTreeScanType(pg bool) reflect.Type {
	if pg {
		return reflect.TypeFor[any]()
	}
	return reflect.TypeFor[string]()
}

func deparseControlViolation(t *testing.T, err error, table, constraint, detail string) {
	want := fixture.CheckViolation(table, constraint, detail)
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	fixture.AssertDiagnostic(t, err, &want)
}

func deparseIdentityControlObserve(t *testing.T, conn *sql.Conn, pg bool, name string, tc deparseIdentityCase, text func(string, string, ...any) string) {
	relation := deparseIdentityRelation(t, conn, name)
	for _, pretty := range []bool{false, true} {
		text(fmt.Sprintf("SELECT pg_get_expr(adbin,adrelid,%t) AS d FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", pretty), tc.leaf, relation)
	}
	for _, pretty := range []bool{false, true} {
		expression := "(NOT (" + tc.leaf + " IS DISTINCT FROM expected))"
		if pretty {
			expression = "NOT " + tc.leaf + " IS DISTINCT FROM expected"
		}
		text(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='c'", pretty), "CHECK ("+expression+")", relation)
		text(fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) AS d FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='c'", pretty), expression, relation)
	}
	var defaultID, defaultRelation, constraintID, constraintRelation int64
	var attribute int
	if err := conn.QueryRowContext(context.Background(), "SELECT oid,adrelid,adnum FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", relation).Scan(&defaultID, &defaultRelation, &attribute); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(context.Background(), "SELECT oid,conrelid FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='c'", relation).Scan(&constraintID, &constraintRelation); err != nil {
		t.Fatal(err)
	}
	if defaultID <= 0 || constraintID <= 0 || defaultID == constraintID || defaultRelation != relation || constraintRelation != relation || attribute != 2 {
		t.Fatal("invalid live definition relationships", name, defaultID, defaultRelation, attribute, constraintID, constraintRelation)
	}
	for _, q := range []string{
		"SELECT adbin FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2",
		"SELECT conbin FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='c'",
	} {
		cursor, err := conn.QueryContext(context.Background(), q, relation)
		if err != nil {
			t.Fatal(err)
		}
		metadata, err := cursor.ColumnTypes()
		if err != nil || len(metadata) != 1 || metadata[0].DatabaseTypeName() != "PG_NODE_TREE" || metadata[0].ScanType() != deparseIdentityTreeScanType(pg) {
			cursor.Close()
			t.Fatal(metadata, err)
		}
		if !cursor.Next() {
			cursor.Close()
			t.Fatal("missing structural carrier", cursor.Err())
		}
		var tree string
		if err := cursor.Scan(&tree); err != nil || (tree == "" || (!pg && !strings.Contains(tree, "Identity kind"))) {
			cursor.Close()
			t.Fatal(tree, err)
		}
		if cursor.Next() || cursor.Err() != nil {
			cursor.Close()
			t.Fatal("extra structural carrier", cursor.Err())
		}
		cursor.Close()
	}
}

func deparseIdentityControlCreate(t *testing.T, conn *sql.Conn, i int, tc deparseIdentityCase, exec func(string, int64), text func(string, string, ...any) string, observe func(string, deparseIdentityCase), expected func(deparseIdentityCase, string, any) any, rows func(bool, string, []string, []string, [][]any), held *[]deparseIdentityControlObject) {
	typ := "NAME"
	if tc.kind == "system" || tc.kind == "fallback" {
		typ = "TEXT"
	}
	name := fmt.Sprintf("control%d", i)
	exec(fmt.Sprintf("CREATE TABLE public.%s(id INT,s TEXT DEFAULT %s,expected %s,CONSTRAINT c CHECK(%s IS NOT DISTINCT FROM expected))", name, tc.input, typ, tc.input), 0)
	relation := deparseIdentityRelation(t, conn, name)
	names := []string{name}
	for mode, pretty := range []bool{false, true} {
		defaultSQL := text(fmt.Sprintf("SELECT pg_get_expr(adbin,adrelid,%t) AS d FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", pretty), tc.leaf, relation)
		expression := "(NOT (" + tc.leaf + " IS DISTINCT FROM expected))"
		if pretty {
			expression = "NOT " + tc.leaf + " IS DISTINCT FROM expected"
		}
		suffix := text(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='c'", pretty), "CHECK ("+expression+")", relation)
		copyName := fmt.Sprintf("%s_copy%d", name, mode)
		exec("CREATE TABLE public."+copyName+"(id INT,s TEXT DEFAULT "+defaultSQL+",expected "+typ+",CONSTRAINT c "+suffix+")", 0)
		names = append(names, copyName)
	}
	x := expected(tc, "dc_identity_control", "dc_control_a")
	for _, objectName := range names {
		observe(objectName, tc)
		stmt, err := conn.PrepareContext(context.Background(), "INSERT INTO public."+objectName+"(id,expected) VALUES($1,$2)")
		if err != nil {
			t.Fatal(err)
		}
		*held = append(*held, deparseIdentityControlObject{i, tc, objectName, stmt})
		if _, err := stmt.ExecContext(context.Background(), 1, x); err != nil {
			t.Fatal(err)
		}
		rows(false, "SELECT id,s,expected FROM public."+objectName, []string{"id", "s", "expected"}, []string{"INT4", "TEXT", typ}, [][]any{{int64(1), x, x}})
		// Restore uses isolated authority: replay empty symbolic CHECK objects, not contextual rows.
		exec("DELETE FROM public."+objectName, 1)
		rows(false, "SELECT id,s,expected FROM public."+objectName, []string{"id", "s", "expected"}, []string{"INT4", "TEXT", typ}, nil)
	}
	exec(fmt.Sprintf("CREATE TABLE public.control_data%d(id INT,s TEXT DEFAULT %s)", i, tc.input), 0)
	exec(fmt.Sprintf("INSERT INTO public.control_data%d(id) VALUES(1)", i), 1)
	rows(false, fmt.Sprintf("SELECT id,s FROM public.control_data%d", i), []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), x}})
}

func deparseIdentityControlExpected(t *testing.T, database string, system any, tc deparseIdentityCase, principal string, path any) any {
	switch tc.kind {
	case "catalog":
		return database
	case "current", "session":
		return principal
	case "system":
		return system
	case "fallback":
		if system != nil {
			return system
		}
		return "trusted"
	case "schema":
		return path
	}
	t.Fatal(tc.kind)
	return nil
}

func TestReferenceDeparseConstraintsReviewIdentityControls(t *testing.T) {
	for caseIndex, tc := range deparseIdentityCases {
		t.Run(fmt.Sprintf("%02d", caseIndex), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"dc_identity_control"})
			fixture.SchemaReferenceCleanup(t, "dc_control_a", "dc_control_b")
			database := deparseIdentityCatalogDisplay("reference/" + t.Name())
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					db := backend.DB
					bootstrap := "postgres"
					var system any
					if backend.PG {
						database = "stuntgres_reference"
						bootstrap = "stuntgres"
						system = "scram-sha-256:stuntgres"
					}
					conn, err := db.Conn(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(step fixture.Step) fixture.Observed {
						return fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
					}
					exec := func(q string, n int64) { run(fixture.Exec(q, n)) }
					rows := func(prepared bool, q string, names, types []string, want [][]any) {
						step := fixture.Select(q, names, types, want)
						step.Prepared = prepared
						run(step)
					}
					defer func() {
						for _, q := range []string{"RESET SESSION AUTHORIZATION", "RESET ROLE", "SET search_path TO public", "ALTER SCHEMA public OWNER TO " + bootstrap} {
							if _, err := conn.ExecContext(context.Background(), q); err != nil {
								t.Error(q, err)
							}
						}
					}()
					exec("CREATE ROLE dc_identity_control", 0)
					exec("ALTER SCHEMA public OWNER TO dc_identity_control", 0)
					for _, schema := range []string{"dc_control_a", "dc_control_b"} {
						exec("CREATE SCHEMA "+schema+" AUTHORIZATION dc_identity_control", 0)
					}
					exec("SET SESSION AUTHORIZATION dc_identity_control", 0)
					exec("SET search_path TO dc_control_a,dc_control_b", 0)

					var held []deparseIdentityControlObject
					defer func() {
						for _, object := range held {
							object.insert.Close()
						}
					}()
					expected := func(tc deparseIdentityCase, principal string, path any) any {
						return deparseIdentityControlExpected(t, database, system, tc, principal, path)
					}
					text := func(q, want string, args ...any) string {
						step := fixture.Select(q, []string{"d"}, []string{"TEXT"}, [][]any{{want}}, args...)
						actual := run(step).Rows[0][0].(string)
						run(fixture.Prepared(step))
						return actual
					}
					observe := func(name string, tc deparseIdentityCase) {
						deparseIdentityControlObserve(t, conn, backend.PG, name, tc, text)
					}
					deparseIdentityControlCreate(t, conn, caseIndex, tc, exec, text, observe, expected, rows, &held)
					if !backend.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
					}
					exec("RESET SESSION AUTHORIZATION", 0)
					for _, object := range held {
						observe(object.name, object.tc)
					}
					for frame, path := range []any{"dc_control_b", nil} {
						if path == nil {
							exec("SET search_path TO dc_control_missing", 0)
						} else {
							exec("SET search_path TO dc_control_b,dc_control_a", 0)
						}
						for _, object := range held {
							i, tc, name := object.caseIndex, object.tc, object.name
							typ := "NAME"
							if tc.kind == "system" || tc.kind == "fallback" {
								typ = "TEXT"
							}
							x := expected(tc, bootstrap, path)
							if _, err := object.insert.ExecContext(context.Background(), frame+2, x); err != nil {
								t.Fatal(err)
							}
							old := expected(tc, "dc_identity_control", "dc_control_a")
							stored := expected(tc, bootstrap, "dc_control_b")
							if _, err := conn.ExecContext(context.Background(), "UPDATE public."+name+" SET expected=$1 WHERE id=2", x); err != nil {
								t.Fatal(err)
							}
							want := [][]any{{int64(2), stored, x}}
							if frame == 1 {
								want = append(want, []any{int64(3), x, x})
							}
							rows(true, "SELECT id,s,expected FROM public."+name+" ORDER BY id", []string{"id", "s", "expected"}, []string{"INT4", "TEXT", typ}, want)
							detail := func(v any) string {
								if v == nil {
									return "null"
								}
								return v.(string)
							}
							_, err := object.insert.ExecContext(context.Background(), 90, "wrong_identity")
							deparseControlViolation(t, err, name, "c", "90, "+detail(x)+", wrong_identity")
							rows(true, "SELECT id,s,expected FROM public."+name+" ORDER BY id", []string{"id", "s", "expected"}, []string{"INT4", "TEXT", typ}, want)
							_, err = conn.ExecContext(context.Background(), "UPDATE public."+name+" SET expected=$1 WHERE id=2", "wrong_identity")
							deparseControlViolation(t, err, name, "c", "2, "+detail(stored)+", wrong_identity")
							rows(true, "SELECT id,s,expected FROM public."+name+" ORDER BY id", []string{"id", "s", "expected"}, []string{"INT4", "TEXT", typ}, want)
							rows(true, fmt.Sprintf("SELECT id,s FROM public.control_data%d", i), []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), old}})
							observe(name, tc)
						}
					}
					for _, object := range held {
						if err := object.insert.Close(); err != nil {
							t.Fatal(err)
						}
					}
					exec("SET search_path TO public", 0)
					for _, object := range held {
						exec("DROP TABLE public."+object.name, 0)
					}
					exec(fmt.Sprintf("DROP TABLE public.control_data%d", caseIndex), 0)
					for _, schema := range []string{"dc_control_a", "dc_control_b"} {
						exec("DROP SCHEMA "+schema, 0)
					}
					exec("ALTER SCHEMA public OWNER TO "+bootstrap, 0)
					exec("DROP ROLE dc_identity_control", 0)
				})
			}
		})
	}
}
