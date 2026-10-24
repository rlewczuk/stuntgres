package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsAlter(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
				base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, s := range []fixture.Step{
					fixture.Exec("CREATE SCHEMA txv_dest", 0),
					fixture.Exec("CREATE TABLE txv_source(id INT,txt TEXT)", 0),
					fixture.Exec("INSERT INTO txv_source VALUES(1,'a'),(1,'a'),(2,NULL)", 3),
					fixture.Exec("CREATE VIEW txv_v AS SELECT id,txt FROM txv_source", 0),
					fixture.Exec("CREATE VIEW txv_dependent AS SELECT id FROM txv_v", 0),
					fixture.Exec("CREATE MATERIALIZED VIEW txv_m AS SELECT id,txt FROM txv_source", 3),
					fixture.Exec("CREATE INDEX txv_index ON txv_m(id)", 0),
					fixture.Exec("GRANT SELECT ON txv_v,txv_m TO PUBLIC", 0),
				} {
					base(s)
				}
				v := transactionalViewsIdentity(t, p, c, "txv_v")
				m := transactionalViewsIdentity(t, p, c, "txv_m")
				var index int64
				if err := c.QueryRowContext(p.Ctx, "SELECT 'txv_index'::regclass::BIGINT").Scan(&index); err != nil {
					t.Fatal(err)
				}
				queries := []string{
					"CREATE OR REPLACE VIEW txv_v AS SELECT id+10 AS id,txt,true AS flag FROM txv_source",
					"ALTER VIEW txv_v SET(security_barrier=true,security_invoker=true)",
					"ALTER VIEW txv_v RESET(security_invoker)",
					"ALTER VIEW txv_v OWNER TO CURRENT_USER",
					"ALTER VIEW txv_v RENAME COLUMN id TO key",
					"ALTER VIEW txv_v RENAME TO txv_renamed",
					"ALTER VIEW txv_renamed SET SCHEMA txv_dest",
					"ALTER MATERIALIZED VIEW txv_m OWNER TO CURRENT_USER",
					"ALTER MATERIALIZED VIEW txv_m RENAME COLUMN id TO key",
					"ALTER MATERIALIZED VIEW txv_m RENAME TO txv_saved",
					"ALTER MATERIALIZED VIEW txv_saved SET SCHEMA txv_dest",
				}
				executors := make([]fixture.Executor, len(queries))
				for i, q := range queries {
					executors[i] = c
					if held {
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal("effect-free Prepare", q, err)
						}
						defer stmt.Close()
						executors[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
				}
				e, finish := transactionalViewsBegin(t, p, b, c, mode)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
				for i, q := range queries {
					fixture.RunStep(t, p.Ctx, executors[i], b.Recorder, fixture.Exec(q, 0))
				}
				if got := transactionalViewsIdentity(t, p, e, "txv_dest.txv_renamed"); !reflect.DeepEqual(got, v) {
					t.Fatalf("view identity %v -> %v", v, got)
				}
				if got := transactionalViewsIdentity(t, p, e, "txv_dest.txv_saved"); !reflect.DeepEqual(got, m) {
					t.Fatalf("materialized identity %v -> %v", m, got)
				}
				run(fixture.Select("SELECT id FROM txv_dependent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(11)}, {int64(12)}}))
				run(fixture.Select("SELECT key,txt,flag FROM txv_dest.txv_renamed ORDER BY key", []string{"key", "txt", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(11), "a", true}, {int64(11), "a", true}, {int64(12), nil, true}}))
				bag := transactionalViewsBag("txv_dest.txv_saved", false)
				bag.Query = "SELECT key AS id,txt FROM txv_dest.txv_saved"
				run(bag)
				fixture.MaterializedViewPopulation(run, "txv_saved", true)
				run(fixture.Select("SELECT oid::BIGINT AS oid FROM pg_class WHERE oid=$1 AND relnamespace='txv_dest'::regnamespace", []string{"oid"}, []string{"INT8"}, [][]any{{index}}, index))
				run(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid=$1 AND attnum>0 ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"key", int64(1)}, {"txt", int64(2)}, {"flag", int64(3)}}, v[0]))
				run(fixture.Select("SELECT count(*) FROM pg_class WHERE oid IN ($1,$2) AND relacl IS NOT NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, v[0], m[0]))
				if mode != "autocommit" {
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalViewsCount("txv_v", 1))
				}
				finish(commit)
				if !commit && mode != "autocommit" {
					base(transactionalViewsBag("txv_v", false))
					base(transactionalViewsBag("txv_m", false))
					if got := transactionalViewsIdentity(t, p, c, "txv_v"); !reflect.DeepEqual(v, got) {
						t.Fatal(v, got)
					}
					if got := transactionalViewsIdentity(t, p, c, "txv_m"); !reflect.DeepEqual(m, got) {
						t.Fatal(m, got)
					}
					base(fixture.Select("SELECT reloptions IS NULL AS clean FROM pg_class WHERE oid=$1", []string{"clean"}, []string{"BOOL"}, [][]any{{true}}, v[0]))
				} else {
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT id FROM txv_dependent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(11)}, {int64(12)}}))
				}
			})
		})
	}
}
