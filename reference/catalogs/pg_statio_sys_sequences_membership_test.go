package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func statioSysSequenceEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, oid int64) {
	t.Helper()
	if got := statioSequenceRows(t, p, b, e, statioSysSequenceProbe, oid); len(got) != 0 {
		t.Fatal("user sequence included in system view", got)
	}
}

// Named all/user projections read the shared provider in one stable boundary.
func statioSysUserWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, oid int64, schema, name string, read, hit int64) {
	t.Helper()
	if name != "" {
		statioSequencePublishedRows(t, p, b, conn, statioSequenceProbe, oid, read, hit)
	}
	statioSequenceExec(t, p, b, conn, "BEGIN")
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	all := statioSequenceRows(t, p, b, conn, statioSequenceProbe, oid)
	user := statioSequenceRows(t, p, b, conn, statioUserSequenceProbe, oid)
	if !reflect.DeepEqual(all, user) {
		t.Fatal("named all and user views differ", all, user)
	}
	if name == "" {
		if len(all) != 0 {
			t.Fatal("dropped identity retained", all)
		}
	} else {
		want := statioSequenceRow{oid: oid, schema: schema, name: name, read: read, hit: hit}
		if len(all) != 1 || all[0].oid != oid || all[0].schema != schema || all[0].name != name {
			t.Fatal("actual user sequence identity", all, want)
		}
		if !b.PG && all[0] != want {
			t.Fatal("deterministic logical counters", all, want)
		}
	}
	statioSysSequenceEmpty(t, p, b, conn, oid)
	statioSequenceExec(t, p, b, conn, "COMMIT")
}

func TestReferencePGStatioSysSequencesMembership(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "statio_sys_private")
	fixture.SchemaReferenceCleanup(t, "PG_TOAST")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			producer, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			for _, q := range []string{
				"CREATE SCHEMA statio_sys_private", `CREATE SCHEMA "PG_TOAST"`,
				"CREATE SEQUENCE public.statio_sys_same CACHE 3", "CREATE SEQUENCE statio_sys_private.statio_sys_same CACHE 3",
				`CREATE SEQUENCE "PG_TOAST"."Quoted Sequence" CACHE 3`, "CREATE TEMP SEQUENCE statio_sys_temp CACHE 3",
				"CREATE TABLE statio_sys_private.ordinary(id integer)",
				"CREATE VIEW statio_sys_private.not_sequence AS SELECT id FROM statio_sys_private.ordinary",
			} {
				statioSequenceExec(t, p, b, producer, q)
			}
			// Keep setup's genuine heap-build work on a separate physical
			// writer; the retained TEMP owner's publication is awaited below.
			ddl, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer ddl.Close()
			statioSequenceExec(t, p, b, ddl, "CREATE INDEX statio_sys_index ON statio_sys_private.ordinary(id)")
			statioSequenceRetire(t, ddl)
			stmt, err := observer.PrepareContext(p.Ctx, statioSysSequenceProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: observer, Statement: stmt, SQL: statioSysSequenceProbe}
			for _, c := range []struct{ qualified, schema, name string }{
				{"public.statio_sys_same", "public", "statio_sys_same"},
				{"statio_sys_private.statio_sys_same", "statio_sys_private", "statio_sys_same"},
				{`"PG_TOAST"."Quoted Sequence"`, "PG_TOAST", "Quoted Sequence"},
				{"pg_temp.statio_sys_temp", "", "statio_sys_temp"},
			} {
				oid := statioSequenceOID(t, p, producer, c.qualified)
				schema := c.schema
				if schema == "" {
					if err := observer.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=$1::oid", oid).Scan(&schema); err != nil || !strings.HasPrefix(schema, "pg_temp_") {
						t.Fatal("actual temporary namespace", schema, err)
					}
				}
				statioSysUserWitness(t, p, b, observer, oid, schema, c.name, 0, 0)
				statioSysSequenceEmpty(t, p, b, held, oid)
				// Give each persistent sequence a real writer lifetime. TEMP
				// retains its owner and CACHE tail, so its ordinary publication
				// is observed with the bounded counter wait instead.
				writer := producer
				if c.schema != "" {
					writer, err = b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer writer.Close()
				}
				statioSequenceExec(t, p, b, writer, "BEGIN")
				for n := int64(1); n <= 4; n++ {
					fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Select("SELECT nextval($1::regclass)", []string{"nextval"}, []string{"INT8"}, [][]any{{n}}, fmt.Sprint(oid)))
				}
				statioSequenceExec(t, p, b, writer, "COMMIT")
				if writer != producer {
					statioSequenceRetire(t, writer)
				}
				statioSysUserWitness(t, p, b, observer, oid, schema, c.name, 1, 1)
				statioSysSequenceEmpty(t, p, b, held, oid)
				statioSequenceExec(t, p, b, producer, "DROP SEQUENCE "+c.qualified)
				statioSysUserWitness(t, p, b, observer, oid, "", "", 0, 0)
				statioSysSequenceEmpty(t, p, b, held, oid)
			}
			// Complete membership excludes decoys and fabricated identities, rather
			// than only proving that a particular user OID is absent.
			statioSequenceCatalogMembership(t, p, b, observer)
			if got := statioSequenceRows(t, p, b, observer, "SELECT * FROM pg_catalog.pg_statio_sys_sequences"); len(got) != 0 {
				t.Fatal("fabricated stock system sequence", got)
			}
			s := fixture.Select("SELECT n,(n IN ('pg_catalog','information_schema') OR n ~ '^pg_toast') AS system,(n NOT IN ('pg_catalog','information_schema') AND n !~ '^pg_toast') AS user_sequence FROM (VALUES ('pg_catalog'),('information_schema'),('pg_toast'),('pg_toast_42'),('pg_toasty'),('PG_TOAST'),('Pg_Catalog'),('information_schema_extra'),('public'),('custom'),('pg_temp_3')) AS v(n) ORDER BY n", []string{"n", "system", "user_sequence"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{
				{"PG_TOAST", false, true}, {"Pg_Catalog", false, true}, {"custom", false, true}, {"information_schema", true, false}, {"information_schema_extra", false, true},
				{"pg_catalog", true, false}, {"pg_temp_3", false, true}, {"pg_toast", true, false}, {"pg_toast_42", true, false}, {"pg_toasty", true, false}, {"public", false, true},
			})
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Prepared(s))
		})
	}
}

