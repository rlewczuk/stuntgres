package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"testing"
	"unicode/utf8"
)

type deparseIdentityCase struct{ input, leaf, kind string }

var deparseIdentityCases = []deparseIdentityCase{
	{"CURRENT_CATALOG", "CURRENT_CATALOG", "catalog"},
	{"CURRENT_ROLE", "CURRENT_ROLE", "current"},
	{"CURRENT_USER", "CURRENT_USER", "current"},
	{"SESSION_USER", "SESSION_USER", "session"},
	{"USER", "USER", "current"},
	{"SYSTEM_USER", "SYSTEM_USER", "system"},
	{"CURRENT_SCHEMA", "CURRENT_SCHEMA", "schema"},
	{"current_database()", "current_database()", "catalog"},
	{"pg_catalog.current_database()", "current_database()", "catalog"},
	{`"current_database"()`, "current_database()", "catalog"},
	{`pg_catalog."current_database"()`, "current_database()", "catalog"},
	{"current_schema()", `"current_schema"()`, "schema"},
	{"pg_catalog.current_schema()", `"current_schema"()`, "schema"},
	{`"current_schema"()`, `"current_schema"()`, "schema"},
	{`pg_catalog."current_schema"()`, `"current_schema"()`, "schema"},
	{"pg_catalog.current_user()", `"current_user"()`, "current"},
	{`pg_catalog."current_user"()`, `"current_user"()`, "current"},
	{`"current_user"()`, `"current_user"()`, "current"},
	{"pg_catalog.session_user()", `"session_user"()`, "session"},
	{`pg_catalog."session_user"()`, `"session_user"()`, "session"},
	{`"session_user"()`, `"session_user"()`, "session"},
	{"pg_catalog.system_user()", `"system_user"()`, "system"},
	{`pg_catalog."system_user"()`, `"system_user"()`, "system"},
	{`"system_user"()`, `"system_user"()`, "system"},
	{"COALESCE(SYSTEM_USER,'trusted')", "COALESCE(SYSTEM_USER, 'trusted'::text)", "fallback"},
}

func TestReferenceDeparseConstraintsReviewIdentityConsumers(t *testing.T) {
	for i, tc := range deparseIdentityCases {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			roleReferenceCleanup(t, []string{"dc_identity_role"})
			schemaReferenceCleanup(t, "dc_identity_path_a", "dc_identity_path_b")
			database := deparseIdentityCatalogDisplay("reference/" + t.Name())
			deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
				bootstrap := "postgres"
				var system any
				ownDatabase := database
				if postgres {
					bootstrap = "stuntgres"
					system = "scram-sha-256:stuntgres"
					ownDatabase = "stuntgres_reference"
				}
				deparseIdentityConsumer(t, conn, ownDatabase, bootstrap, system, tc, run)
			})
		})
	}
	t.Run("nested", deparseIdentityNested)
}
func deparseIdentityConsumer(t *testing.T, conn *sql.Conn, database, bootstrap string, system any, tc deparseIdentityCase, run func(referenceStep) referenceObserved) {
	exec := func(q string, n int64) { run(referenceExec(q, n)) }
	rows := func(p bool, q string, names, types []string, want [][]any) {
		if want == nil {
			want = [][]any{}
		}
		step := referenceSelect(q, names, types, want)
		step.prepared = p
		run(step)
	}
	text := func(q, want string, args ...any) string {
		step := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{want}}, args...)
		actual := run(step).rows[0][0].(string)
		run(referencePrepared(step))
		return actual
	}
	dml := func(p bool, q string, args ...any) (sql.Result, error) {
		if p {
			s, e := conn.PrepareContext(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			return s.ExecContext(context.Background(), args...)
		}
		return conn.ExecContext(context.Background(), q, args...)
	}
	violation := func(err error, table, constraint, detail string) {
		want := referenceCheckViolation(table, constraint, detail)
		want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
		referenceAssertDiagnostic(t, err, &want)
	}

	expectedType := "NAME"
	if tc.kind == "system" || tc.kind == "fallback" {
		expectedType = "TEXT"
	}
	value := func(current, session string, path any) any {
		principal := current
		if tc.kind == "session" {
			principal = session
		}
		return deparseIdentityControlExpected(t, database, system, tc, principal, path)
	}
	var cleanup []string
	defer func() {
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "RESET ROLE", "SET search_path TO public"} {
			if _, err := conn.ExecContext(context.Background(), q); err != nil {
				t.Error(q, err)
			}
		}
		for i := len(cleanup) - 1; i >= 0; i-- {
			if _, err := conn.ExecContext(context.Background(), cleanup[i]); err != nil {
				t.Error(cleanup[i], err)
			}
		}
	}()
	deparseIdentityDefinitions(t, conn, bootstrap, expectedType, tc, exec, text, &cleanup)
	// Inquiry and preparation are symbolic: these rows remain empty and authority unchanged.
	rows(false, "SELECT CURRENT_USER AS u", []string{"u"}, []string{"NAME"}, [][]any{{bootstrap}})
	deparseIdentityFrames(t, conn, bootstrap, expectedType, tc, value, exec, rows, dml, violation)
}

