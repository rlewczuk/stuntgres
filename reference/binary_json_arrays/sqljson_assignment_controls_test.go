package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONAssignmentControls(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// Each constructor produces JSON; the column owns the outer JSONB
		// assignment. Moving that conversion into RETURNING is not equivalent:
		// SCALAR/JSON have no such clause, and binary OBJECT checks the NULL key.
		run(fixture.Exec(`CREATE TABLE sqj_assignment (
			id int,
			s jsonb DEFAULT JSON_SCALAR(1),
			j jsonb DEFAULT JSON('{"a":1}'),
			o jsonb DEFAULT JSON_OBJECT(NULL:NULL ABSENT ON NULL)
		)`, 0))
		run(fixture.Exec(`INSERT INTO sqj_assignment(id) VALUES(1)`, 1))
		check := fixture.Select(`SELECT id,s,j,o FROM sqj_assignment ORDER BY id`,
			[]string{"id", "s", "j", "o"}, []string{"INT4", "JSONB", "JSONB", "JSONB"},
			[][]any{{int64(1), `1`, `{"a": 1}`, `{}`}})
		fixture.SQLJSONBoth(t, p, b, c, check)
		if b.PG {
			run(fixture.Exec(`INSERT INTO sqj_assignment(id) VALUES(2)`, 1))
			check.Rows = append(check.Rows, []any{int64(2), `1`, `{"a": 1}`, `{}`})
			fixture.SQLJSONBoth(t, p, b, c, check)
			return
		}

		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		// Restore must consume the real recipe, not an equivalent handwritten
		// expression or a normalized replacement of the emitted SQL.
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatalf("restore actual assignment recipe: %v\n%s", err, dump.String())
		}
		fixture.SQLJSONBoth(t, p, b, c, check)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatalf("nondeterministic assignment redump\nbefore:\n%s\nafter:\n%s", dump.String(), redump.String())
		}
		run(fixture.Exec(`INSERT INTO sqj_assignment(id) VALUES(2)`, 1))
		check.Rows = append(check.Rows, []any{int64(2), `1`, `{"a": 1}`, `{}`})
		fixture.SQLJSONBoth(t, p, b, c, check)

		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatalf("PostgreSQL replay of actual assignment SQL: %v\n%s", err, dump.String())
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord,
				fixture.Exec(`INSERT INTO sqj_assignment(id) VALUES(2)`, 1))
			for _, prepared := range []bool{false, true} {
				check.Prepared = prepared
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
				t.Fatal(err)
			}
		}
	})
}
