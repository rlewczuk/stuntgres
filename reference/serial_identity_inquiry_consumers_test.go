package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestReferenceSerialIdentityConsumers(t *testing.T) {
	serialIdentityScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		serialIdentityRun(t, p, b, c,
			referenceExec(`CREATE TABLE si_consumer(id SERIAL,plain INT)`, 0),
			referenceExec(`CREATE TABLE si_inputs(n INT,col TEXT)`, 0),
			referenceExec(`INSERT INTO si_inputs VALUES(1,'id'),(2,'plain')`, 2),
			referenceSelect(`SELECT n,pg_get_serial_sequence('si_consumer',col) AS result FROM si_inputs ORDER BY n`, []string{"n", "result"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "public.si_consumer_id_seq"}, {int64(2), nil}}),
			referenceSelect(`SELECT n FROM si_inputs WHERE pg_get_serial_sequence('si_consumer',col) IS NOT NULL ORDER BY n`, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}),
			referenceSelect(`SELECT CASE WHEN pg_get_serial_sequence('si_consumer','plain') IS NULL THEN pg_get_serial_sequence('si_consumer','id') ELSE 'bad' END AS result,COALESCE(pg_get_serial_sequence('si_consumer','plain'),'none') AS fallback`, []string{"result", "fallback"}, []string{"TEXT", "TEXT"}, [][]any{{"public.si_consumer_id_seq", "none"}}),
			referenceSelect(`SELECT pg_get_serial_sequence('si_consumer',col) AS result FROM si_inputs ORDER BY result NULLS LAST`, []string{"result"}, []string{"TEXT"}, [][]any{{"public.si_consumer_id_seq"}, {nil}}),
			referenceExec(`CREATE VIEW si_symbolic AS SELECT pg_get_serial_sequence('si_consumer','id') AS result`, 0),
			referenceExec(`CREATE TABLE si_defaults(n INT,result TEXT DEFAULT pg_get_serial_sequence('si_consumer','id'))`, 0),
			referenceExec(`CREATE TABLE si_checked(col TEXT CONSTRAINT si_has_owner CHECK(pg_get_serial_sequence('si_consumer',col) IS NOT NULL))`, 0),
			referenceExec(`INSERT INTO si_defaults(n) VALUES(1)`, 1),
			referenceExec(`INSERT INTO si_checked VALUES('id')`, 1),
			referenceSelect(`SELECT result FROM si_symbolic`, []string{"result"}, []string{"TEXT"}, [][]any{{"public.si_consumer_id_seq"}}))
		w := controlsReferenceDiagnostic("23514", `new row for relation "si_checked" violates check constraint "si_has_owner"`)
		w.Detail, w.Schema, w.Table, w.Constraint = "Failing row contains (plain).", "public", "si_checked", "si_has_owner"
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic(`INSERT INTO si_checked VALUES('plain')`, w)
			s.prepared, s.errorStage = prepared, "execute"
			serialIdentityRun(t, p, b, c, s)
		}
		serialIdentityRun(t, p, b, c,
			referenceExec(`ALTER SEQUENCE si_consumer_id_seq OWNED BY NONE`, 0),
			referenceSelect(`SELECT result FROM si_symbolic`, []string{"result"}, []string{"TEXT"}, [][]any{{nil}}),
			referenceExec(`INSERT INTO si_defaults(n) VALUES(2)`, 1),
			referenceSelect(`SELECT n,result FROM si_defaults ORDER BY n`, []string{"n", "result"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "public.si_consumer_id_seq"}, {int64(2), nil}}),
			referenceSelect(`SELECT col FROM si_checked`, []string{"col"}, []string{"TEXT"}, [][]any{{"id"}}))
		w.Detail = "Failing row contains (id)."
		s := referenceErrorDiagnostic(`INSERT INTO si_checked VALUES('id')`, w)
		s.errorStage = "execute"
		serialIdentityRun(t, p, b, c, s,
			referenceSelect(`SELECT col FROM si_checked`, []string{"col"}, []string{"TEXT"}, [][]any{{"id"}}))
		// The existing cancellation convention uses server admission on pq,
		// which can expose a cursor on a precanceled fast query.
		if b.pg {
			referenceSessionCancel(t, p.ctx, c, false)
		} else {
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := c.QueryContext(ctx, `SELECT pg_get_serial_sequence('si_consumer','id')`)
			if rows != nil { rows.Close(); t.Fatal("canceled inquiry exposed rows") }
			if !errors.Is(err, context.Canceled) { t.Fatal(err) }
		}
		serialIdentityRun(t, p, b, c,
			serialIdentityText(`SELECT pg_get_serial_sequence('si_consumer','id')`, nil, b.pg),
			referenceSelect(`SELECT last_value,is_called FROM si_consumer_id_seq`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}),
			referenceSelect(`SELECT nextval('si_consumer_id_seq')`, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}
