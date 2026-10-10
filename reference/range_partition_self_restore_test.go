package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceRangePartitionSelfRestoreControls(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(step referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, step) }
		for _, query := range []string{
			"CREATE SEQUENCE rp_self_seq",
			"CREATE TABLE rp_self_root(k INT,payload INT NOT NULL DEFAULT nextval('rp_self_seq'),CONSTRAINT rp_self_positive CHECK(payload>0)) PARTITION BY RANGE((k+0))",
			"ALTER SEQUENCE rp_self_seq OWNED BY rp_self_root.payload",
			"CREATE TABLE rp_self_middle PARTITION OF rp_self_root DEFAULT PARTITION BY RANGE(k)",
			"CREATE TABLE rp_self_leaf PARTITION OF rp_self_middle(CONSTRAINT rp_self_unique UNIQUE(payload)) DEFAULT",
			"CREATE TABLE rp_self_coupled(payload INT DEFAULT nextval('rp_self_seq'),CHECK(payload>0))",
			"CREATE VIEW rp_self_view AS SELECT k,payload FROM rp_self_root",
		} {
			run(referenceExec(query, 0))
		}
		run(referenceExec("INSERT INTO rp_self_root(k) VALUES(1),(2)", 2))
		expected := referenceSelect("SELECT k,payload FROM rp_self_view ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}})
		run(expected)
		held, err := b.db.PrepareContext(p.ctx, expected.query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		heldExecutor := referenceHeldStatement{referenceExecutor: b.db, statement: held, query: expected.query}
		referenceRunStep(t, p.ctx, heldExecutor, b.recorder, expected)
		if !b.pg {
			cursor, err := b.db.QueryContext(p.ctx, "SELECT k,payload FROM rp_self_root")
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			columns, err := cursor.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			var stream bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &stream); err != nil {
				t.Fatal(err)
			}
			// Restore directly into the same open database: no Reset or replacement
			// driver hides coupled key/default/CHECK/view/index identity rebuilding.
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(stream.Bytes())); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, heldExecutor, b.recorder, expected)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || !bytes.Equal(stream.Bytes(), again.Bytes()) {
				t.Fatal("self Restore semantic redump", err)
			}
			invalid := stream.String() + "garbage;"
			diagnostic := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "garbage"`, Position: referenceRangePosition(invalid, "garbage")}
			referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString(invalid)), &diagnostic)
			referenceRunStep(t, p.ctx, heldExecutor, b.recorder, expected)
			referenceTableDDLColumnTypes(t, false, columns, expected.columns, expected.types, []bool{true, false})
			var retained [][]any
			for cursor.Next() {
				var k, payload any
				if err := cursor.Scan(&k, &payload); err != nil {
					t.Fatal(err)
				}
				retained = append(retained, []any{k, payload})
			}
			if err := cursor.Err(); err != nil || !reflect.DeepEqual(retained, expected.rows) {
				t.Fatal("self Restore changed retained prefixes", retained, err)
			}
			if p.postgres != nil {
				oracle, err := p.postgres.Conn(p.ctx)
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
				if _, err := oracle.ExecContext(p.ctx, stream.String()); err != nil {
					t.Fatal("PostgreSQL rejected actual coupled self replacement stream", err)
				}
				referenceRunStep(t, p.ctx, oracle, p.postgresRecord, expected)
				referenceRunStep(t, p.ctx, oracle, p.postgresRecord, referenceSelect("SELECT pg_get_serial_sequence('rp_self_root','payload') AS owned", []string{"owned"}, []string{"TEXT"}, [][]any{{"public.rp_self_seq"}}))
			}
		}
		run(referencePrepared(referenceExec("INSERT INTO rp_self_root(k) VALUES($1)", 1, 3)))
		run(referenceExec("INSERT INTO rp_self_coupled DEFAULT VALUES", 1))
		expected.rows = append(expected.rows, []any{int64(3), int64(3)})
		referenceRunStep(t, p.ctx, heldExecutor, b.recorder, expected)
		run(referenceSelect("SELECT payload FROM rp_self_coupled", []string{"payload"}, []string{"INT4"}, [][]any{{int64(4)}}))
		run(referenceRangeError("INSERT INTO rp_self_root VALUES(4,7),(5,1)", "23505", `duplicate key value violates unique constraint "rp_self_unique"`, referenceDiagnostic{Detail: "Key (payload)=(1) already exists.", Schema: "public", Table: "rp_self_leaf", Constraint: "rp_self_unique"}))
		run(referencePrepared(referenceRangeError("INSERT INTO rp_self_root VALUES(4,0)", "23514", `new row for relation "rp_self_leaf" violates check constraint "rp_self_positive"`, referenceDiagnostic{Detail: "Failing row contains (4, 0).", Schema: "public", Table: "rp_self_leaf", Constraint: "rp_self_positive"})))
		referenceRunStep(t, p.ctx, heldExecutor, b.recorder, expected)
	})
}
