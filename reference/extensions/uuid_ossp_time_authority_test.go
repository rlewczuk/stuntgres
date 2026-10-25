package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUUIDOsspTimeAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		label := "direct"
		if prepared {
			label = "fresh_prepared"
		}
		t.Run(label, func(t *testing.T) {
			uuidOsspCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, r, s) }
				actor := func(body func()) { fixture.RoleGrantActor(run, "uuid_ossp_owner", body) }
				run(fixture.Exec("CREATE USER uuid_ossp_owner", 0))
				run(fixture.Exec("CREATE SCHEMA uuid_ossp_part", 0))
				run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA uuid_ossp_part TO uuid_ossp_owner", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO uuid_ossp_owner", 0))
				var statements []*sql.Stmt
				defer func() {
					for _, s := range statements {
						s.Close()
					}
				}()
				hold := func(q string) fixture.HeldStatement {
					s, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					statements = append(statements, s)
					return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
				}
				for _, name := range uuidOsspTimeNames {
					mc := name == "uuid_generate_v1mc"
					signature := "uuid_ossp_part." + name + "()"
					q := "SELECT " + signature + " AS u"
					read := func(e fixture.Executor, query string, n int, nullable ...bool) {
						if prepared {
							s, err := c.PrepareContext(p.Ctx, query)
							if err != nil {
								t.Fatal(err)
							}
							defer s.Close()
							e = fixture.HeldStatement{Executor: c, Statement: s, SQL: query}
						}
						uuidOsspTimeRead(t, p, e, r, pg, query, n, 1, mc, nullable...)
					}
					actor(func() { read(c, q, 1) })
					// Keep consumers in public even after the schema-authority path switch.
					run(fixture.Exec("CREATE TABLE public."+name+"_acl_saved(id integer,u uuid DEFAULT "+signature+")", 0))
					run(fixture.Exec("INSERT INTO public."+name+"_acl_saved(id) VALUES(7)", 1))
					run(fixture.Exec("CREATE VIEW public."+name+"_acl_view AS SELECT id,"+signature+" AS u FROM public."+name+"_acl_saved", 0))
					run(fixture.Exec("GRANT SELECT,INSERT ON public."+name+"_acl_saved TO uuid_ossp_owner", 0))
					run(fixture.Exec("GRANT SELECT ON public."+name+"_acl_view TO uuid_ossp_owner", 0))
					view := "SELECT u FROM public." + name + "_acl_view ORDER BY id"
					insert := "INSERT INTO public." + name + "_acl_saved(id) VALUES(8),(9) RETURNING u AS u"
					var retained, viewHeld, insertHeld fixture.HeldStatement
					actor(func() { retained = hold(q); viewHeld = hold(view); insertHeld = hold(insert) })
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC", 0))
					denial := func(query string) fixture.Step {
						s := fixture.ImplicitError(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+name))
						s.SelectRows = true
						s.ErrorStage = "execute"
						return s
					}
					denied := func() {
						actor(func() {
							run(denial(q))
							fixture.RunStep(t, p.Ctx, retained, r, denial(q))
							run(denial(view))
							fixture.RunStep(t, p.Ctx, viewHeld, r, denial(view))
							run(denial(insert))
							fixture.RunStep(t, p.Ctx, insertHeld, r, denial(insert))
							run(denial("INSERT INTO public." + name + "_acl_saved(id,u) VALUES(8,'12345678-1234-1234-9234-123456789abc'),(9,DEFAULT) RETURNING u AS u"))
						})
						run(fixture.Select("SELECT id FROM public."+name+"_acl_saved ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
					denied()
					for range 2 {
						run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO uuid_ossp_owner", 0))
						actor(func() {
							read(c, q, 1)
							uuidOsspTimeRead(t, p, retained, r, pg, q, 1, 1, mc)
							uuidOsspTimeRead(t, p, viewHeld, r, pg, view, 1, 1, mc, true)
							uuidOsspTimeRead(t, p, insertHeld, r, pg, insert, 2, 1, mc, true)
						})
						run(fixture.Exec("DELETE FROM public."+name+"_acl_saved WHERE id IN (8,9)", 2))
						run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM uuid_ossp_owner", 0))
						denied()
					}
					// Schema authority is distinct from invocation authority.
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO uuid_ossp_owner", 0))
					run(fixture.Exec("REVOKE USAGE ON SCHEMA uuid_ossp_part FROM uuid_ossp_owner", 0))
					actor(func() {
						uuidOsspFailure(t, p, c, q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema uuid_ossp_part", Position: "8"})
						run(fixture.Exec("SET search_path TO uuid_ossp_part,public", 0))
						uuidOsspFailure(t, p, c, "SELECT "+name+"()", uuidOsspMissing(name+"()", 8))
					})
					run(fixture.Exec("GRANT USAGE ON SCHEMA uuid_ossp_part TO uuid_ossp_owner", 0))
					actor(func() { read(c, q, 1); uuidOsspTimeRead(t, p, retained, r, pg, q, 1, 1, mc) })
					// RoleGrantActor restored the administrative owner, which still works.
					read(c, q, 1)
				}
			})
		})
	}
}
