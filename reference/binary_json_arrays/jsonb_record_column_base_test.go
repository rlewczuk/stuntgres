package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBRecordColumnBase(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c,
			`CREATE TYPE jbr_column_inner AS (t regtype,c regclass,v int)`,
			`CREATE TYPE jbr_column_recursive AS (n jbr_column_inner,a regtype[])`,
			`CREATE TABLE jbr_column_docs(id int,b jbr_column_inner,r jbr_column_recursive,j jsonb,js jsonb,jr jsonb,jrs jsonb)`)
		plain := `{"t":"integer","c":"pg_catalog.pg_class","v":1}`
		recursive := `{"n":{"t":"integer","c":"pg_catalog.pg_class","v":1},"a":["integer","text"]}`
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jbr_column_docs VALUES(1,NULL,NULL,$1,$2,$3,$4),(2,'(text,pg_catalog.pg_class,9)',NULL,$1,$2,$3,$4)`, 2, plain, "["+plain+"]", recursive, "["+recursive+"]"))
		var checks []fixture.Step
		for _, layout := range []struct {
			base, document, projection, good, next, bad string
			columns, types                              []string
			first, second                               []any
		}{
			{"b", "j", `x.t::oid::bigint AS t,x.c::oid::bigint AS c,x.v AS v`, plain,
				`{"t":"text","c":"pg_catalog.pg_class","v":2}`, `{"t":"integer","c":"pg_catalog.pg_class","v":"bad"}`,
				[]string{"t", "c", "v"}, []string{"INT8", "INT8", "INT4"}, []any{int64(23), int64(1259), int64(1)}, []any{int64(25), int64(1259), int64(2)}},
			{"r", "jr", `(x.n).t::oid::bigint AS t,(x.n).c::oid::bigint AS c,x.a[1]::oid::bigint AS first,x.a[2]::oid::bigint AS second`, recursive,
				`{"n":{"t":"text","c":"pg_catalog.pg_class","v":2},"a":["text","integer"]}`, `{"n":{"t":"integer","c":"pg_catalog.pg_class","v":"bad"},"a":["integer","text"]}`,
				[]string{"t", "c", "first", "second"}, []string{"INT8", "INT8", "INT8", "INT8"}, []any{int64(23), int64(1259), int64(23), int64(25)}, []any{int64(25), int64(1259), int64(25), int64(23)}},
		} {
			for _, function := range []string{"jsonb_populate_record", "jsonb_populate_recordset", "jsonb_populate_record_valid"} {
				t.Run(layout.base+"/"+function, func(t *testing.T) {
					good, next, bad := layout.good, layout.next, layout.bad
					document := layout.document
					if function == "jsonb_populate_recordset" {
						good, next, bad = "["+good+"]", "["+next+"]", "["+bad+"]"
						document += "s"
					}
					// The base is always a stored column. The sole parameter is JSONB;
					// projections use numeric identity casts, never named registry input.
					query := `SELECT ` + layout.projection + ` FROM jbr_column_docs d,` + function + `(d.` + layout.base + `,$1::jsonb) x WHERE d.id=1`
					columns, types := layout.columns, layout.types
					first, second := layout.first, layout.second
					if function == "jsonb_populate_record_valid" {
						query = `SELECT jsonb_populate_record_valid(d.` + layout.base + `,$1::jsonb) AS valid FROM jbr_column_docs d WHERE d.id=1`
						columns, types, first, second = []string{"valid"}, []string{"BOOL"}, []any{true}, []any{true}
					}
					jsonEachBoth(t, p, b, c, fixture.Select(query, columns, types, [][]any{first}, good))
					literal := strings.ReplaceAll(query, "$1::jsonb", "'"+good+"'::jsonb")
					jsonEachBoth(t, p, b, c, fixture.Select(literal, columns, types, [][]any{first}))
					stored := strings.ReplaceAll(query, "$1::jsonb", "d."+document)
					jsonEachBoth(t, p, b, c, fixture.Select(stored, columns, types, [][]any{first}))
					// Also analyze the same named base usage with a non-NULL datum.
					if layout.base == "b" {
						nonnull := strings.ReplaceAll(query, "d.id=1", "d.id=2")
						jsonEachBoth(t, p, b, c, fixture.Select(nonnull, columns, types, [][]any{first}, good))
					}
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, columns, types, [][]any{first}, good))
					if function == "jsonb_populate_record_valid" {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, columns, types, [][]any{{false}}, bad))
					} else {
						want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
						fixture.CompositeReferenceError(t, p, c, query, "execute", want, bad)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(query, want, bad))
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, columns, types, [][]any{second}, next))
					view := "jbr_column_" + layout.base + "_" + function
					jsonbRecordSetup(t, p, b, c, "CREATE VIEW "+view+" AS "+stored)
					check := fixture.Select("SELECT * FROM "+view, columns, types, [][]any{first})
					jsonEachBoth(t, p, b, c, check)
					checks = append(checks, check)
				})
			}
		}
		if b.PG {
			return
		}
		// Native APIs have no PostgreSQL equivalent. Retain actual same/fresh
		// replacement and emitted-SQL replay phases for execution after discovery.
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
			jsonEachBoth(t, p, b, c, check)
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
				t.Fatal("actual native column-base dump replay", err)
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
