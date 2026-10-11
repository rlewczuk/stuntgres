package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func deparseIdentityNested(t *testing.T) {
	fixture.DeparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(fixture.Step) fixture.Observed) {
		bootstrap := "postgres"
		var system any
		if postgres {
			bootstrap = "stuntgres"
			system = "scram-sha-256:stuntgres"
		}
		exec := func(q string, n int64) { run(fixture.Exec(q, n)) }
		rows := func(p bool, q string, names, types []string, want [][]any) {
			step := fixture.Select(q, names, types, want)
			step.Prepared = p
			run(step)
		}
		text := func(q, want string, args ...any) string {
			step := fixture.Select(q, []string{"d"}, []string{"TEXT"}, [][]any{{want}}, args...)
			actual := run(step).Rows[0][0].(string)
			run(fixture.Prepared(step))
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
			want := fixture.CheckViolation(table, constraint, detail)
			want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
			fixture.AssertDiagnostic(t, err, &want)
		}

		exec("CREATE TABLE public.who_original(id INT,who NAME,plain TEXT DEFAULT CURRENT_USER,called TEXT DEFAULT pg_catalog.current_user(),fallback TEXT DEFAULT COALESCE(SYSTEM_USER,'trusted'),CONSTRAINT w CHECK(who=CURRENT_USER))", 0)
		exec("CREATE TABLE public.fallback_original(id INT,CONSTRAINT f CHECK(COALESCE(SYSTEM_USER,'trusted')='trusted'))", 0)
		whoRelation := deparseIdentityRelation(t, conn, "who_original")
		fallbackRelation := deparseIdentityRelation(t, conn, "fallback_original")
		fallback := system
		if fallback == nil {
			fallback = "trusted"
		}
		for mode, pretty := range []bool{false, true} {
			prefix := "(who = CURRENT_USER)"
			fallbackExpr := "(COALESCE(SYSTEM_USER, 'trusted'::text) = 'trusted'::text)"
			if pretty {
				prefix = "who = CURRENT_USER"
				fallbackExpr = "COALESCE(SYSTEM_USER, 'trusted'::text) = 'trusted'::text"
			}
			defaults := make([]string, 3)
			for j, want := range []string{"CURRENT_USER", `"current_user"()`, "COALESCE(SYSTEM_USER, 'trusted'::text)"} {
				defaults[j] = text(fmt.Sprintf("SELECT pg_get_expr(adbin,adrelid,%t) AS d FROM pg_catalog.pg_attrdef WHERE adrelid=$1 AND adnum=%d", pretty, j+3), want, whoRelation)
			}
			suffix := text(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='w'", pretty), "CHECK ("+prefix+")", whoRelation)
			text(fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) AS d FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='w'", pretty), prefix, whoRelation)
			exec(fmt.Sprintf("CREATE TABLE public.who_copy%d(id INT,who NAME,plain TEXT DEFAULT %s,called TEXT DEFAULT %s,fallback TEXT DEFAULT %s,CONSTRAINT w %s)", mode, defaults[0], defaults[1], defaults[2], suffix), 0)
			suffix = text(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='f'", pretty), "CHECK ("+fallbackExpr+")", fallbackRelation)
			text(fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) AS d FROM pg_catalog.pg_constraint WHERE conrelid=$1 AND conname='f'", pretty), fallbackExpr, fallbackRelation)
			exec(fmt.Sprintf("CREATE TABLE public.fallback_copy%d(id INT,CONSTRAINT f %s)", mode, suffix), 0)
		}
		for _, name := range []string{"who_original", "who_copy0", "who_copy1"} {
			for _, prepared := range []bool{false, true} {
				if _, err := dml(prepared, "INSERT INTO public."+name+"(id,who) VALUES($1,$2),($3,$4)", 1, bootstrap, 2, nil); err != nil {
					t.Fatal(err)
				}
				retained := [][]any{{int64(1), bootstrap, bootstrap, bootstrap, fallback}, {int64(2), nil, bootstrap, bootstrap, fallback}}
				snapshot := func() {
					rows(prepared, "SELECT id,who,plain,called,fallback FROM public."+name+" ORDER BY id", []string{"id", "who", "plain", "called", "fallback"}, []string{"INT4", "NAME", "TEXT", "TEXT", "TEXT"}, retained)
				}
				snapshot()
				detail := "90, wrong_identity, " + bootstrap + ", " + bootstrap + ", " + fallback.(string)
				_, err := dml(prepared, "INSERT INTO public."+name+"(id,who) VALUES($1,$2)", 90, "wrong_identity")
				violation(err, name, "w", detail)
				snapshot()
				_, err = dml(prepared, "INSERT INTO public."+name+"(id,who) VALUES($1,$2),($3,$4),($5,$6)", 80, bootstrap, 90, "wrong_identity", 100, nil)
				violation(err, name, "w", detail)
				snapshot()
				_, err = dml(prepared, "UPDATE public."+name+" SET who=$1 WHERE id=1", "wrong_identity")
				violation(err, name, "w", "1, wrong_identity, "+bootstrap+", "+bootstrap+", "+fallback.(string))
				snapshot()
				exec("DELETE FROM public."+name, 2)
			}
		}
		for _, name := range []string{"fallback_original", "fallback_copy0", "fallback_copy1"} {
			for _, prepared := range []bool{false, true} {
				_, err := dml(prepared, "INSERT INTO public."+name+" VALUES(1)")
				if system == nil {
					if err != nil {
						t.Fatal(err)
					}
					rows(prepared, "SELECT id FROM public."+name, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
					exec("DELETE FROM public."+name, 1)
				} else {
					violation(err, name, "f", "1")
					rows(prepared, "SELECT id FROM public."+name, []string{"id"}, []string{"INT4"}, [][]any{})
				}
			}
		}
	})
}