func deparseIdentityRelation(t *testing.T, conn *sql.Conn, name string) int64 {
	t.Helper()
	var oid int64
	if err := conn.QueryRowContext(context.Background(), "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", name).Scan(&oid); err != nil || oid <= 0 {
		t.Fatal(name, oid, err)
	}
	return oid
}

func deparseIdentityDetail(value any) string {
	if value == nil {
		return "null"
	}
	return value.(string)
}

func deparseIdentityDefinitions(t *testing.T, conn *sql.Conn, bootstrap, expectedType string, tc deparseIdentityCase, exec func(string, int64), text func(string, string, ...any) string, cleanup *[]string) {
	exec("CREATE ROLE dc_identity_role", 0)
	*cleanup = append(*cleanup, "DROP ROLE dc_identity_role")
	*cleanup = append(*cleanup, "ALTER SCHEMA public OWNER TO "+bootstrap)
	exec("ALTER SCHEMA public OWNER TO dc_identity_role", 0)
	for _, name := range []string{"dc_identity_path_a", "dc_identity_path_b"} {
		*cleanup = append(*cleanup, "DROP SCHEMA IF EXISTS "+name+" CASCADE")
		exec("CREATE SCHEMA "+name+" AUTHORIZATION dc_identity_role", 0)
	}
	exec("SET search_path TO public", 0)
	exec("SET ROLE dc_identity_role", 0)
	*cleanup = append(*cleanup, "DROP TABLE IF EXISTS public.original")
	exec("CREATE TABLE public.original(id INT,s TEXT DEFAULT "+tc.input+",expected "+expectedType+",CONSTRAINT c CHECK("+tc.input+" IS NOT DISTINCT FROM expected))", 0)
	*cleanup = append(*cleanup, "DROP TABLE IF EXISTS public.null_original")
	exec("CREATE TABLE public.null_original(id INT,CONSTRAINT n CHECK("+tc.input+" IS NULL))", 0)
	exec("RESET ROLE", 0)
	originalRelation := deparseIdentityRelation(t, conn, "original")
	nullRelation := deparseIdentityRelation(t, conn, "null_original")
	for mode, pretty := range []bool{false, true} {
		defaultSQL := text(fmt.Sprintf("SELECT pg_get_expr(adbin,adrelid,%t) AS d FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2", pretty), tc.leaf, originalRelation)
		checkExpr := "(NOT (" + tc.leaf + " IS DISTINCT FROM expected))"
		nullExpr := "(" + tc.leaf + " IS NULL)"
		if pretty {
			checkExpr = "NOT " + tc.leaf + " IS DISTINCT FROM expected"
			nullExpr = tc.leaf + " IS NULL"
		}
		suffix := text(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='c'", pretty), "CHECK ("+checkExpr+")", originalRelation)
		text(fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) AS d FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='c'", pretty), checkExpr, originalRelation)
		nullSuffix := text(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='n'", pretty), "CHECK ("+nullExpr+")", nullRelation)
		text(fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) AS d FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='n'", pretty), nullExpr, nullRelation)
		name := fmt.Sprintf("copy%d", mode)
		exec("SET ROLE dc_identity_role", 0)
		*cleanup = append(*cleanup, "DROP TABLE IF EXISTS public."+name)
		exec("CREATE TABLE public."+name+"(id INT,s TEXT DEFAULT "+defaultSQL+",expected "+expectedType+",CONSTRAINT c "+suffix+")", 0)
		nullName := fmt.Sprintf("null_copy%d", mode)
		*cleanup = append(*cleanup, "DROP TABLE IF EXISTS public."+nullName)
		exec("CREATE TABLE public."+nullName+"(id INT,CONSTRAINT n "+nullSuffix+")", 0)
		exec("RESET ROLE", 0)
	}
}

func deparseIdentityFrames(t *testing.T, conn *sql.Conn, bootstrap, expectedType string, tc deparseIdentityCase, value func(string, string, any) any, exec func(string, int64), rows func(bool, string, []string, []string, [][]any), dml func(bool, string, ...any) (sql.Result, error), violation func(error, string, string, string)) {
	detailValue := deparseIdentityDetail
	for _, name := range []string{"original", "copy0", "copy1"} {
		relation := deparseIdentityRelation(t, conn, name)
		held, err := conn.PrepareContext(context.Background(), "INSERT INTO public."+name+"(id,expected) VALUES($1,$2)")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		update, err := conn.PrepareContext(context.Background(), "UPDATE public."+name+" SET expected=$1 WHERE id=$2")
		if err != nil {
			t.Fatal(err)
		}
		defer update.Close()
		inquiry, err := conn.PrepareContext(context.Background(), "SELECT pg_get_expr(adbin,adrelid,true) FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=2")
		if err != nil {
			t.Fatal(err)
		}
		defer inquiry.Close()
		for _, prepared := range []bool{false, true} {
			baseline := value(bootstrap, bootstrap, "public")
			var retained [][]any
			var nullRetained [3][][]any
			for frame, state := range []struct {
				commands         []string
				current, session string
				path             any
			}{
				{nil, bootstrap, bootstrap, "public"},
				{[]string{"SET ROLE dc_identity_role"}, "dc_identity_role", bootstrap, "public"},
				{[]string{"RESET ROLE", "SET SESSION AUTHORIZATION dc_identity_role"}, "dc_identity_role", "dc_identity_role", "public"},
				{[]string{"RESET SESSION AUTHORIZATION", "SET search_path TO dc_identity_path_a,dc_identity_path_b"}, bootstrap, bootstrap, "dc_identity_path_a"},
				{[]string{"SET search_path TO dc_identity_path_b,dc_identity_path_a"}, bootstrap, bootstrap, "dc_identity_path_b"},
				{[]string{"SET search_path TO dc_identity_missing"}, bootstrap, bootstrap, nil},
				{[]string{"SET search_path TO public"}, bootstrap, bootstrap, "public"},
			} {
				for _, q := range state.commands {
					exec(q, 0)
				}
				x := value(state.current, state.session, state.path)
				rows(false, "SELECT "+tc.input+" AS v", []string{"v"}, []string{expectedType}, [][]any{{x}})
				rows(true, "SELECT "+tc.input+" AS v", []string{"v"}, []string{expectedType}, [][]any{{x}})
				var symbolic string
				if err := inquiry.QueryRowContext(context.Background(), relation).Scan(&symbolic); err != nil || symbolic != tc.leaf {
					t.Fatal(symbolic, tc.leaf, err)
				}
				insert := func(id int, v any) (sql.Result, error) {
					if prepared {
						return held.ExecContext(context.Background(), id, v)
					}
					return conn.ExecContext(context.Background(), "INSERT INTO public."+name+"(id,expected) VALUES($1,$2)", id, v)
				}
				change := func(v any) (sql.Result, error) {
					if prepared {
						return update.ExecContext(context.Background(), v, 1)
					}
					return conn.ExecContext(context.Background(), "UPDATE public."+name+" SET expected=$1 WHERE id=$2", v, 1)
				}
				if frame == 0 {
					for _, id := range []int{1, 2} {
						r, e := insert(id, x)
						if e != nil {
							t.Fatal(e)
						}
						if n, _ := r.RowsAffected(); n != 1 {
							t.Fatal(n)
						}
						retained = append(retained, []any{int64(id), x, x})
					}
				} else {
					if _, err := change(x); err != nil {
						t.Fatal(err)
					}
					retained[0][2] = x
					id := 20 + frame
					if _, err := insert(id, x); err != nil {
						t.Fatal(err)
					}
					retained = append(retained, []any{int64(id), x, x})
				}
				snapshot := func() {
					for _, p := range []bool{false, true} {
						rows(p, "SELECT id,s,expected FROM public."+name+" ORDER BY id", []string{"id", "s", "expected"}, []string{"INT4", "TEXT", expectedType}, retained)
					}
				}
				snapshot()
				_, err = insert(90, "wrong_identity")
				violation(err, name, "c", "90, "+detailValue(x)+", wrong_identity")
				snapshot()
				_, err = dml(prepared, "INSERT INTO public."+name+"(id,expected) VALUES($1,$2),($3,$4),($5,$6)", 80, x, 90, "wrong_identity", 100, x)
				violation(err, name, "c", "90, "+detailValue(x)+", wrong_identity")
				snapshot()
				_, err = change("wrong_identity")
				violation(err, name, "c", "1, "+detailValue(baseline)+", wrong_identity")
				snapshot()
				if x != nil {
					_, err = insert(91, nil)
					violation(err, name, "c", "91, "+detailValue(x)+", null")
					snapshot()
				}
				for j, nullName := range []string{"null_original", "null_copy0", "null_copy1"} {
					id := frame + 1
					_, err = dml(prepared, "INSERT INTO public."+nullName+" VALUES($1)", id)
					if x == nil {
						if err != nil {
							t.Fatal(err)
						}
						nullRetained[j] = append(nullRetained[j], []any{int64(id)})
					} else {
						violation(err, nullName, "n", strconv.Itoa(id))
					}
					rows(prepared, "SELECT id FROM public."+nullName+" ORDER BY id", []string{"id"}, []string{"INT4"}, nullRetained[j])
				}
			}
			exec("DELETE FROM public."+name, int64(len(retained)))
			for j, nullName := range []string{"null_original", "null_copy0", "null_copy1"} {
				exec("DELETE FROM public."+nullName, int64(len(nullRetained[j])))
			}
		}
	}
}

// NAME-valued catalog inquiries expose a 63-byte, UTF8-safe display, not the
// full native DSN routing key. Derive this expectation from the fixture input.
func deparseIdentityCatalogDisplay(database string) string {
	if len(database) <= 63 {
		return database
	}
	end := 63
	for !utf8.RuneStart(database[end]) {
		end--
	}
	return database[:end]
}
