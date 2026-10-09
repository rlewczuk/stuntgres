package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferenceDeparseConstraintsReviewProviderVisibility(t *testing.T) {
	schemaReferenceCleanup(t, "shadow")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) referenceObserved { return referenceRunStep(t, p.ctx, conn, b.recorder, s) }
			exec := func(q string) {
				n := int64(0)
				if strings.HasPrefix(q, "INSERT ") {
					n = 1
				}
				run(referenceExec(q, n))
			}
			text := func(q, want string) {
				s := referenceSelect(q, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
				run(s)
				run(referencePrepared(s))
			}
			number := func(q string, want int64) { run(referenceSelect(q, []string{"n"}, []string{"INT8"}, [][]any{{want}})) }
			exec("CREATE SCHEMA shadow")
			exec("CREATE SEQUENCE public.s START 40")
			exec("CREATE SEQUENCE shadow.s START 900")
			exec("CREATE TABLE original(a INT,CONSTRAINT c CHECK(nextval('public.s')<100))")
			exec("CREATE TABLE visible(a INT,CONSTRAINT v CHECK(nextval('shadow.s')>0))")
			stmt, err := conn.PrepareContext(p.ctx, "SELECT pg_get_constraintdef(oid,$1) FROM pg_catalog.pg_constraint WHERE conname='c'")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := func(pretty bool, want string) string {
				t.Helper()
				var got string
				if err := stmt.QueryRowContext(p.ctx, pretty).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Fatalf("held prepared got %q want %q", got, want)
				}
				return got
			}
			exec("SET search_path TO shadow,public")
			var saved string
			for i := 0; i < 2; i++ {
				want := "CHECK ((nextval('public.s'::regclass) < 100))"
				if i == 1 {
					want = "CHECK (nextval('public.s'::regclass) < 100)"
				}
				saved = held(i == 1, want)
				text(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", i == 1), want)
				visible := "CHECK ((nextval('s'::regclass) > 0))"
				if i == 1 {
					visible = "CHECK (nextval('s'::regclass) > 0)"
				}
				text(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='v'", i == 1), visible)
				exec(fmt.Sprintf("CREATE TABLE copy%d(a INT,CONSTRAINT copied %s)", i, saved))
			}
			number("SELECT nextval('public.s') AS n", 40)
			number("SELECT nextval('shadow.s') AS n", 900)
			exec("INSERT INTO original VALUES(1)")
			exec("INSERT INTO copy0 VALUES(1)")
			exec("INSERT INTO copy1 VALUES(1)")
			number("SELECT currval('public.s') AS n", 43)
			number("SELECT currval('shadow.s') AS n", 900)
			exec("SET search_path TO public,shadow")
			held(true, "CHECK (nextval('s'::regclass) < 100)")
			text("SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='v'", "CHECK (nextval('shadow.s'::regclass) > 0)")
			exec("SET search_path TO shadow,public")
			exec(`ALTER SEQUENCE public.s RENAME TO "Renamed sequence"`)
			held(true, `CHECK (nextval('"Renamed sequence"'::regclass) < 100)`)
			if saved != "CHECK (nextval('public.s'::regclass) < 100)" {
				t.Fatal("held text mutated", saved)
			}
			exec(`CREATE TABLE shadow."Renamed sequence"(a INT)`)
			suffix := held(true, `CHECK (nextval('public."Renamed sequence"'::regclass) < 100)`)
			exec("CREATE TABLE renamed_copy(a INT,CONSTRAINT renamed " + suffix + ")")
			exec("INSERT INTO renamed_copy VALUES(1)")
			number(`SELECT currval('public."Renamed sequence"') AS n`, 44)
			number("SELECT currval('shadow.s') AS n", 900)
		})
	}
}
