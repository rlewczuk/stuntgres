package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRangePartitionSelfRestoreControls(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
		for _, query := range []string{
			"CREATE SEQUENCE rp_self_seq",
			"CREATE TABLE rp_self_root(k INT,payload INT NOT NULL DEFAULT nextval('rp_self_seq'),CONSTRAINT rp_self_positive CHECK(payload>0)) PARTITION BY RANGE((k+0))",
			"ALTER SEQUENCE rp_self_seq OWNED BY rp_self_root.payload",
			"CREATE TABLE rp_self_middle PARTITION OF rp_self_root DEFAULT PARTITION BY RANGE(k)",
			"CREATE TABLE rp_self_leaf PARTITION OF rp_self_middle(CONSTRAINT rp_self_unique UNIQUE(payload)) DEFAULT",
			"CREATE TABLE rp_self_coupled(payload INT DEFAULT nextval('rp_self_seq'),CHECK(payload>0))",
			"CREATE VIEW rp_self_view AS SELECT k,payload FROM rp_self_root",
		} {
			run(fixture.Exec(query, 0))
		}
		run(fixture.Exec("INSERT INTO rp_self_root(k) VALUES(1),(2)", 2))
		expected := fixture.Select("SELECT k,payload FROM rp_self_view ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}})
		run(expected)
		held, err := b.DB.PrepareContext(p.Ctx, expected.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		heldExecutor := fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: expected.Query}
		fixture.RunStep(t, p.Ctx, heldExecutor, b.Recorder, expected)
		if !b.PG {
			cursor, err := b.DB.QueryContext(p.Ctx, "SELECT k,payload FROM rp_self_root")
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			columns, err := cursor.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			var stream bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
				t.Fatal(err)
			}
			// Restore directly into the same open database: no Reset or replacement
			// driver hides coupled key/default/CHECK/view/index identity rebuilding.
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(stream.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, heldExecutor, b.Recorder, expected)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(stream.Bytes(), again.Bytes()) {
				t.Fatal("self Restore semantic redump", err)
			}
			invalid := stream.String() + "garbage;"
			diagnostic := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "garbage"`, Position: referenceRangePosition(invalid, "garbage")}
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(invalid)), &diagnostic)
			fixture.RunStep(t, p.Ctx, heldExecutor, b.Recorder, expected)
			fixture.TableDDLColumnTypes(t, false, columns, expected.Columns, expected.Types, []bool{true, false})
			var retained [][]any
			for cursor.Next() {
				var k, payload any
				if err := cursor.Scan(&k, &payload); err != nil {
					t.Fatal(err)
				}
				retained = append(retained, []any{k, payload})
			}
			if err := cursor.Err(); err != nil || !reflect.DeepEqual(retained, expected.Rows) {
				t.Fatal("self Restore changed retained prefixes", retained, err)
			}
			if p.Postgres != nil {
				oracle, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer oracle.Close()
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					for _, query := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP VIEW IF EXISTS rp_self_view", "DROP TABLE IF EXISTS rp_self_coupled", "DROP TABLE IF EXISTS rp_self_root CASCADE", "DROP SEQUENCE IF EXISTS rp_self_seq"} {
						if _, err := oracle.ExecContext(ctx, query); err != nil {
							t.Error(err)
						}
					}
				}()
				if _, err := oracle.ExecContext(p.Ctx, stream.String()); err != nil {
					t.Fatal("PostgreSQL rejected actual coupled self replacement stream", err)
				}
				fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, expected)
				fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, fixture.Select("SELECT pg_get_serial_sequence('rp_self_root','payload') AS owned", []string{"owned"}, []string{"TEXT"}, [][]any{{"public.rp_self_seq"}}))
			}
		}
		run(fixture.Prepared(fixture.Exec("INSERT INTO rp_self_root(k) VALUES($1)", 1, 3)))
		run(fixture.Exec("INSERT INTO rp_self_coupled DEFAULT VALUES", 1))
		expected.Rows = append(expected.Rows, []any{int64(3), int64(3)})
		fixture.RunStep(t, p.Ctx, heldExecutor, b.Recorder, expected)
		run(fixture.Select("SELECT payload FROM rp_self_coupled", []string{"payload"}, []string{"INT4"}, [][]any{{int64(4)}}))
		run(referenceRangeError("INSERT INTO rp_self_root VALUES(4,7),(5,1)", "23505", `duplicate key value violates unique constraint "rp_self_unique"`, fixture.Diagnostic{Detail: "Key (payload)=(1) already exists.", Schema: "public", Table: "rp_self_leaf", Constraint: "rp_self_unique"}))
		run(fixture.Prepared(referenceRangeError("INSERT INTO rp_self_root VALUES(4,0)", "23514", `new row for relation "rp_self_leaf" violates check constraint "rp_self_positive"`, fixture.Diagnostic{Detail: "Failing row contains (4, 0).", Schema: "public", Table: "rp_self_leaf", Constraint: "rp_self_positive"})))
		fixture.RunStep(t, p.Ctx, heldExecutor, b.Recorder, expected)
	})
}
