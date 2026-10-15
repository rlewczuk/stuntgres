package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONEmptyObjectControls(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE VIEW sqj_empty_view AS SELECT JSON_OBJECT() AS d,JSON_OBJECT(RETURNING json) AS j,JSON_OBJECT(RETURNING jsonb) AS b,JSON_OBJECT(RETURNING text) AS t,JSON_OBJECT(RETURNING bytea FORMAT JSON ENCODING UTF8) AS bytes`, 0))
		view := fixture.Select(`SELECT d,j,b,t,bytes FROM sqj_empty_view`,
			[]string{"d", "j", "b", "t", "bytes"}, []string{"JSON", "JSON", "JSONB", "TEXT", "BYTEA"},
			[][]any{{`{}`, `{}`, `{}`, `{}`, `{}`}})
		sqljsonBoth(t, p, b, c, view)
		run(fixture.Exec(`CREATE TABLE sqj_empty_defaults(id int,d json DEFAULT JSON_OBJECT(),j json DEFAULT JSON_OBJECT(RETURNING json),b jsonb DEFAULT JSON_OBJECT(RETURNING jsonb),t text DEFAULT JSON_OBJECT(RETURNING text),bytes bytea DEFAULT JSON_OBJECT(RETURNING bytea FORMAT JSON ENCODING UTF8))`, 0))
		run(fixture.Exec(`INSERT INTO sqj_empty_defaults(id) VALUES(1)`, 1))
		defaults := fixture.Select(`SELECT id,d,j,b,t,bytes FROM sqj_empty_defaults ORDER BY id`,
			[]string{"id", "d", "j", "b", "t", "bytes"}, []string{"INT4", "JSON", "JSON", "JSONB", "TEXT", "BYTEA"},
			[][]any{{int64(1), `{}`, `{}`, `{}`, `{}`, `{}`}})
		sqljsonBoth(t, p, b, c, defaults)
		if b.PG {
			return // PostgreSQL supplies the SQL contract, not native control APIs.
		}

		dump := fixture.CheckDump(t, p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		// Use the real emitted recipes, not a hand-authored replacement script.
		// CheckRoundtrip restores and requires a byte-identical deterministic redump.
		fixture.CheckRoundtrip(t, p, dump)
		sqljsonBoth(t, p, b, c, view)
		sqljsonBoth(t, p, b, c, defaults)
		run(fixture.Exec(`INSERT INTO sqj_empty_defaults(id) VALUES(2)`, 1))
		defaults.Rows = [][]any{
			{int64(1), `{}`, `{}`, `{}`, `{}`, `{}`},
			{int64(2), `{}`, `{}`, `{}`, `{}`, `{}`},
		}
		sqljsonBoth(t, p, b, c, defaults)

		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
				t.Fatal("actual empty-object emitted SQL replay", err)
			}
			pg := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
			sqljsonBoth(t, p, pg, p.Postgres, view)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec(`INSERT INTO sqj_empty_defaults(id) VALUES(2)`, 1))
			sqljsonBoth(t, p, pg, p.Postgres, defaults)
			if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
				t.Fatal(err)
			}
		}
	})
}
