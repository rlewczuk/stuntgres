package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioUserSequencesMembership(t *testing.T) {
	for _, schema := range []string{"statio_user_a", "statio_user_b", "statio_user_private", "PG_TOAST"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			owner, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			for _, q := range []string{
				"CREATE SCHEMA statio_user_a", "CREATE SCHEMA statio_user_b", "CREATE SCHEMA statio_user_private", `CREATE SCHEMA "PG_TOAST"`,
				"CREATE SEQUENCE public.statio_user_public", "CREATE SEQUENCE statio_user_a.same", "CREATE SEQUENCE statio_user_b.same",
				`CREATE SEQUENCE statio_user_a."Quoted Sequence"`, `CREATE SEQUENCE "PG_TOAST".statio_user_upper`,
				"CREATE SEQUENCE statio_user_private.hidden", "REVOKE ALL ON SCHEMA statio_user_private FROM PUBLIC",
				"CREATE TEMP SEQUENCE statio_user_temp", "CREATE TABLE statio_user_a.ordinary(id integer)",
				"CREATE INDEX statio_user_decoy_index ON statio_user_a.ordinary(id)", "CREATE VIEW statio_user_a.not_sequence AS SELECT id FROM statio_user_a.ordinary",
				"CREATE TABLE statio_user_a.serial_owner(id serial)", "CREATE TABLE statio_user_b.identity_owner(id bigint GENERATED ALWAYS AS IDENTITY)",
			} {
				statioSequenceExec(t, p, b, owner, q)
			}
			want := map[int64]statioSequenceRow{}
			for _, c := range []struct{ qualified, schema, name string }{
				{"public.statio_user_public", "public", "statio_user_public"},
				{"statio_user_a.same", "statio_user_a", "same"}, {"statio_user_b.same", "statio_user_b", "same"},
				{`statio_user_a."Quoted Sequence"`, "statio_user_a", "Quoted Sequence"}, {`"PG_TOAST".statio_user_upper`, "PG_TOAST", "statio_user_upper"},
				{"statio_user_private.hidden", "statio_user_private", "hidden"}, {"pg_temp.statio_user_temp", "", "statio_user_temp"},
				{"statio_user_a.serial_owner_id_seq", "statio_user_a", "serial_owner_id_seq"}, {"statio_user_b.identity_owner_id_seq", "statio_user_b", "identity_owner_id_seq"},
			} {
				oid := statioSequenceOID(t, p, owner, c.qualified)
				if c.schema == "" {
					if err := observer.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=$1::oid", oid).Scan(&c.schema); err != nil || !strings.HasPrefix(c.schema, "pg_temp_") {
						t.Fatal("other session's actual TEMP namespace", c.schema, err)
					}
				}
				want[oid] = statioSequenceRow{oid: oid, schema: c.schema, name: c.name}
			}
			stmt, err := observer.PrepareContext(p.Ctx, statioUserSequenceProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: observer, Statement: stmt, SQL: statioUserSequenceProbe}
			for oid, row := range want {
				statioUserSequenceWitness(t, p, b, observer, held, oid, row.schema, row.name, 0, 0)
			}
			const candidate = "n.nspname IN ('statio_user_a','statio_user_b','statio_user_private','PG_TOAST') OR c.relname IN ('statio_user_public','statio_user_temp')"
			catalogRows := [][]any{}
			for _, r := range want {
				catalogRows = append(catalogRows, []any{r.oid, r.schema, r.name})
			}
			s := fixture.Select("SELECT c.oid::bigint AS oid,n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind='S' AND ("+candidate+")", []string{"oid", "nspname", "relname"}, []string{"INT8", "NAME", "NAME"}, catalogRows)
			s.Bag = true
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Prepared(s))
			const query = "SELECT * FROM pg_catalog.pg_statio_user_sequences WHERE schemaname IN ('statio_user_a','statio_user_b','statio_user_private','PG_TOAST') OR relname IN ('statio_user_public','statio_user_temp')"
			for _, prepared := range []bool{false, true} {
				var e fixture.Executor = observer
				if prepared {
					bagStmt, err := observer.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer bagStmt.Close()
					e = fixture.HeldStatement{Executor: observer, Statement: bagStmt, SQL: query}
				}
				got := map[int64]statioSequenceRow{}
				for _, row := range statioSequenceRows(t, p, b, e, query) {
					if _, duplicate := got[row.oid]; duplicate {
						t.Fatal("duplicate user sequence", row)
					}
					row.read, row.hit = 0, 0
					got[row.oid] = row
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("complete actual sequence bag; no decoy/fabricated rows", got, want)
				}
			}
			// Creation and committed drop affect the named user view, not merely
			// a complementary predicate evaluated over the all view.
			dropped := statioSequenceOID(t, p, owner, "public.statio_user_public")
			statioSequenceExec(t, p, b, owner, "DROP SEQUENCE public.statio_user_public")
			statioUserSequenceWitness(t, p, b, observer, held, dropped, "", "", 0, 0)
		})
	}
}

func TestReferencePGStatioUserSequencesVisibility(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "statio_user_denied")
	fixture.RoleReferenceCleanup(t, []string{"statio_user_actor"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			actor, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			defer actor.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			for _, q := range []string{"CREATE ROLE statio_user_actor", "CREATE SCHEMA statio_user_denied", "CREATE SEQUENCE statio_user_denied.hidden CACHE 3", "REVOKE ALL ON SCHEMA statio_user_denied FROM PUBLIC", "REVOKE ALL ON SEQUENCE statio_user_denied.hidden FROM PUBLIC"} {
				statioSequenceExec(t, p, b, admin, q)
			}
			oid := statioSequenceOID(t, p, admin, "statio_user_denied.hidden")
			writer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			statioSequenceExec(t, p, b, writer, "BEGIN")
			for n := int64(1); n <= 4; n++ {
				pgSequencesScalar(t, p, b, writer, "SELECT nextval('statio_user_denied.hidden')", "nextval", n)
			}
			statioSequenceExec(t, p, b, writer, "COMMIT")
			statioSequenceRetire(t, writer)
			statioSequenceExec(t, p, b, actor, "SET SESSION AUTHORIZATION statio_user_actor")
			stmt, err := actor.PrepareContext(p.Ctx, statioUserSequenceProbe)
			if err != nil {
				t.Fatal("PUBLIC monitoring admission", err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: statioUserSequenceProbe}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT has_schema_privilege(current_user,'statio_user_denied','USAGE') AS schema_access,has_sequence_privilege(current_user,$1::oid,'USAGE') AS sequence_access,has_table_privilege(current_user,'pg_catalog.pg_statio_user_sequences','SELECT') AS monitor_access", []string{"schema_access", "sequence_access", "monitor_access"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, true}}, oid)
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Select("SELECT x.privilege_type FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) x WHERE c.oid='pg_catalog.pg_statio_user_sequences'::regclass AND x.grantee=0", []string{"privilege_type"}, []string{"TEXT"}, [][]any{{"SELECT"}}))
			statioUserSequenceWitness(t, p, b, actor, held, oid, "statio_user_denied", "hidden", 1, 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT nextval($1::regclass)", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence hidden"}, fmt.Sprint(oid))
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Select("SELECT last_value FROM pg_sequences WHERE schemaname='statio_user_denied' AND sequencename='hidden'", []string{"last_value"}, []string{"INT8"}, [][]any{{nil}}))
			statioUserSequenceWitness(t, p, b, actor, held, oid, "statio_user_denied", "hidden", 1, 1)
			statioSequenceExec(t, p, b, actor, "RESET SESSION AUTHORIZATION")
		})
	}
}
