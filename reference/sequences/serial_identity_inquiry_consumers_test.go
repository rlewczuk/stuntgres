package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSerialIdentityConsumers(t *testing.T) {
	serialIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		serialIdentityRun(t, p, b, c,
			fixture.Exec(`CREATE TABLE si_consumer(id SERIAL,plain INT)`, 0),
			fixture.Exec(`CREATE TABLE si_inputs(n INT,col TEXT)`, 0),
			fixture.Exec(`INSERT INTO si_inputs VALUES(1,'id'),(2,'plain')`, 2),
			fixture.Select(`SELECT n,pg_get_serial_sequence('si_consumer',col) AS result FROM si_inputs ORDER BY n`, []string{"n", "result"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "public.si_consumer_id_seq"}, {int64(2), nil}}),
			fixture.Select(`SELECT n FROM si_inputs WHERE pg_get_serial_sequence('si_consumer',col) IS NOT NULL ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}),
			fixture.Select(`SELECT CASE WHEN pg_get_serial_sequence('si_consumer','plain') IS NULL THEN pg_get_serial_sequence('si_consumer','id') ELSE 'bad' END AS result,COALESCE(pg_get_serial_sequence('si_consumer','plain'),'none') AS fallback`, []string{"result", "fallback"}, []string{"TEXT", "TEXT"}, [][]any{{"public.si_consumer_id_seq", "none"}}),
			fixture.Select(`SELECT pg_get_serial_sequence('si_consumer',col) AS result FROM si_inputs ORDER BY result NULLS LAST`, []string{"result"}, []string{"TEXT"}, [][]any{{"public.si_consumer_id_seq"}, {nil}}),
			fixture.Exec(`CREATE VIEW si_symbolic AS SELECT pg_get_serial_sequence('si_consumer','id') AS result`, 0),
			fixture.Exec(`CREATE TABLE si_defaults(n INT,result TEXT DEFAULT pg_get_serial_sequence('si_consumer','id'))`, 0),
			fixture.Exec(`CREATE TABLE si_checked(col TEXT CONSTRAINT si_has_owner CHECK(pg_get_serial_sequence('si_consumer',col) IS NOT NULL))`, 0),
			fixture.Exec(`INSERT INTO si_defaults(n) VALUES(1)`, 1),
			fixture.Exec(`INSERT INTO si_checked VALUES('id')`, 1),
			fixture.Select(`SELECT result FROM si_symbolic`, []string{"result"}, []string{"TEXT"}, [][]any{{"public.si_consumer_id_seq"}}))
		w := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "si_checked" violates check constraint "si_has_owner"`)
		w.Detail, w.Schema, w.Table, w.Constraint = "Failing row contains (plain).", "public", "si_checked", "si_has_owner"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(`INSERT INTO si_checked VALUES('plain')`, w)
			s.Prepared, s.ErrorStage = prepared, "execute"
			serialIdentityRun(t, p, b, c, s)
		}
		serialIdentityRun(t, p, b, c,
			fixture.Exec(`ALTER SEQUENCE si_consumer_id_seq OWNED BY NONE`, 0),
			fixture.Select(`SELECT result FROM si_symbolic`, []string{"result"}, []string{"TEXT"}, [][]any{{nil}}),
			fixture.Exec(`INSERT INTO si_defaults(n) VALUES(2)`, 1),
			fixture.Select(`SELECT n,result FROM si_defaults ORDER BY n`, []string{"n", "result"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "public.si_consumer_id_seq"}, {int64(2), nil}}),
			fixture.Select(`SELECT col FROM si_checked`, []string{"col"}, []string{"TEXT"}, [][]any{{"id"}}))
		w.Detail = "Failing row contains (id)."
		s := fixture.ErrorDiagnostic(`INSERT INTO si_checked VALUES('id')`, w)
		s.ErrorStage = "execute"
		serialIdentityRun(t, p, b, c, s,
			fixture.Select(`SELECT col FROM si_checked`, []string{"col"}, []string{"TEXT"}, [][]any{{"id"}}))
		// The existing cancellation convention uses server admission on pq,
		// which can expose a cursor on a precanceled fast query.
		if b.PG {
			fixture.SessionCancel(t, p.Ctx, c, false)
		} else {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(ctx, `SELECT pg_get_serial_sequence('si_consumer','id')`)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled inquiry exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		serialIdentityRun(t, p, b, c,
			serialIdentityText(`SELECT pg_get_serial_sequence('si_consumer','id')`, nil, b.PG),
			fixture.Select(`SELECT last_value,is_called FROM si_consumer_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}),
			fixture.Select(`SELECT nextval('si_consumer_id_seq')`, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}
