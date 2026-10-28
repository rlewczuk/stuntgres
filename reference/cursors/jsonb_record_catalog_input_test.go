package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBRecordCatalogInput(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c,
			`CREATE TYPE jbr_catalog_nested AS (t regtype,c regclass)`,
			`CREATE TABLE jbr_catalog_documents(id int,j jsonb,js jsonb)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jbr_catalog_documents VALUES(1,'{"t":"integer","c":"pg_catalog.pg_class"}','[{"t":"integer","c":"pg_catalog.pg_class"},{"t":"text","c":"pg_catalog.pg_class"}]')`, 1))
		// Project numeric builtin identities, not catalog-name casts that could
		// independently capture input authority and conceal the missing member capture.
		for _, tc := range []struct {
			name, call, input string
			rows              [][]any
		}{
			{"scalar", `jsonb_to_record($1::jsonb)`, `{"t":"integer","c":"pg_catalog.pg_class"}`, [][]any{{int64(23), int64(1259)}}},
			{"set", `jsonb_to_recordset($1::jsonb)`, `[{"t":"integer","c":"pg_catalog.pg_class"},{"t":"text","c":"pg_catalog.pg_class"}]`, [][]any{{int64(23), int64(1259)}, {int64(25), int64(1259)}}},
			{"contextual scalar", `jsonb_populate_record(NULL::record,$1::jsonb)`, `{"t":"integer","c":"pg_catalog.pg_class"}`, [][]any{{int64(23), int64(1259)}}},
			{"contextual set", `jsonb_populate_recordset(NULL::record,$1::jsonb)`, `[{"t":"integer","c":"pg_catalog.pg_class"},{"t":"text","c":"pg_catalog.pg_class"}]`, [][]any{{int64(23), int64(1259)}, {int64(25), int64(1259)}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				query := `SELECT t::oid::bigint AS t,c::oid::bigint AS c FROM ` + tc.call + ` AS r(t regtype,c regclass) ORDER BY t`
				fixture.JSONEachBoth(t, p, b, c, fixture.Select(query, []string{"t", "c"}, []string{"INT8", "INT8"}, tc.rows, tc.input))
				literal := strings.ReplaceAll(query, "$1::jsonb", "'"+tc.input+"'::jsonb")
				fixture.JSONEachBoth(t, p, b, c, fixture.Select(literal, []string{"t", "c"}, []string{"INT8", "INT8"}, tc.rows))
			})
		}
		for _, function := range []string{"jsonb_to_record", "jsonb_to_recordset"} {
			input := `{"n":{"t":"integer","c":"pg_catalog.pg_class"},"a":["integer","text"]}`
			if strings.HasSuffix(function, "recordset") {
				input = "[" + input + "]"
			}
			query := `SELECT (n).t::oid::bigint AS t,(n).c::oid::bigint AS c,a[1]::oid::bigint AS first,a[2]::oid::bigint AS second FROM ` + function + `($1::jsonb) AS r(n jbr_catalog_nested,a regtype[])`
			fixture.JSONEachBoth(t, p, b, c, fixture.Select(query, []string{"t", "c", "first", "second"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(23), int64(1259), int64(23), int64(25)}}, input))
		}
		for _, tc := range []struct{ member, input, code, message string }{
			{"regtype", `{"v":"jbr_catalog_missing_type"}`, "42704", `type "jbr_catalog_missing_type" does not exist`},
			{"regclass", `{"v":"jbr_catalog_missing_relation"}`, "42P01", `relation "jbr_catalog_missing_relation" does not exist`},
		} {
			for _, function := range []string{"jsonb_to_record", "jsonb_to_recordset"} {
				input := tc.input
				if strings.HasSuffix(function, "recordset") {
					input = "[" + input + "]"
				}
				query := `SELECT v::oid::bigint AS v FROM ` + function + `($1::jsonb) AS r(v ` + tc.member + `)`
				fixture.CompositeReferenceError(t, p, c, query, "execute", fixture.ControlsReferenceDiagnostic(tc.code, tc.message), input)
			}
		}
		// Keep the same prepared object through repeated executions and unrelated
		// catalog publication. The destination occurs only in the AS declaration.
		heldQuery := `SELECT t::oid::bigint AS t FROM jsonb_to_record($1::jsonb) AS r(t regtype)`
		held, err := c.PrepareContext(p.Ctx, heldQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for i, tc := range []struct {
			input string
			oid   int64
		}{{`{"t":"integer"}`, 23}, {`{"t":"text"}`, 25}} {
			if i == 1 {
				jsonbRecordSetup(t, p, b, c, `CREATE TABLE jbr_catalog_unrelated(v int)`)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: heldQuery}, b.Recorder,
				fixture.Select(heldQuery, []string{"t"}, []string{"INT8"}, [][]any{{tc.oid}}, tc.input))
		}
		jsonbRecordSetup(t, p, b, c,
			`CREATE VIEW jbr_catalog_scalar AS SELECT r.t::oid::bigint AS t,r.c::oid::bigint AS c FROM jbr_catalog_documents d,jsonb_to_record(d.j) AS r(t regtype,c regclass)`,
			`CREATE VIEW jbr_catalog_set AS SELECT r.t::oid::bigint AS t,r.c::oid::bigint AS c FROM jbr_catalog_documents d,jsonb_to_recordset(d.js) AS r(t regtype,c regclass)`,
			`CREATE VIEW jbr_catalog_context AS SELECT r.t::oid::bigint AS t FROM jbr_catalog_documents d,jsonb_populate_record(NULL::record,d.j) AS r(t regtype)`,
			`CREATE VIEW jbr_catalog_recursive AS SELECT (r.n).t::oid::bigint AS t,r.a[2]::oid::bigint AS second FROM jsonb_to_record('{"n":{"t":"integer","c":"pg_catalog.pg_class"},"a":["integer","text"]}'::jsonb) AS r(n jbr_catalog_nested,a regtype[])`)
		checks := []fixture.Step{
			fixture.Select(`SELECT * FROM jbr_catalog_scalar`, []string{"t", "c"}, []string{"INT8", "INT8"}, [][]any{{int64(23), int64(1259)}}),
			fixture.Select(`SELECT * FROM jbr_catalog_set ORDER BY t`, []string{"t", "c"}, []string{"INT8", "INT8"}, [][]any{{int64(23), int64(1259)}, {int64(25), int64(1259)}}),
			fixture.Select(`SELECT * FROM jbr_catalog_context`, []string{"t"}, []string{"INT8"}, [][]any{{int64(23)}}),
			fixture.Select(`SELECT * FROM jbr_catalog_recursive`, []string{"t", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(23), int64(25)}}),
		}
		for _, check := range checks {
			fixture.JSONEachBoth(t, p, b, c, check)
		}
		if b.PG {
			return
		}
		// Native-only controls remain present for execution after oracle discovery;
		// PostgreSQL's phase above exercises the same persistent SQL consumers.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, check := range checks {
			fixture.JSONEachBoth(t, p, b, c, check)
		}
		fresh := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, check := range checks {
			fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.Backends()[0].Recorder, check)
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE;CREATE SCHEMA public`); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual native catalog-input dump replay", err)
			}
			for _, check := range checks {
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE;CREATE SCHEMA public`); err != nil {
				t.Fatal(err)
			}
		}
	})
}
