package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceFixtureNativeIsolation(t *testing.T) {
	// Equal test names must not retain an earlier pilot's process-local engine.
	first := newReferenceFixture(t, false, true)
	first.step(t, referenceExec("CREATE TABLE isolated(v INT)", 0))
	first.step(t, referenceExec("INSERT INTO isolated VALUES (7)", 1))
	var value int64
	if err := first.native.QueryRowContext(first.ctx, "SELECT v FROM isolated").Scan(&value); err != nil || value != 7 {
		t.Fatalf("native control pool shares wrapped state: value=%d error=%v", value, err)
	}
	second := newReferenceFixture(t, false, true)
	second.step(t, referenceExec("CREATE TABLE isolated(v INT)", 0))
	second.step(t, referenceSelect("SELECT v FROM isolated", []string{"v"}, []string{"INT4"}, nil))
	first.step(t, referenceSelect("SELECT v FROM isolated", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func TestReferenceFixtureResetIsolation(t *testing.T) {
	t.Run("contaminate", func(t *testing.T) {
		p := newReferenceScenario(t)
		for _, b := range p.backends() {
			t.Run(b.name, func(t *testing.T) {
				c, err := b.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				for _, query := range []string{
					"SET SESSION AUTHORIZATION postgres",
					"SET application_name='fixture-contaminated'",
					"CREATE TABLE public.fixture_reset_probe(v INT)",
				} {
					referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(query, 0))
				}
				if b.pg {
					// A replay can return an admin session with the wrong
					// principal. Boundary sanitation must restore its original
					// authenticator before recreating the public schema.
					admin, err := referenceAdmin.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer admin.Close()
					if _, err := admin.ExecContext(p.ctx, "SET SESSION AUTHORIZATION postgres"); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	})
	t.Run("next_case", func(t *testing.T) {
		p := newReferenceScenario(t)
		for _, b := range p.backends() {
			t.Run(b.name, func(t *testing.T) {
				principal := "postgres"
				if b.pg {
					principal = "stuntgres"
				}
				run := func(step referenceStep) {
					referenceRunStep(t, p.ctx, b.db, b.recorder, step)
				}
				run(referenceSelect("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{principal, principal}}))
				run(referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
				if b.pg {
					// pg_namespace is an oracle surface, not a native catalog.
					run(referenceSelect("SELECT pg_get_userbyid(nspowner) AS owner FROM pg_namespace WHERE nspname='public'", []string{"owner"}, []string{"NAME"}, [][]any{{principal}}))
				} else {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
						t.Fatal(err)
					}
					if dump.Len() != 0 {
						t.Fatalf("fresh native fixture retained catalog state: %s", dump.String())
					}
				}
				// Successful same-name creation and the empty result independently
				// establish that no previous fixture table or data was retained.
				run(referenceExec("CREATE TABLE public.fixture_reset_probe(v INT)", 0))
				run(referenceSelect("SELECT v FROM public.fixture_reset_probe", []string{"v"}, []string{"INT4"}, nil))
				if !b.pg {
					// Native host control independently clears actual catalog/data
					// without PostgreSQL-only namespace introspection.
					run(referenceExec("INSERT INTO public.fixture_reset_probe VALUES(7)", 1))
					if err := stuntgres.Reset(p.ctx, p.native); err != nil {
						t.Fatal(err)
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
						t.Fatal(err)
					}
					if dump.Len() != 0 {
						t.Fatalf("native reset retained catalog state: %s", dump.String())
					}
					run(referenceSelect("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{principal, principal}}))
					run(referenceExec("CREATE TABLE public.fixture_reset_probe(v INT)", 0))
					run(referenceSelect("SELECT v FROM public.fixture_reset_probe", []string{"v"}, []string{"INT4"}, nil))
				}
			})
		}
	})
}