func TestReferencePGStatioSysSequencesVisibility(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "statio_sys_denied")
	fixture.RoleReferenceCleanup(t, []string{"statio_sys_actor"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			defer actor.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			for _, q := range []string{"CREATE ROLE statio_sys_actor", "CREATE SCHEMA statio_sys_denied", "CREATE SEQUENCE statio_sys_denied.hidden", "REVOKE ALL ON SCHEMA statio_sys_denied FROM PUBLIC", "REVOKE ALL ON SEQUENCE statio_sys_denied.hidden FROM PUBLIC"} {
				statioSequenceExec(t, p, b, admin, q)
			}
			oid := statioSequenceOID(t, p, admin, "statio_sys_denied.hidden")
			statioSequenceExec(t, p, b, actor, "SET SESSION AUTHORIZATION statio_sys_actor")
			stmt, err := actor.PrepareContext(p.Ctx, statioSysSequenceProbe)
			if err != nil {
				t.Fatal("PUBLIC SELECT admission", err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: statioSysSequenceProbe}
			statioSysUserWitness(t, p, b, actor, oid, "statio_sys_denied", "hidden", 0, 0)
			statioSysSequenceEmpty(t, p, b, held, oid)
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT nextval($1::regclass)", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence hidden"}, fmt.Sprint(oid))
				s.SelectRows = true
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
			}
			statioSysUserWitness(t, p, b, actor, oid, "statio_sys_denied", "hidden", 0, 0)
			statioSysSequenceEmpty(t, p, b, held, oid)
			statioSequenceExec(t, p, b, actor, "RESET SESSION AUTHORIZATION")
		})
	}
}
