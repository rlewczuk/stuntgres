package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceRangePartitionManualOwnedSequences(t *testing.T) {
	schemaReferenceCleanup(t, "rp_owned_wrong_schema")
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(step referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, step) }
		for _, query := range []string{
			"CREATE SEQUENCE rp_owned_root_seq",
			"CREATE SEQUENCE rp_owned_middle_seq",
			"CREATE TABLE rp_owned_root(k INT,payload BIGINT DEFAULT nextval('rp_owned_root_seq')) PARTITION BY RANGE(k)",
			"CREATE TABLE rp_owned_middle PARTITION OF rp_owned_root(payload DEFAULT nextval('rp_owned_middle_seq')) DEFAULT PARTITION BY RANGE(k)",
			"CREATE TABLE rp_owned_leaf PARTITION OF rp_owned_middle DEFAULT",
		} {
			run(referenceExec(query, 0))
		}
		run(referenceExec("ALTER SEQUENCE rp_owned_root_seq OWNED BY rp_owned_root.payload", 0))
		run(referencePrepared(referenceExec("ALTER SEQUENCE rp_owned_middle_seq OWNED BY rp_owned_middle.payload", 0)))
		run(referenceExec("CREATE SEQUENCE rp_owned_extra_seq OWNED BY rp_owned_root.k", 0))
		run(referenceExec("CREATE SCHEMA rp_owned_wrong_schema", 0))
		run(referenceExec("CREATE TABLE rp_owned_wrong_schema.rp_owned_other(k INT,payload BIGINT) PARTITION BY RANGE(k)", 0))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ query, code, message string }{
				{"ALTER SEQUENCE rp_owned_root_seq OWNED BY rp_owned_root.missing", "42703", `column "missing" of relation "rp_owned_root" does not exist`},
				{"ALTER SEQUENCE rp_owned_root_seq OWNED BY rp_owned_wrong_schema.rp_owned_other.payload", "55000", "sequence must be in same schema as table it is linked to"},
			} {
				step := referenceRangeError(tc.query, tc.code, tc.message, referenceDiagnostic{})
				step.prepared = prepared
				run(step)
			}
		}
		run(referenceExec("DROP SCHEMA rp_owned_wrong_schema CASCADE", 0))
		run(referenceExec("INSERT INTO rp_owned_root(k) VALUES(1)", 1))
		run(referencePrepared(referenceExec("INSERT INTO rp_owned_middle(k) VALUES($1)", 1, 2)))
		rows := referenceSelect("SELECT k,payload FROM rp_owned_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}})
		check := func(db referenceExecutor, record *referenceRecorder) {
			referenceRunStep(t, p.ctx, db, record, rows)
			referenceRunStep(t, p.ctx, db, record, referenceSelect("SELECT pg_get_serial_sequence('rp_owned_root','payload') AS root,pg_get_serial_sequence('rp_owned_middle','payload') AS middle,pg_get_serial_sequence('rp_owned_root','k') AS extra", []string{"root", "middle", "extra"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"public.rp_owned_root_seq", "public.rp_owned_middle_seq", "public.rp_owned_extra_seq"}}))
			referenceRunStep(t, p.ctx, db, record, referenceSelect("SELECT s.relname,d.refobjsubid,d.deptype AS kind FROM pg_depend d JOIN pg_class s ON s.oid=d.objid JOIN pg_class r ON r.oid=d.refobjid JOIN pg_tables t ON t.tablename=r.relname WHERE t.schemaname='public' AND (((s.relname='rp_owned_root_seq' OR s.relname='rp_owned_extra_seq') AND t.tablename='rp_owned_root') OR (s.relname='rp_owned_middle_seq' AND t.tablename='rp_owned_middle')) AND d.classid=1259 AND d.refclassid=1259 AND d.deptype='a' ORDER BY s.relname", []string{"relname", "refobjsubid", "kind"}, []string{"NAME", "INT4", "CHAR"}, [][]any{{"rp_owned_extra_seq", int64(1), "a"}, {"rp_owned_middle_seq", int64(2), "a"}, {"rp_owned_root_seq", int64(2), "a"}}))
		}
		check(b.db, b.recorder)
		if !b.pg {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(b.db, b.recorder)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("manual hierarchy ownership redump", err)
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
					for _, query := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP TABLE IF EXISTS rp_owned_root CASCADE", "DROP SCHEMA IF EXISTS rp_owned_wrong_schema CASCADE"} {
						if _, err := oracle.ExecContext(ctx, query); err != nil {
							t.Error(err)
						}
					}
				}()
				if _, err := oracle.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal("PostgreSQL rejected actual manual hierarchy sequence stream", err)
				}
				check(oracle, p.postgresRecord)
			}
		}
		run(referencePrepared(referenceExec("DROP TABLE rp_owned_middle", 0)))
		run(referenceSelect("SELECT COUNT(*) FROM rp_owned_root", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='rp_owned_middle' OR relname='rp_owned_leaf' OR relname='rp_owned_middle_seq'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(referenceRangeError("SELECT nextval('rp_owned_middle_seq')", "42P01", `relation "rp_owned_middle_seq" does not exist`, referenceDiagnostic{Position: "16"}))
		run(referenceSelect("SELECT nextval('rp_owned_root_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(referencePrepared(referenceExec("DROP TABLE rp_owned_root", 0)))
		run(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='rp_owned_root' OR relname='rp_owned_middle' OR relname='rp_owned_leaf' OR relname='rp_owned_root_seq' OR relname='rp_owned_middle_seq' OR relname='rp_owned_extra_seq'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(referenceRangeError("SELECT nextval('rp_owned_root_seq')", "42P01", `relation "rp_owned_root_seq" does not exist`, referenceDiagnostic{Position: "16"}))
	})
}
