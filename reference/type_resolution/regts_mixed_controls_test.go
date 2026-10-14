package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRegTSMixedRoutinePersistence(t *testing.T) {
	var emitted []byte
	regtsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Discarded objects consume source OIDs but are absent from the dump.
		// Numeric dictionary fields must not accidentally pass by allocator parity.
		regtsExec(t, p, b, c, "CREATE TABLE public.reg_ts_mixed_gap(v integer)", 0)
		regtsExec(t, p, b, c, "DROP TABLE public.reg_ts_mixed_gap", 0)
		regtsInstall(t, p, b, c)
		const value = `'("abs(integer)","""Reg.TS"".unaccent")'::public.reg_ts_mixed_pair`
		for _, q := range []string{
			"CREATE TYPE public.reg_ts_mixed_pair AS (p regprocedure,d regdictionary)",
			"CREATE TABLE public.reg_ts_mixed_values(id integer,c public.reg_ts_mixed_pair DEFAULT " + value + ")",
			"CREATE VIEW public.reg_ts_mixed_view AS SELECT " + value + " AS c",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO public.reg_ts_mixed_values VALUES(0,"+value+")", 1)
		regtsExec(t, p, b, c, "INSERT INTO public.reg_ts_mixed_values(id) VALUES(1)", 1)
		regtsExec(t, p, b, c, "INSERT INTO public.reg_ts_mixed_values VALUES(2,NULL)", 1)
		regtsMixedPersistentCheck(t, p, b, c)

		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native mixed routine/dictionary dump required for PostgreSQL replay")
			}
			for _, q := range []string{
				"DROP VIEW public.reg_ts_mixed_view",
				"DROP TABLE public.reg_ts_mixed_values",
				"DROP TYPE public.reg_ts_mixed_pair",
				"DROP EXTENSION unaccent",
				`DROP SCHEMA "Reg.TS"`,
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("PostgreSQL rejected actual mixed routine/dictionary dump", err)
			}
			regtsMixedPersistentCheck(t, p, b, c)
			return
		}

		oldOID := regtsDictionaryOID(t, p, c)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Len() == 0 {
			t.Fatal("empty mixed routine/dictionary dump")
		}
		emitted = bytes.Clone(dump.Bytes())
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		// Use the established public TEMP-collision destination, not ordinary
		// objects that Restore discards or a hard-coded allocator sequence.
		var collider string
		for i := 0; collider == ""; i++ {
			name := fmt.Sprintf("reg_ts_mixed_displace_%d", i)
			regtsExec(t, fresh, fb, fc, "CREATE TEMP SEQUENCE "+name, 0)
			var id int64
			if err := fc.QueryRowContext(fresh.Ctx, "SELECT 'pg_temp."+name+"'::regclass::oid::bigint").Scan(&id); err != nil {
				t.Fatal(err)
			}
			if id == oldOID {
				collider = name
			}
			if id > oldOID {
				t.Fatalf("retained allocation passed source dictionary OID without collision: retained=%d source=%d", id, oldOID)
			}
		}
		regtsSelect(t, fresh, fb, fc, "SELECT setval('pg_temp."+collider+"',41,true) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(41)}})
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("mixed routine/dictionary Restore", err)
		}
		if newOID := regtsDictionaryOID(t, fresh, fc); newOID == oldOID {
			t.Fatalf("mixed allocation witness did not displace dictionary OID %d", oldOID)
		}
		regtsSelect(t, fresh, fb, fc, "SELECT 'pg_temp."+collider+"'::regclass::oid::bigint AS id,nextval('pg_temp."+collider+"') AS v", []string{"id", "v"}, []string{"INT8", "INT8"}, [][]any{{oldOID, int64(42)}})
		regtsMixedPersistentCheck(t, fresh, fb, fc)
		// Container field quotes escape the qualified name's identifier quotes.
		if !bytes.Contains(emitted, []byte(`\"Reg.TS\".unaccent`)) {
			t.Fatal("mixed dump omitted the qualified dictionary field reference")
		}
		if bytes.Contains(emitted, []byte(fmt.Sprintf(`,"%d")`, oldOID))) {
			t.Fatal("mixed dump serialized the source dictionary OID as a numeric field")
		}
	})
}

func regtsMixedPersistentCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	oid := regtsDictionaryOID(t, p, c)
	regtsSelect(t, p, b, c,
		"SELECT id,(c).p::oid::bigint AS p,(c).d::oid::bigint AS d FROM public.reg_ts_mixed_values ORDER BY id",
		[]string{"id", "p", "d"}, []string{"INT4", "INT8", "INT8"},
		[][]any{{int64(0), int64(1397), oid}, {int64(1), int64(1397), oid}, {int64(2), nil, nil}})
	regtsSelect(t, p, b, c,
		"SELECT id,(c).p::text AS p,(c).d AS d FROM public.reg_ts_mixed_values ORDER BY id",
		[]string{"id", "p", "d"}, []string{"INT4", "TEXT", "REGDICTIONARY"},
		[][]any{{int64(0), "abs(integer)", `"Reg.TS".unaccent`}, {int64(1), "abs(integer)", `"Reg.TS".unaccent`}, {int64(2), nil, nil}})
	regtsSelect(t, p, b, c,
		"SELECT (c).p::oid::bigint AS proc_oid,(c).d::oid::bigint AS dict_oid,(c).p::text AS p,(c).d AS d FROM public.reg_ts_mixed_view",
		[]string{"proc_oid", "dict_oid", "p", "d"}, []string{"INT8", "INT8", "TEXT", "REGDICTIONARY"},
		[][]any{{int64(1397), oid, "abs(integer)", `"Reg.TS".unaccent`}})
	// Exercise the restored definition, not only a row materialized before Dump.
	regtsExec(t, p, b, c, "INSERT INTO public.reg_ts_mixed_values(id) VALUES(3)", 1)
	regtsSelect(t, p, b, c,
		"SELECT (c).p::oid::bigint AS proc_oid,(c).d::oid::bigint AS dict_oid,(c).p::text AS p,(c).d AS d FROM public.reg_ts_mixed_values WHERE id=3",
		[]string{"proc_oid", "dict_oid", "p", "d"}, []string{"INT8", "INT8", "TEXT", "REGDICTIONARY"},
		[][]any{{int64(1397), oid, "abs(integer)", `"Reg.TS".unaccent`}})
	regtsExec(t, p, b, c, "DELETE FROM public.reg_ts_mixed_values WHERE id=3", 1)
}
