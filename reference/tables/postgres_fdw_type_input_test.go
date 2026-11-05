package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// PostgreSQL's local foreign tuple descriptor selects textual input; the remote
// result OID is not permission to decode a value for a different local type.
func TestReferencePostgresFDWTypeInput(t *testing.T) {
	p, _ := postgresFDWPhysicalScenario(t)
	for _, step := range []fixture.Step{
		fixture.Exec(`CREATE TABLE "pf source ""quoted".type_input(id integer,binary_value bytea,text_value text,short_value text)`, 0),
		fixture.Exec(`INSERT INTO "pf source ""quoted".type_input VALUES(1,decode('6162','hex'),E'\\x6162','ab'),(2,NULL,NULL,NULL),(3,decode('','hex'),'','')`, 3),
		fixture.Exec(`CREATE TABLE "pf source ""quoted".type_rejected(text_value text,short_value text)`, 0),
		fixture.Exec(`INSERT INTO "pf source ""quoted".type_rejected VALUES(E'\\xff','abc')`, 1),
	} {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
	}
	postgresFDWEach(t, p, func(t *testing.T, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		postgresFDWPhysicalInstall(t, b, run)
		for _, query := range []string{
			`CREATE FOREIGN TABLE pf_type_input(id integer NOT NULL,label text OPTIONS(column_name 'binary_value'),bytes bytea OPTIONS(column_name 'text_value'),matching bytea OPTIONS(column_name 'binary_value')) SERVER pf_server OPTIONS(schema_name 'pf source "quoted',table_name 'type_input')`,
			`CREATE SCHEMA pf_helpers`,
			`CREATE SEQUENCE pf_helpers.input_calls START 1`,
			`CREATE FUNCTION pf_helpers.input_allowed(bytea) RETURNS boolean LANGUAGE sql VOLATILE STRICT AS $$SELECT nextval('pf_helpers.input_calls')>0 AND ($1=decode('6162','hex') OR octet_length($1)=0)$$`,
			`CREATE DOMAIN pf_helpers.pf_bytes AS bytea CONSTRAINT pf_bytes_allowed CHECK(pf_helpers.input_allowed(VALUE))`,
			`CREATE FOREIGN TABLE pf_domain_input(id integer NOT NULL,bytes pf_helpers.pf_bytes OPTIONS(column_name 'text_value'),short_text varchar(2) OPTIONS(column_name 'short_value')) SERVER pf_server OPTIONS(schema_name 'pf source "quoted',table_name 'type_input')`,
			`CREATE FOREIGN TABLE pf_domain_rejected(bytes pf_helpers.pf_bytes OPTIONS(column_name 'text_value')) SERVER pf_server OPTIONS(schema_name 'pf source "quoted',table_name 'type_rejected')`,
			`CREATE FOREIGN TABLE pf_typmod_rejected(short_text varchar(2) OPTIONS(column_name 'short_value')) SERVER pf_server OPTIONS(schema_name 'pf source "quoted',table_name 'type_rejected')`,
		} {
			run(fixture.Exec(query, 0))
		}
		const query = "SELECT id,label,bytes,matching FROM pf_type_input ORDER BY id"
		values := [][]any{{int64(1), `\x6162`, "ab", "ab"}, {int64(2), nil, nil, nil}, {int64(3), `\x`, "", ""}}
		converted := fixture.Select(query, []string{"id", "label", "bytes", "matching"}, []string{"INT4", "TEXT", "BYTEA", "BYTEA"}, values)
		run(converted)
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		for range 2 {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, converted)
		}

		const domainQuery = "SELECT id,bytes::bytea AS bytes,short_text FROM pf_domain_input ORDER BY id"
		domainValues := fixture.Select(domainQuery, []string{"id", "bytes", "short_text"}, []string{"INT4", "BYTEA", "VARCHAR"}, [][]any{{int64(1), "ab", "ab"}, {int64(2), nil, nil}, {int64(3), "", ""}})
		calls := func(n int64) {
			t.Helper()
			run(fixture.Select("SELECT last_value,is_called FROM pf_helpers.input_calls", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, true}}))
		}
		// Only the two non-NULL inputs call the STRICT domain predicate, once
		// each. NULL and empty octets remain different before constraint checks.
		run(domainValues)
		calls(2)
		domainStmt, err := c.PrepareContext(p.Ctx, domainQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer domainStmt.Close()
		domainHeld := fixture.HeldStatement{Executor: c, Statement: domainStmt, SQL: domainQuery}
		for _, n := range []int64{4, 6} {
			fixture.RunStep(t, p.Ctx, domainHeld, b.Recorder, domainValues)
			calls(n)
		}
		domainError := fdwDiagnostic("23514", `value for domain pf_helpers.pf_bytes violates check constraint "pf_bytes_allowed"`)
		domainError.Schema, domainError.DataType, domainError.Constraint = "pf_helpers", "pf_bytes", "pf_bytes_allowed"
		domainError.Context = `column "bytes" of foreign table "pf_domain_rejected"`
		postgresFDWQueryFailure(t, p, b, c, "SELECT bytes::bytea AS bytes FROM pf_domain_rejected", prepared, domainError)
		// Sequence advancement survives statement rollback and witnesses exactly
		// one constraint evaluation for the one genuinely rejected source row.
		calls(7)
		typmodError := fdwDiagnostic("22001", "value too long for type character varying(2)")
		typmodError.Context = `column "short_text" of foreign table "pf_typmod_rejected"`
		postgresFDWQueryFailure(t, p, b, c, "SELECT short_text FROM pf_typmod_rejected", prepared, typmodError)
		calls(7)
		run(converted)
	})
}
