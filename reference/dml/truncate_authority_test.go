package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateAuthorization(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateExec(t, f, f.A, "CREATE ROLE truncate_reader", 0)
		defer func() {
			f.A.ExecContext(p.Ctx, "RESET ROLE")
			f.A.ExecContext(p.Ctx, "DROP OWNED BY truncate_reader")
			f.A.ExecContext(p.Ctx, "DROP ROLE truncate_reader")
		}()
		if b.PG {
			truncateExec(t, f, f.A, "GRANT USAGE ON SCHEMA public TO truncate_reader", 0)
		}
		truncateExec(t, f, f.A, "CREATE TABLE p(id INT PRIMARY KEY)", 0)
		truncateExec(t, f, f.A, "CREATE TABLE c(id INT REFERENCES p)", 0)
		truncateExec(t, f, f.A, "CREATE TABLE plain(id INT)", 0)
		truncateExec(t, f, f.A, "INSERT INTO p VALUES(1)", 1)
		truncateExec(t, f, f.A, "INSERT INTO c VALUES(1)", 1)
		truncateExec(t, f, f.A, "INSERT INTO plain VALUES(7)", 1)
		for _, right := range []string{"SELECT", "DELETE", "MAINTAIN", "SELECT(id)"} {
			truncateExec(t, f, f.A, "GRANT "+right+" ON plain TO truncate_reader", 0)
			truncateExec(t, f, f.A, "SET ROLE truncate_reader", 0)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("TRUNCATE plain", truncateWant("42501", "permission denied for table plain"))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				truncateRun(t, f, f.A, s)
			}
			truncateExec(t, f, f.A, "RESET ROLE", 0)
			truncateExec(t, f, f.A, "REVOKE "+right+" ON plain FROM truncate_reader", 0)
		}
		stmt, err := f.A.PrepareContext(p.Ctx, "TRUNCATE plain")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		truncateExec(t, f, f.A, "GRANT TRUNCATE ON plain TO truncate_reader", 0)
		truncateExec(t, f, f.A, "SET ROLE truncate_reader", 0)
		result, err := stmt.ExecContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		n, err := result.RowsAffected()
		if err != nil || n != 0 {
			t.Fatalf("truncate count %d: %v", n, err)
		}
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateRows(t, f, f.A, "plain")
		truncateExec(t, f, f.A, "REVOKE TRUNCATE ON plain FROM truncate_reader", 0)
		truncateExec(t, f, f.A, "SET ROLE truncate_reader", 0)
		_, err = stmt.ExecContext(p.Ctx)
		d := truncateWant("42501", "permission denied for table plain")
		fixture.AssertDiagnostic(t, err, &d)
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateExec(t, f, f.A, "GRANT TRUNCATE ON p TO truncate_reader", 0)
		truncateExec(t, f, f.A, "SET ROLE truncate_reader", 0)
		truncateError(t, f, f.A, "TRUNCATE p CASCADE", truncateWant("42501", "permission denied for table c"))
		truncateNotices(t, f, "c")
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateRows(t, f, f.A, "p", 1)
		truncateRows(t, f, f.A, "c", 1)
		truncateExec(t, f, f.A, "GRANT TRUNCATE ON c TO truncate_reader", 0)
		truncateExec(t, f, f.A, "SET ROLE truncate_reader", 0)
		truncateExec(t, f, f.A, "TRUNCATE p CASCADE", 0)
		truncateNotices(t, f, "c")
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		for _, q := range []string{"CREATE TABLE parent(id INT)", "CREATE TABLE child() INHERITS(parent)", "GRANT TRUNCATE ON parent TO truncate_reader"} {
			truncateExec(t, f, f.A, q, 0)
		}
		truncateExec(t, f, f.A, "INSERT INTO child VALUES(3)", 1)
		truncateExec(t, f, f.A, "SET ROLE truncate_reader", 0)
		truncateError(t, f, f.A, "TRUNCATE parent,child", truncateWant("42501", "permission denied for table child"))
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateRows(t, f, f.A, "child", 3)
		truncateExec(t, f, f.A, "SET ROLE truncate_reader", 0)
		truncateExec(t, f, f.A, "TRUNCATE parent", 0)
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateRows(t, f, f.A, "child")
		// Revoked owner use is not bypassed by grant authority. Bootstrap superuser
		// is the positive control, not evidence that owners have irrevocable use.
		if b.PG {
			truncateExec(t, f, f.A, "GRANT CREATE ON SCHEMA public TO truncate_reader", 0)
		}
		if !b.PG {
			truncateExec(t, f, f.A, "CREATE SCHEMA truncate_reader AUTHORIZATION truncate_reader", 0)
		}
		truncateExec(t, f, f.A, "SET ROLE truncate_reader", 0)
		ownerTable := "owned"
		if !b.PG {
			ownerTable = "truncate_reader.owned"
		}
		truncateExec(t, f, f.A, "CREATE TABLE "+ownerTable+"(id INT)", 0)
		truncateExec(t, f, f.A, "REVOKE TRUNCATE ON "+ownerTable+" FROM truncate_reader", 0)
		truncateError(t, f, f.A, "TRUNCATE "+ownerTable, truncateWant("42501", "permission denied for table owned"))
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateExec(t, f, f.A, "TRUNCATE "+ownerTable, 0)
	})
}
func TestReferenceTruncateIdentity(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		for _, q := range []string{"CREATE TABLE serial_t(id SERIAL)", "CREATE TABLE identity_t(id INT GENERATED ALWAYS AS IDENTITY(START WITH 44))", "CREATE SEQUENCE unrelated START WITH 80 CACHE 5", "CREATE SEQUENCE uncalled START WITH 100 CACHE 4"} {
			truncateExec(t, f, f.A, q, 0)
		}
		truncateExec(t, f, f.A, "ALTER SEQUENCE serial_t_id_seq CACHE 5", 0)
		truncateExec(t, f, f.A, "INSERT INTO serial_t DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO serial_t DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO identity_t DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO identity_t DEFAULT VALUES", 1)
		scalar := func(x fixture.Executor, q, label string, v int64) {
			truncateRun(t, f, x, fixture.Select(q, []string{label}, []string{"INT8"}, [][]any{{v}}))
		}
		scalar(f.A, "SELECT nextval('unrelated')", "nextval", 80)
		scalar(f.A, "SELECT currval('serial_t_id_seq')", "currval", 2)
		scalar(f.A, "SELECT currval('identity_t_id_seq')", "currval", 45)
		scalar(f.A, "SELECT lastval()", "lastval", 80)
		state := func(x fixture.Executor) {
			truncateRun(t, f, x, fixture.Select("SELECT last_value,is_called FROM uncalled", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(100), false}}))
			truncateRun(t, f, x, fixture.Select("SELECT last_value FROM pg_sequences WHERE schemaname='public' AND sequencename='serial_t_id_seq'", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(5)}}))
		}
		state(f.A)
		tx := truncateBegin(t, p, f.A, nil)
		truncateExec(t, f, tx, "TRUNCATE serial_t,identity_t CONTINUE IDENTITY", 0)
		scalar(tx, "SELECT currval('serial_t_id_seq')", "currval", 2)
		scalar(tx, "SELECT currval('identity_t_id_seq')", "currval", 45)
		scalar(tx, "SELECT lastval()", "lastval", 80)
		state(tx)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		truncateRows(t, f, f.A, "serial_t", 1, 2)
		truncateRows(t, f, f.A, "identity_t", 44, 45)
		truncateExec(t, f, f.A, "TRUNCATE serial_t,identity_t", 0)
		state(f.A)
		truncateExec(t, f, f.A, "INSERT INTO serial_t DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO identity_t DEFAULT VALUES", 1)
		truncateRows(t, f, f.A, "serial_t", 3)
		truncateRows(t, f, f.A, "identity_t", 46)
		scalar(f.A, "SELECT nextval('unrelated')", "nextval", 81)
		scalar(f.A, "SELECT nextval('uncalled')", "nextval", 100)
		// Public definition/high-water controls prove truncate did not reset an
		// uncalled generator or discard a physical-session cache tail.
		truncateRun(t, f, f.A, fixture.Select("SELECT last_value FROM pg_sequences WHERE schemaname='public' AND sequencename='unrelated'", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(84)}}))
		truncateExec(t, f, f.A, "CREATE ROLE truncate_no_sequence", 0)
		defer func() {
			f.A.ExecContext(p.Ctx, "RESET ROLE")
			f.A.ExecContext(p.Ctx, "DROP OWNED BY truncate_no_sequence")
			f.A.ExecContext(p.Ctx, "DROP ROLE truncate_no_sequence")
		}()
		if b.PG {
			truncateExec(t, f, f.A, "GRANT USAGE ON SCHEMA public TO truncate_no_sequence", 0)
		}
		truncateExec(t, f, f.A, "GRANT TRUNCATE ON serial_t,identity_t TO truncate_no_sequence", 0)
		truncateExec(t, f, f.A, "SET ROLE truncate_no_sequence", 0)
		truncateExec(t, f, f.A, "TRUNCATE serial_t,identity_t CONTINUE IDENTITY", 0)
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateExec(t, f, f.A, "INSERT INTO serial_t DEFAULT VALUES", 1)
		truncateRows(t, f, f.A, "serial_t", 4)
	})
}
func TestReferenceTruncateReadOnly(t *testing.T) {
	truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		truncateSeed(t, f)
		truncateExec(t, f, f.A, "CREATE TEMP TABLE temp_t(id INT)", 0)
		truncateExec(t, f, f.A, "INSERT INTO temp_t VALUES(8)", 1)
		for _, name := range []string{"p", "temp_t", "missing"} {
			tx := truncateBegin(t, p, f.A, &sql.TxOptions{ReadOnly: true})
			stmt, err := tx.PrepareContext(p.Ctx, "TRUNCATE "+name)
			if err != nil {
				t.Fatal(err)
			}
			_, err = stmt.ExecContext(p.Ctx)
			d := truncateWant("25006", "cannot execute TRUNCATE TABLE in a read-only transaction")
			fixture.AssertDiagnostic(t, err, &d)
			stmt.Close()
			tx.Rollback()
		}
		truncateRows(t, f, f.A, "p", 1, 2)
		truncateRows(t, f, f.A, "temp_t", 8)
		truncateExec(t, f, f.A, "CREATE ROLE truncate_readonly_denied", 0)
		defer func() {
			f.A.ExecContext(p.Ctx, "RESET ROLE")
			f.A.ExecContext(p.Ctx, "DROP ROLE truncate_readonly_denied")
		}()
		if b.PG {
			truncateExec(t, f, f.A, "GRANT USAGE ON SCHEMA public TO truncate_readonly_denied", 0)
			defer f.A.ExecContext(p.Ctx, "DROP OWNED BY truncate_readonly_denied")
		}
		truncateExec(t, f, f.A, "SET ROLE truncate_readonly_denied", 0)
		tx := truncateBegin(t, p, f.A, &sql.TxOptions{ReadOnly: true})
		truncateError(t, f, tx, "TRUNCATE p", truncateWant("25006", "cannot execute TRUNCATE TABLE in a read-only transaction"))
		tx.Rollback()
		truncateExec(t, f, f.A, "RESET ROLE", 0)
	})
}
