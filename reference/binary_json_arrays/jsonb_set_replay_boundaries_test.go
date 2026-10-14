package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func jsonbSetReplayBoundarySetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		`CREATE DOMAIN jbs_replay_doc AS jsonb CONSTRAINT jbs_replay_object CHECK (jsonb_typeof(VALUE) = 'object')`,
		`CREATE TABLE jbs_replay_input(id int,src jsonb,k text,r numeric)`,
		`CREATE VIEW jbs_replay_named AS SELECT id,jsonb_set_lax(null_value_treatment => 'delete_key',replacement => NULL,path => '{a}',jsonb_in => src) AS changed FROM jbs_replay_input`,
		`CREATE VIEW jbs_replay_checked AS SELECT id,jsonb_set_lax(src::jbs_replay_doc,'{a}',NULL,true,'return_target') AS changed FROM jbs_replay_input`,
		`CREATE VIEW jbs_replay_outer AS SELECT id,jsonb_set((((src::json)::jsonb)::jbs_replay_doc)::jsonb,'{a}',(((r::numeric(4,1))::float8)::text)::jsonb) AS changed FROM jbs_replay_input`,
		`CREATE VIEW jbs_replay_typmod AS SELECT id,jsonb_set(src,ARRAY[k::varchar(1)]::text[],'9') AS changed FROM jbs_replay_input`,
		`CREATE VIEW jbs_replay_types AS SELECT id,k::varchar(1) AS key,r::numeric(4,1) AS replacement,((src::json)::jsonb)::jbs_replay_doc AS checked FROM jbs_replay_input`,
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jbs_replay_input VALUES(1,'{"a":1,"b":2}','ab',12.340),(2,'{"a":3,"b":4}','bc',7.890)`, 2))
}

func jsonbSetReplayBoundaryChecks(t *testing.T, p *fixture.Pair, x fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	for _, tc := range []struct {
		view string
		want [][]any
	}{
		{"jbs_replay_named", [][]any{{int64(1), `{"b": 2}`}, {int64(2), `{"b": 4}`}}},
		{"jbs_replay_checked", [][]any{{int64(1), `{"a": 1, "b": 2}`}, {int64(2), `{"a": 3, "b": 4}`}}},
		{"jbs_replay_outer", [][]any{{int64(1), `{"a": 12.3, "b": 2}`}, {int64(2), `{"a": 7.9, "b": 4}`}}},
		{"jbs_replay_typmod", [][]any{{int64(1), `{"a": 9, "b": 2}`}, {int64(2), `{"a": 3, "b": 9}`}}},
	} {
		s := fixture.Select("SELECT id,changed FROM "+tc.view+" ORDER BY id", []string{"id", "changed"}, []string{"INT4", "JSONB"}, tc.want)
		fixture.RunStep(t, p.Ctx, x, recorder, s)
		fixture.RunStep(t, p.Ctx, x, recorder, fixture.Prepared(s))
	}
	s := fixture.Select("SELECT key,replacement,checked FROM jbs_replay_types WHERE id=1", []string{"key", "replacement", "checked"}, []string{"VARCHAR", "NUMERIC", "JSONB"}, [][]any{{"a", "12.3", `{"a": 1, "b": 2}`}})
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 3 {
			t.Fatal(types, err)
		}
		if length, known := types[0].Length(); !known || length != 1 {
			t.Fatalf("retained varchar typmod=%d,%v want 1,true", length, known)
		}
		if precision, scale, known := types[1].DecimalSize(); !known || precision != 4 || scale != 1 {
			t.Fatalf("retained numeric typmod=%d,%d,%v want 4,1,true", precision, scale, known)
		}
	}
	fixture.RunStep(t, p.Ctx, x, recorder, s)
	fixture.RunStep(t, p.Ctx, x, recorder, fixture.Prepared(s))
	fixture.RunStep(t, p.Ctx, x, recorder, fixture.Select("SELECT a.atttypid = 'public.jbs_replay_doc'::regtype::oid AS domain_identity FROM pg_catalog.pg_attribute a WHERE a.attrelid='public.jbs_replay_types'::regclass::oid AND a.attname='checked'", []string{"domain_identity"}, []string{"BOOL"}, [][]any{{true}}))
	fixture.RunStep(t, p.Ctx, x, recorder, fixture.Select("SELECT id,src,k,r FROM jbs_replay_input ORDER BY id", []string{"id", "src", "k", "r"}, []string{"INT4", "JSONB", "TEXT", "NUMERIC"}, [][]any{{int64(1), `{"a": 1, "b": 2}`, "ab", "12.340"}, {int64(2), `{"a": 3, "b": 4}`, "bc", "7.890"}}))
}

func jsonbSetReplayBoundaryDomainFailure(t *testing.T, p *fixture.Pair, x fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, recorder, fixture.Exec(`INSERT INTO jbs_replay_input VALUES(3,'[1]','ab',12.340)`, 1))
	d := fixture.ControlsReferenceDiagnostic("23514", `value for domain jbs_replay_doc violates check constraint "jbs_replay_object"`)
	d.Schema, d.DataType, d.Constraint = "public", "jbs_replay_doc", "jbs_replay_object"
	for _, view := range []string{"jbs_replay_checked", "jbs_replay_outer"} {
		s := jsonbSetError("SELECT changed FROM "+view+" WHERE id=3", d)
		fixture.RunStep(t, p.Ctx, x, recorder, s)
		fixture.RunStep(t, p.Ctx, x, recorder, fixture.Prepared(s))
	}
	fixture.RunStep(t, p.Ctx, x, recorder, fixture.Exec("DELETE FROM jbs_replay_input WHERE id=3", 1))
	jsonbSetReplayBoundaryChecks(t, p, x, recorder)
}

func jsonbSetReplayBoundaryControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	if b.PG {
		return // PostgreSQL has no native control API.
	}
	var baseline bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
		t.Fatal(err)
	}
	for round := range 3 {
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(baseline.Bytes())); err != nil {
			t.Fatal(round, err)
		}
		jsonbSetReplayBoundaryChecks(t, p, c, b.Recorder)
		jsonbSetReplayBoundaryDomainFailure(t, p, c, b.Recorder)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(round, err)
		}
		if after.String() != baseline.String() {
			t.Fatalf("replay boundary dump changed in round %d\nbefore:\n%s\nafter:\n%s", round, baseline.String(), after.String())
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, after.String()); err != nil {
				t.Fatal("actual emitted boundary dump replay", round, err)
			}
			jsonbSetReplayBoundaryChecks(t, p, p.Postgres, p.PostgresRecord)
			jsonbSetReplayBoundaryDomainFailure(t, p, p.Postgres, p.PostgresRecord)
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestReferenceJSONBSetReplayBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			jsonbSetReplayBoundarySetup(t, p, b, c)
			jsonbSetReplayBoundaryChecks(t, p, c, b.Recorder)
			jsonbSetReplayBoundaryDomainFailure(t, p, c, b.Recorder)
			jsonbSetReplayBoundaryControls(t, p, b, c)
		})
	}
}
