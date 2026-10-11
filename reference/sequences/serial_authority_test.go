package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSerialSequenceAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"serial_writer"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		run(fixture.Exec(`CREATE USER serial_writer`, 0))
		run(fixture.Exec(`CREATE TABLE public.t(id SERIAL)`, 0))
		if backend.PG {
			run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO serial_writer`, 0))
		}
		run(fixture.Exec(`GRANT INSERT ON public.t TO serial_writer`, 0))
		run(fixture.Exec(`SET ROLE serial_writer`, 0))
		run(fixture.Prepared(fixture.ImplicitError(`INSERT INTO public.t DEFAULT VALUES`, fixture.Diagnostic{Code: "42501", Message: `permission denied for sequence t_id_seq`})))
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Select(`SELECT last_value,is_called FROM public.t_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		if backend.PG {
			run(fixture.Exec(`GRANT USAGE ON SEQUENCE public.t_id_seq TO serial_writer`, 0))
			run(fixture.Exec(`SET ROLE serial_writer`, 0))
			run(fixture.Prepared(fixture.Exec(`INSERT INTO public.t DEFAULT VALUES`, 1)))
			run(fixture.ImplicitError(`SELECT setval('public.t_id_seq',9)`, fixture.Diagnostic{Code: "42501", Message: `permission denied for sequence t_id_seq`}))
			run(fixture.Exec(`RESET ROLE`, 0))
			run(fixture.Select(`SELECT * FROM public.t`, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
	}
}
