//go:build reference

package reference_test

import "testing"

// SQL GRANT is oracle fixture setup, not a new native GRANT capability.
func TestReferenceSerialSequenceAuthority(t *testing.T) {
	roleReferenceCleanup(t,[]string{"serial_writer"})
	p := newReferencePair(t)
	conn,err := p.postgres.Conn(p.ctx)
	if err != nil { t.Fatal(err) }; defer conn.Close()
	run := func(s referenceStep) { referenceRunStep(t,p.ctx,conn,p.postgresRecord,s) }
	run(referenceExec(`CREATE USER serial_writer`,0))
	run(referenceExec(`CREATE TABLE public.t(id SERIAL)`,0))
	run(referenceExec(`GRANT USAGE ON SCHEMA public TO serial_writer`,0))
	run(referenceExec(`GRANT INSERT ON public.t TO serial_writer`,0))
	run(referenceExec(`SET ROLE serial_writer`,0))
	run(referencePrepared(referenceImplicitError(`INSERT INTO public.t DEFAULT VALUES`,referenceDiagnostic{Code:"42501",Message:`permission denied for sequence t_id_seq`})))
	run(referenceExec(`RESET ROLE`,0))
	run(referenceSelect(`SELECT last_value,is_called FROM public.t_id_seq`,[]string{"last_value","is_called"},[]string{"INT8","BOOL"},[][]any{{int64(1),false}}))
	run(referenceExec(`GRANT USAGE ON SEQUENCE public.t_id_seq TO serial_writer`,0))
	run(referenceExec(`SET ROLE serial_writer`,0))
	run(referencePrepared(referenceExec(`INSERT INTO public.t DEFAULT VALUES`,1)))
	run(referenceImplicitError(`SELECT setval('public.t_id_seq',9)`,referenceDiagnostic{Code:"42501",Message:`permission denied for sequence t_id_seq`}))
	run(referenceExec(`RESET ROLE`,0))
	run(referenceSelect(`SELECT * FROM public.t`,[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))
}
