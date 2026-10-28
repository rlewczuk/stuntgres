package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbRecordStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) []fixture.Step {
	t.Helper()
	jsonbRecordSetup(t, p, b, c,
		`CREATE SCHEMA jbr_saved_schema`,
		`CREATE TABLE jbr_saved_pair(a int,b text)`,
		`CREATE TABLE jbr_saved_docs(id int,j jsonb,js jsonb)`,
		`CREATE TABLE jbr_saved_rows(id int,v jbr_saved_pair)`)
	buffer := []byte(`{"a":1}`)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO jbr_saved_docs VALUES(1,$1,'[{"a":1},{"b":"x"},{}]'),(2,'{"b":"x"}','[]'),(3,NULL,NULL)`, 3, buffer)))
	for i := range buffer {
		buffer[i] = 'x'
	}
	jsonbRecordSetup(t, p, b, c,
		`CREATE VIEW jbr_saved_scalar AS SELECT id,jsonb_populate_record('(9,keep)'::jbr_saved_pair,j) AS v FROM jbr_saved_docs`,
		`CREATE VIEW jbr_saved_typed AS SELECT d.id,r.a,r.b,r.c FROM jbr_saved_docs d,jsonb_to_record(d.j) AS r(a int,b varchar(3),c numeric(4,1))`,
		`CREATE VIEW jbr_saved_set AS SELECT d.id,r.a,r.b,r.c FROM jbr_saved_docs d,jsonb_to_recordset(d.js) AS r(a int,b varchar(3),c numeric(4,1))`,
		`CREATE VIEW jbr_saved_empty AS SELECT * FROM jsonb_to_recordset('[]'::jsonb) AS r(a int,b varchar(3),c numeric(4,1),nested jbr_saved_pair)`,
		`CREATE VIEW jbr_saved_null AS SELECT * FROM jsonb_to_record(NULL::jsonb) AS r(a int,b varchar(3),c numeric(4,1))`)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jbr_saved_rows SELECT id,v FROM jbr_saved_scalar`, 3))
	// Relation-owned rowtypes follow their table's identity; standalone types
	// instead use ALTER TYPE identity utilities.
	jsonbRecordSetup(t, p, b, c, `ALTER TABLE jbr_saved_docs RENAME COLUMN j TO document`, `ALTER TABLE jbr_saved_pair RENAME TO jbr_saved_renamed`, `ALTER TABLE jbr_saved_renamed SET SCHEMA jbr_saved_schema`)
	checks := []fixture.Step{
		fixture.Select(`SELECT id,v::text AS v FROM jbr_saved_rows ORDER BY id`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "(1,keep)"}, {int64(2), "(9,x)"}, {int64(3), "(9,keep)"}}),
		fixture.Select(`SELECT id,v::text AS v FROM jbr_saved_scalar ORDER BY id`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "(1,keep)"}, {int64(2), "(9,x)"}, {int64(3), "(9,keep)"}}),
		fixture.Select(`SELECT * FROM jbr_saved_typed ORDER BY id`, []string{"id", "a", "b", "c"}, []string{"INT4", "INT4", "VARCHAR", "NUMERIC"}, [][]any{{int64(1), int64(1), nil, nil}, {int64(2), nil, "x", nil}, {int64(3), nil, nil, nil}}),
		fixture.Select(`SELECT * FROM jbr_saved_set ORDER BY id,a NULLS LAST,b NULLS LAST`, []string{"id", "a", "b", "c"}, []string{"INT4", "INT4", "VARCHAR", "NUMERIC"}, [][]any{{int64(1), int64(1), nil, nil}, {int64(1), nil, "x", nil}, {int64(1), nil, nil, nil}}),
		fixture.Select(`SELECT a,b,c FROM jbr_saved_empty`, []string{"a", "b", "c"}, []string{"INT4", "VARCHAR", "NUMERIC"}, nil),
		fixture.Select(`SELECT * FROM jbr_saved_null`, []string{"a", "b", "c"}, []string{"INT4", "VARCHAR", "NUMERIC"}, [][]any{{nil, nil, nil}}),
	}
	for _, s := range checks {
		fixture.JSONEachBoth(t, p, b, c, s)
	}
	return checks
}

func jsonbRecordControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, checks []fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // Native controls have no PostgreSQL API equivalent.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, checks[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !old.Next() {
		t.Fatal("missing retained row")
	}
	var id int
	var value string
	if err := old.Scan(&id, &value); err != nil || id != 1 || value != "(1,keep)" {
		t.Fatal(id, value, err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.StringCoreRetainedRows(t, old, nil, checks[0].Columns, checks[0].Types, checks[0].Rows[1:], false)
	for i, ct := range meta {
		if ct.Name() != checks[0].Columns[i] || ct.DatabaseTypeName() != checks[0].Types[i] {
			t.Fatal("retained metadata changed", ct)
		}
	}
	for _, s := range checks {
		fixture.JSONEachBoth(t, p, b, c, s)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatalf("record dump not deterministic after restore\n--- before ---\n%s\n--- after ---\n%s", dump.String(), redump.String())
	}
	fresh := fixture.NewFixture(t, false, true)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, s := range checks {
		fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.Backends()[0].Recorder, s)
	}
	readerErr := errors.New("jbr reader failure")
	for _, failure := range []struct{ sql, state string }{
		{dump.String() + "\n" + `CREATE TABLE replacement(a int);INSERT INTO replacement VALUES(1);broken syntax;`, ""},
		// These are real dump-plus-VALUES imports, not unsupported arbitrary
		// restore commands that could fail before reaching the owned codecs.
		{dump.String() + "\n" + `INSERT INTO jbr_saved_rows VALUES(99,'(bad,late)');`, "22P02"},
		{dump.String() + "\n" + `CREATE DOMAIN replacement_positive AS int CONSTRAINT replacement_positive_check CHECK(VALUE>0);CREATE TABLE replacement(a replacement_positive);INSERT INTO replacement VALUES(0);`, "23514"},
	} {
		err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(failure.sql))
		if err == nil {
			t.Fatal("bad staged restore succeeded")
		}
		if failure.state != "" {
			fixture.State(t, err, failure.state)
		}
		for _, s := range checks {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: readerErr}); !errors.Is(err, readerErr) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	fixture.LexicalOrdinary(t, stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())), context.Canceled)
	for _, s := range checks {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	jsonbRecordControlIO(t, p, b, c, dump.Bytes(), checks)
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE;CREATE SCHEMA public;DROP SCHEMA IF EXISTS jbr_saved_schema CASCADE`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual native dump replay", err)
		}
		for _, s := range checks {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE;CREATE SCHEMA public;DROP SCHEMA jbr_saved_schema CASCADE`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceJSONBRecordStoredControls(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "jbr_saved_schema")
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		checks := jsonbRecordStored(t, p, b, c)
		jsonbRecordControls(t, p, b, c, checks)
	})
}
