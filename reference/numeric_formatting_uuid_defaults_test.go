package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

// UUID generators never return SQL NULL: num_nulls therefore returns INTEGER0.
// Formatting that count gives TEXT0; format('%s',count) supplies TEXT0 to the
// selected numeric reader. These independent expectations do not depend on a
// random UUID's spelling or on backend parity.
func numericFormattingUUIDDefaults(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	numericFormattingSelect(t, p, b, c, "SELECT to_char(num_nulls(uuidv4()),'FM9') AS label,to_number(format('%s',num_nulls(uuidv4())),'9') AS parsed", []string{"label", "parsed"}, []string{"TEXT", "NUMERIC"}, [][]any{{"0", "0"}})
	for _, prepared := range []bool{false, true} {
		table := fmt.Sprintf("nf_uuid_default_%t", prepared)
		ddl := referenceExec("CREATE TABLE "+table+"(id integer,label text DEFAULT to_char(num_nulls(uuidv4()),'FM9'),parsed numeric DEFAULT to_number(format('%s',num_nulls(uuidv4())),'9'))", 0)
		ddl.prepared = prepared
		run(ddl)
		q := "INSERT INTO " + table + "(id) VALUES($1::integer)"
		// Direct and genuinely reused held INSERT both demand the stored trees.
		run(referenceExec(q, 1, int64(1)))
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, id := range []int64{2, 3} {
			referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 1, id))
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		defaults := referenceExec("INSERT INTO "+table+" DEFAULT VALUES", 1)
		defaults.prepared = prepared
		run(defaults)
		numericFormattingBoth(t, p, b, c, referenceSelect("SELECT id,label,parsed FROM "+table+" ORDER BY id NULLS LAST", []string{"id", "label", "parsed"}, []string{"INT4", "TEXT", "NUMERIC"}, [][]any{{int64(1), "0", "0"}, {int64(2), "0", "0"}, {int64(3), "0", "0"}, {nil, "0", "0"}}))
	}
	if b.pg {
		return
	} // PostgreSQL has no native Reset/Dump/Restore API.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"pg_catalog.to_char(", "pg_catalog.to_number(", "pg_catalog.num_nulls(", "pg_catalog.uuidv4(", "pg_catalog.format("} {
		if !strings.Contains(dump.String(), fragment) {
			t.Fatalf("missing symbolic DEFAULT %q in actual dump:\n%s", fragment, dump.String())
		}
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, prepared := range []bool{false, true} {
		table := fmt.Sprintf("nf_uuid_default_%t", prepared)
		// New DEFAULT VALUES must evaluate restored symbolic providers, not the
		// four previously dumped materialized result cells.
		s := referenceExec("INSERT INTO "+table+"(id) VALUES(4)", 1)
		s.prepared = prepared
		run(s)
		s = referenceExec("INSERT INTO "+table+" DEFAULT VALUES", 1)
		s.prepared = prepared
		run(s)
		numericFormattingBoth(t, p, b, c, referenceSelect("SELECT id,label,parsed FROM "+table+" ORDER BY id NULLS LAST", []string{"id", "label", "parsed"}, []string{"INT4", "TEXT", "NUMERIC"}, [][]any{{int64(1), "0", "0"}, {int64(2), "0", "0"}, {int64(3), "0", "0"}, {int64(4), "0", "0"}, {nil, "0", "0"}, {nil, "0", "0"}}))
	}
	if p.postgres != nil {
		// Replay the actual emitted SQL, not a parallel handwritten fixture.
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatalf("emitted SQL replay: %v\n%s", err, dump.String())
		}
		for _, prepared := range []bool{false, true} {
			table := fmt.Sprintf("nf_uuid_default_%t", prepared)
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO "+table+"(id) VALUES(4)", 1))
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO "+table+" DEFAULT VALUES", 1))
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT id,label,parsed FROM "+table+" ORDER BY id NULLS LAST", []string{"id", "label", "parsed"}, []string{"INT4", "TEXT", "NUMERIC"}, [][]any{{int64(1), "0", "0"}, {int64(2), "0", "0"}, {int64(3), "0", "0"}, {int64(4), "0", "0"}, {nil, "0", "0"}, {nil, "0", "0"}}))
		}
		// Dump contains the scenario's complete schema, including other smoke
		// consumers when this body is reused. Clear replay objects before the
		// ensuing shared PostgreSQL scenario, following existing control helpers.
		if _, err := p.postgres.ExecContext(p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceNumericFormattingUUIDDefaults(t *testing.T) {
	numericFormattingScenario(t, numericFormattingUUIDDefaults)
}
