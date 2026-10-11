package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRangePartitionManualOwnedSequences(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rp_owned_wrong_schema")
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
		for _, query := range []string{
			"CREATE SEQUENCE rp_owned_root_seq",
			"CREATE SEQUENCE rp_owned_middle_seq",
			"CREATE TABLE rp_owned_root(k INT,payload BIGINT DEFAULT nextval('rp_owned_root_seq')) PARTITION BY RANGE(k)",
			"CREATE TABLE rp_owned_middle PARTITION OF rp_owned_root(payload DEFAULT nextval('rp_owned_middle_seq')) DEFAULT PARTITION BY RANGE(k)",
			"CREATE TABLE rp_owned_leaf PARTITION OF rp_owned_middle DEFAULT",
		} {
			run(fixture.Exec(query, 0))
		}
		run(fixture.Exec("ALTER SEQUENCE rp_owned_root_seq OWNED BY rp_owned_root.payload", 0))
		run(fixture.Prepared(fixture.Exec("ALTER SEQUENCE rp_owned_middle_seq OWNED BY rp_owned_middle.payload", 0)))
		run(fixture.Exec("CREATE SEQUENCE rp_owned_extra_seq OWNED BY rp_owned_root.k", 0))
		run(fixture.Exec("CREATE SCHEMA rp_owned_wrong_schema", 0))
		run(fixture.Exec("CREATE TABLE rp_owned_wrong_schema.rp_owned_other(k INT,payload BIGINT) PARTITION BY RANGE(k)", 0))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ query, code, message string }{
				{"ALTER SEQUENCE rp_owned_root_seq OWNED BY rp_owned_root.missing", "42703", `column "missing" of relation "rp_owned_root" does not exist`},
				{"ALTER SEQUENCE rp_owned_root_seq OWNED BY rp_owned_wrong_schema.rp_owned_other.payload", "55000", "sequence must be in same schema as table it is linked to"},
			} {
				step := referenceRangeError(tc.query, tc.code, tc.message, fixture.Diagnostic{})
				step.Prepared = prepared
				run(step)
			}
		}
		run(fixture.Exec("DROP SCHEMA rp_owned_wrong_schema CASCADE", 0))
		run(fixture.Exec("INSERT INTO rp_owned_root(k) VALUES(1)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO rp_owned_middle(k) VALUES($1)", 1, 2)))
		rows := fixture.Select("SELECT k,payload FROM rp_owned_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}})
		check := func(db fixture.Executor, record *fixture.Recorder) {
			fixture.RunStep(t, p.Ctx, db, record, rows)
			fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT pg_get_serial_sequence('rp_owned_root','payload') AS root,pg_get_serial_sequence('rp_owned_middle','payload') AS middle,pg_get_serial_sequence('rp_owned_root','k') AS extra", []string{"root", "middle", "extra"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"public.rp_owned_root_seq", "public.rp_owned_middle_seq", "public.rp_owned_extra_seq"}}))
			fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT s.relname,d.refobjsubid,d.deptype AS kind FROM pg_depend d JOIN pg_class s ON s.oid=d.objid JOIN pg_class r ON r.oid=d.refobjid JOIN pg_tables t ON t.tablename=r.relname WHERE t.schemaname='public' AND (((s.relname='rp_owned_root_seq' OR s.relname='rp_owned_extra_seq') AND t.tablename='rp_owned_root') OR (s.relname='rp_owned_middle_seq' AND t.tablename='rp_owned_middle')) AND d.classid=1259 AND d.refclassid=1259 AND d.deptype='a' ORDER BY s.relname", []string{"relname", "refobjsubid", "kind"}, []string{"NAME", "INT4", "CHAR"}, [][]any{{"rp_owned_extra_seq", int64(1), "a"}, {"rp_owned_middle_seq", int64(2), "a"}, {"rp_owned_root_seq", int64(2), "a"}}))
		}
		check(b.DB, b.Recorder)
		if !b.PG {
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
			check(b.DB, b.Recorder)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("manual hierarchy ownership redump", err)
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
					for _, query := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP TABLE IF EXISTS rp_owned_root CASCADE", "DROP SCHEMA IF EXISTS rp_owned_wrong_schema CASCADE"} {
						if _, err := oracle.ExecContext(ctx, query); err != nil {
							t.Error(err)
						}
					}
				}()
				if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("PostgreSQL rejected actual manual hierarchy sequence stream", err)
				}
				check(oracle, p.PostgresRecord)
			}
		}
		run(fixture.Prepared(fixture.Exec("DROP TABLE rp_owned_middle", 0)))
		run(fixture.Select("SELECT COUNT(*) FROM rp_owned_root", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='rp_owned_middle' OR relname='rp_owned_leaf' OR relname='rp_owned_middle_seq'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(referenceRangeError("SELECT nextval('rp_owned_middle_seq')", "42P01", `relation "rp_owned_middle_seq" does not exist`, fixture.Diagnostic{Position: "16"}))
		run(fixture.Select("SELECT nextval('rp_owned_root_seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(fixture.Prepared(fixture.Exec("DROP TABLE rp_owned_root", 0)))
		run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='rp_owned_root' OR relname='rp_owned_middle' OR relname='rp_owned_leaf' OR relname='rp_owned_root_seq' OR relname='rp_owned_middle_seq' OR relname='rp_owned_extra_seq'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(referenceRangeError("SELECT nextval('rp_owned_root_seq')", "42P01", `relation "rp_owned_root_seq" does not exist`, fixture.Diagnostic{Position: "16"}))
	})
}
