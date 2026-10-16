package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantSequences(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"og_seq_reader", "og_seq_delegate"})
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) {
						s.Prepared = prepared
						if !t.Run(s.Query, func(t *testing.T) {
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
						}) {
							t.FailNow()
						}
					}
					actor := func(role string, body func()) {
						run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
						body()
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					}
					denied := func(query, name string) {
						run(fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence " + name}))
					}
					value := func(query, column string, n int64) {
						run(fixture.Select(query, []string{column}, []string{"INT8"}, [][]any{{n}}))
					}

					run(fixture.Exec("CREATE ROLE og_seq_reader", 0))
					run(fixture.Exec("CREATE ROLE og_seq_delegate", 0))
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_seq_reader,og_seq_delegate", 0))
					run(fixture.Exec("CREATE SEQUENCE og_seq START 10", 0))
					// Ordinary sequences have no default PUBLIC use rights.
					actor("og_seq_reader", func() {
						denied("SELECT nextval('og_seq')", "og_seq")
						denied("SELECT last_value FROM og_seq", "og_seq")
					})
					run(fixture.Exec("GRANT USAGE ON SEQUENCE og_seq TO og_seq_reader", 0))
					actor("og_seq_reader", func() {
						value("SELECT nextval('og_seq')", "nextval", 10)
						value("SELECT nextval('og_seq')", "nextval", 11)
						value("SELECT currval('og_seq')", "currval", 11)
						value("SELECT lastval()", "lastval", 11)
						denied("SELECT setval('og_seq',20)", "og_seq")
						denied("SELECT last_value FROM og_seq", "og_seq")
					})

					// Hold the same consumer through permission changes. Session
					// currval memory is independent of ACL publication and role reset.
					query := "SELECT currval('og_seq')"
					stmt, err := conn.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
					run(fixture.Exec("REVOKE USAGE ON SEQUENCE og_seq FROM og_seq_reader", 0))
					run(fixture.Exec("GRANT SELECT ON SEQUENCE og_seq TO og_seq_reader", 0))
					actor("og_seq_reader", func() {
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(query, []string{"currval"}, []string{"INT8"}, [][]any{{int64(11)}}))
						value("SELECT lastval()", "lastval", 11)
						value("SELECT last_value FROM og_seq", "last_value", 11)
						denied("SELECT nextval('og_seq')", "og_seq")
						denied("SELECT setval('og_seq',20)", "og_seq")
					})
					run(fixture.Exec("REVOKE SELECT ON SEQUENCE og_seq FROM og_seq_reader", 0))
					run(fixture.Exec("GRANT UPDATE ON SEQUENCE og_seq TO og_seq_reader", 0))
					actor("og_seq_reader", func() {
						value("SELECT nextval('og_seq')", "nextval", 12)
						value("SELECT setval('og_seq',20)", "setval", 20)
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence og_seq"}))
						denied("SELECT lastval()", "og_seq")
						denied("SELECT last_value FROM og_seq", "og_seq")
					})
					run(fixture.Exec("GRANT ALL PRIVILEGES ON SEQUENCE og_seq TO og_seq_reader", 0))
					actor("og_seq_reader", func() {
						value("SELECT nextval('og_seq')", "nextval", 21)
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(query, []string{"currval"}, []string{"INT8"}, [][]any{{int64(21)}}))
						value("SELECT last_value FROM og_seq", "last_value", 21)
					})

					// Grant-option cascade removes delegated use, not generator state.
					run(fixture.Exec("GRANT USAGE ON SEQUENCE og_seq TO og_seq_reader WITH GRANT OPTION", 0))
					actor("og_seq_reader", func() {
						run(fixture.Exec("GRANT USAGE ON SEQUENCE og_seq TO og_seq_delegate", 0))
					})
					actor("og_seq_delegate", func() { value("SELECT nextval('og_seq')", "nextval", 22) })
					run(fixture.Exec("REVOKE GRANT OPTION FOR USAGE ON SEQUENCE og_seq FROM og_seq_reader CASCADE", 0))
					actor("og_seq_delegate", func() { denied("SELECT nextval('og_seq')", "og_seq") })
					actor("og_seq_reader", func() { value("SELECT nextval('og_seq')", "nextval", 23) })

					// Table INSERT is not sequence USAGE; identity generation has
					// PostgreSQL's separate implicit-generation authority.
					run(fixture.Exec("CREATE TABLE og_serial(id SERIAL,x INT)", 0))
					run(fixture.Exec("CREATE TABLE og_identity(id INT GENERATED ALWAYS AS IDENTITY,x INT)", 0))
					run(fixture.Exec("GRANT INSERT ON og_serial,og_identity TO og_seq_reader", 0))
					actor("og_seq_reader", func() {
						denied("INSERT INTO og_serial(x) VALUES(7)", "og_serial_id_seq")
						run(fixture.Exec("INSERT INTO og_identity(x) VALUES(7)", 1))
						denied("SELECT nextval('og_identity_id_seq')", "og_identity_id_seq")
					})
					run(fixture.Select("SELECT last_value,is_called FROM og_serial_id_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
					run(fixture.Select("SELECT id,x FROM og_serial", []string{"id", "x"}, []string{"INT4", "INT4"}, nil))
					run(fixture.Select("SELECT id,x FROM og_identity", []string{"id", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}}))
					run(fixture.Exec("GRANT USAGE ON SEQUENCE og_serial_id_seq TO og_seq_reader", 0))
					actor("og_seq_reader", func() { run(fixture.Exec("INSERT INTO og_serial(x) VALUES(9)", 1)) })
					run(fixture.Select("SELECT id,x FROM og_serial", []string{"id", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
				})
			}
		})
	}
}
