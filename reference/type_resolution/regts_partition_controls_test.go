package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRegTSPartitionPersistence(t *testing.T) {
	var emitted []byte
	regtsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// The namespace exists early; the dictionary provider does not. Allocate
		// matching ordinary relations before installation, then attach late.
		for _, q := range []string{
			`CREATE SCHEMA "Reg.TS"`,
			"CREATE TABLE public.reg_ts_partition_parent(id integer,k regdictionary) PARTITION BY RANGE(k)",
			"CREATE TABLE public.reg_ts_partition_child(id integer,k regdictionary)",
			`CREATE EXTENSION unaccent WITH SCHEMA "Reg.TS"`,
			`ALTER TABLE public.reg_ts_partition_parent ATTACH PARTITION public.reg_ts_partition_child FOR VALUES FROM(MINVALUE) TO('"Reg.TS".unaccent')`,
			"CREATE TABLE public.reg_ts_partition_default PARTITION OF public.reg_ts_partition_parent DEFAULT",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO public.reg_ts_partition_parent VALUES(0,'-'::regdictionary)", 1)
		// Keep the source stream zero-only; destination routing probes run once
		// after actual replay, without unsupported partition DELETE.
		regtsSelect(t, p, b, c,
			"SELECT id,k::oid::bigint AS k FROM public.reg_ts_partition_child",
			[]string{"id", "k"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(0)}})

		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native dictionary partition dump required for PostgreSQL replay")
			}
			for _, q := range []string{
				"DROP TABLE public.reg_ts_partition_parent CASCADE",
				"DROP EXTENSION unaccent",
				`DROP SCHEMA "Reg.TS"`,
			} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("PostgreSQL rejected actual dictionary partition dump", err)
			}
			regtsPartitionPersistentCheck(t, p, b, c)
			return
		}

		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Len() == 0 {
			t.Fatal("empty dictionary partition dump")
		}
		emitted = bytes.Clone(dump.Bytes())
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("dictionary partition Restore", err)
		}
		regtsPartitionPersistentCheck(t, fresh, fb, fc)
		// Only one finite partition and one extension occur in this scenario.
		// Check the real stream after replay, so missing predecessors first
		// expose the actual Restore failure rather than only presentation.
		extension := bytes.Index(emitted, []byte("CREATE EXTENSION"))
		bound := bytes.Index(emitted, []byte("FOR VALUES FROM"))
		if extension < 0 || bound < 0 || extension >= bound {
			t.Fatalf("dictionary provider must precede finite partition declaration: extension=%d bound=%d", extension, bound)
		}
		if !bytes.Contains(emitted, []byte(`"Reg.TS".unaccent`)) {
			t.Fatal("partition dump omitted qualified live dictionary bound")
		}
	})
}

func regtsPartitionPersistentCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	oid := regtsDictionaryOID(t, p, c)
	// The finite upper bound excludes the named descriptor itself and includes
	// its immediate predecessor. This proves bound identity after allocation
	// changes without relying on release-specific pg_node_tree presentation.
	regtsExec(t, p, b, c, fmt.Sprintf("INSERT INTO public.reg_ts_partition_parent VALUES(1,'%d'::regdictionary),(2,'\"Reg.TS\".unaccent'::regdictionary),(3,'%d'::regdictionary),(4,NULL)", oid-1, oid+1), 4)
	regtsSelect(t, p, b, c,
		"SELECT id,k::oid::bigint AS k FROM public.reg_ts_partition_child ORDER BY id",
		[]string{"id", "k"}, []string{"INT4", "INT8"},
		[][]any{{int64(0), int64(0)}, {int64(1), oid - 1}})
	regtsSelect(t, p, b, c,
		"SELECT id,k::oid::bigint AS k FROM public.reg_ts_partition_default ORDER BY id",
		[]string{"id", "k"}, []string{"INT4", "INT8"},
		[][]any{{int64(2), oid}, {int64(3), oid + 1}, {int64(4), nil}})
	regtsSelect(t, p, b, c,
		"SELECT id,k::oid::bigint AS k FROM public.reg_ts_partition_parent ORDER BY id",
		[]string{"id", "k"}, []string{"INT4", "INT8"},
		[][]any{{int64(0), int64(0)}, {int64(1), oid - 1}, {int64(2), oid}, {int64(3), oid + 1}, {int64(4), nil}})
	// Drop the populated default child before testing the exclusive endpoint.
	// Keep the valid finite-child probe as part of the atomicity baseline.
	regtsExec(t, p, b, c, "DROP TABLE public.reg_ts_partition_default", 0)
	miss := fixture.ImplicitError(
		`INSERT INTO public.reg_ts_partition_parent VALUES(5,'-'::regdictionary),(6,'"Reg.TS".unaccent'::regdictionary)`,
		fixture.Diagnostic{
			Code: "23514", Message: `no partition of relation "reg_ts_partition_parent" found for row`,
			Schema: "public", Table: "reg_ts_partition_parent",
			Detail: `Partition key of the failing row contains (k) = ("Reg.TS".unaccent).`,
		})
	miss.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, c, b.Recorder, miss)
	regtsSelect(t, p, b, c,
		"SELECT id,k::oid::bigint AS k FROM public.reg_ts_partition_child ORDER BY id",
		[]string{"id", "k"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(0)}, {int64(1), oid - 1}})
}
