package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var statioSequenceColumns = []string{"relid", "schemaname", "relname", "blks_read", "blks_hit"}
var statioSequenceTypes = []string{"OID", "NAME", "NAME", "INT8", "INT8"}

const statioSequenceProbe = "SELECT * FROM pg_catalog.pg_statio_all_sequences WHERE relid=$1::oid"

type statioSequenceRow struct {
	oid          int64
	schema, name string
	read, hit    int64
}

func statioSequenceMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, statioSequenceColumns) {
		t.Fatal("five PG18 columns", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 5 {
		t.Fatal("column types", types, err)
	}
	for i, typ := range types {
		wantScan := reflect.TypeFor[int64]()
		if i == 1 || i == 2 {
			wantScan = reflect.TypeFor[string]()
		}
		if pg && i < 3 {
			wantScan = reflect.TypeFor[any]()
		}
		if typ.Name() != names[i] || typ.DatabaseTypeName() != statioSequenceTypes[i] || typ.ScanType() != wantScan {
			t.Errorf("column %d: %q/%q/%v want %q/%q/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), names[i], statioSequenceTypes[i], wantScan)
		}
		nullable, known := typ.Nullable()
		if nullable != !pg || known != !pg {
			t.Errorf("nullable %d: %v/%v", i, nullable, known)
		}
		if n, ok := typ.Length(); n != 0 || ok {
			t.Errorf("length %d: %d/%v", i, n, ok)
		}
		if p, s, ok := typ.DecimalSize(); p != 0 || s != 0 || ok {
			t.Errorf("precision %d: %d/%d/%v", i, p, s, ok)
		}
	}
}

func statioSequenceRows(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, args ...any) []statioSequenceRow {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	statioSequenceMetadata(t, rows, b.PG)
	got := []statioSequenceRow{}
	for rows.Next() {
		var r statioSequenceRow
		if err := rows.Scan(&r.oid, &r.schema, &r.name, &r.read, &r.hit); err != nil {
			t.Fatal("non-NULL typed row", err)
		}
		if r.oid <= 0 || r.read < 0 || r.hit < 0 {
			t.Fatal("identity/nonnegative counters", r)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func statioSequenceOID(t *testing.T, p *fixture.Pair, e fixture.Executor, qualified string) int64 {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, "SELECT $1::regclass::oid", qualified)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var oid int64
	if !rows.Next() {
		t.Fatal("missing regclass", rows.Err())
	}
	if err := rows.Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || rows.Next() || rows.Err() != nil {
		t.Fatal("regclass identity", oid, rows.Err())
	}
	return oid
}

func statioSequenceCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, oid int64, schema, name string, read, hit int64) {
	t.Helper()
	got := statioSequenceRows(t, p, b, e, statioSequenceProbe, oid)
	if name == "" {
		if len(got) != 0 {
			t.Fatal("dropped identity retained", got)
		}
		return
	}
	if len(got) != 1 || got[0].oid != oid || got[0].schema != schema || got[0].name != name {
		t.Fatal("sequence membership", got, oid, schema, name)
	}
	// Physical PostgreSQL counters have asynchronous publication and are not the
	// in-memory engine's logical-page arithmetic.
	if !b.PG && (got[0].read != read || got[0].hit != hit) {
		t.Fatalf("logical counters %s: %d/%d want %d/%d", name, got[0].read, got[0].hit, read, hit)
	}
}

func statioSequenceExec(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
}

func TestReferencePGStatioAllSequencesShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			statioSequenceExec(t, p, b, b.DB, "CREATE SEQUENCE statio_shape")
			oid := statioSequenceOID(t, p, b.DB, "public.statio_shape")
			for _, q := range []string{"SELECT * FROM pg_catalog.pg_statio_all_sequences WHERE false", "SELECT * FROM pg_statio_all_sequences WHERE relname='statio_missing'", statioSequenceProbe} {
				args := []any{}
				if q == statioSequenceProbe {
					args = append(args, oid)
				}
				for _, prepared := range []bool{false, true} {
					var e fixture.Executor = b.DB
					if prepared {
						stmt, err := b.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						e = fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
					}
					got := statioSequenceRows(t, p, b, e, q, args...)
					if q == statioSequenceProbe {
						if len(got) != 1 || got[0].oid != oid || got[0].name != "statio_shape" || got[0].schema != "public" {
							t.Fatal(got)
						}
					} else if len(got) != 0 {
						t.Fatal("empty result", got)
					}
				}
			}
			s := fixture.Select("SELECT attnum,attname,atttypid,attnotnull FROM pg_attribute WHERE attrelid='pg_catalog.pg_statio_all_sequences'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull"}, []string{"INT2", "NAME", "OID", "BOOL"}, [][]any{{int64(1), "relid", int64(26), false}, {int64(2), "schemaname", int64(19), false}, {int64(3), "relname", int64(19), false}, {int64(4), "blks_read", int64(20), false}, {int64(5), "blks_hit", int64(20), false}})
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Prepared(s))
			q := "SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_statio_all_sequences'::regclass"
			s = fixture.Select(q, []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(5), true, true, true}})
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
			fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Prepared(s))
			if !b.PG {
				s = fixture.Select("SELECT c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_statio_all_sequences'::regclass", []string{"reltype", "typarray"}, []string{"OID", "OID"}, [][]any{{int64(12215), int64(12214)}})
				fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
				fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Prepared(s))
			}
			const definition = ` SELECT c.oid AS relid,
    n.nspname AS schemaname,
    c.relname,
    (pg_stat_get_blocks_fetched(c.oid) - pg_stat_get_blocks_hit(c.oid)) AS blks_read,
    pg_stat_get_blocks_hit(c.oid) AS blks_hit
   FROM (pg_class c
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = 'S'::"char");`
			s = fixture.Select("SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_statio_all_sequences'", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		})
	}
}

func TestReferencePGStatioAllSequencesMembership(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "statio_a")
	fixture.SchemaReferenceCleanup(t, "statio_b")
	fixture.SchemaReferenceCleanup(t, "PG_TOAST")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			for _, q := range []string{"CREATE SCHEMA statio_a", "CREATE SCHEMA statio_b", `CREATE SCHEMA "PG_TOAST"`, "CREATE SEQUENCE statio_a.same", "CREATE SEQUENCE statio_b.same", `CREATE SEQUENCE statio_a."Quoted Sequence"`, `CREATE SEQUENCE "PG_TOAST".statio_upper`, "CREATE TABLE statio_a.ordinary(id integer)", "CREATE INDEX statio_index ON statio_a.ordinary(id)", "CREATE VIEW statio_a.not_sequence AS SELECT id FROM statio_a.ordinary", "CREATE TABLE statio_a.serial_owner(id serial)", "CREATE TABLE statio_b.identity_owner(id bigint GENERATED ALWAYS AS IDENTITY)", "CREATE TEMP SEQUENCE statio_temp"} {
				statioSequenceExec(t, p, b, a, q)
			}
			q := "SELECT * FROM pg_catalog.pg_statio_all_sequences WHERE schemaname IN ('statio_a','statio_b','PG_TOAST') OR relname='statio_temp' ORDER BY schemaname,relname"
			want := map[string]int64{}
			for _, name := range []string{"statio_a.same", "statio_b.same", `statio_a."Quoted Sequence"`, `"PG_TOAST".statio_upper`, "statio_a.serial_owner_id_seq", "statio_b.identity_owner_id_seq", "pg_temp.statio_temp"} {
				want[name] = statioSequenceOID(t, p, a, name)
			}
			// The all-view includes another physical session's TEMP sequence too.
			for _, prepared := range []bool{false, true} {
				var e fixture.Executor = peer
				if prepared {
					stmt, err := peer.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					e = fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: q}
				}
				got := statioSequenceRows(t, p, b, e, q)
				seen := map[int64]bool{}
				for _, row := range got {
					if seen[row.oid] {
						t.Fatal("duplicate sequence", row)
					}
					seen[row.oid] = true
					found := false
					for _, oid := range want {
						if row.oid == oid {
							found = true
						}
					}
					if !found {
						t.Fatal("fabricated/nonsequence row", row)
					}
				}
				if len(seen) != len(want) {
					t.Fatal("incomplete all membership", got, want)
				}
			}
			pgSequencesScalar(t, p, b, a, "SELECT nextval('statio_a.same')", "nextval", 1)
			statioSequenceCheck(t, p, b, peer, want["statio_a.same"], "statio_a", "same", 1, 0)
			predicate := "schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast'"
			s := fixture.Select("SELECT relname FROM pg_statio_all_sequences WHERE ("+predicate+") AND (relname='statio_temp' OR schemaname IN ('statio_a','statio_b','PG_TOAST'))", []string{"relname"}, []string{"NAME"}, [][]any{})
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(s))
			// A literal truth table specifies the exact case-sensitive boundary,
			// without trying to create PostgreSQL-reserved schemas or adjacent views.
			s = fixture.Select("SELECT n,(n IN ('pg_catalog','information_schema') OR n ~ '^pg_toast') AS system,(n NOT IN ('pg_catalog','information_schema') AND n !~ '^pg_toast') AS user_sequence FROM (VALUES ('pg_catalog'),('information_schema'),('pg_toast'),('pg_toasty'),('PG_TOAST'),('public'),('custom'),('pg_temp_3')) AS v(n) ORDER BY n", []string{"n", "system", "user_sequence"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"PG_TOAST", false, true}, {"custom", false, true}, {"information_schema", true, false}, {"pg_catalog", true, false}, {"pg_temp_3", false, true}, {"pg_toast", true, false}, {"pg_toasty", true, false}, {"public", false, true}})
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(s))
			statioSequenceCatalogMembership(t, p, b, peer)
		})
	}
}

func TestReferencePGStatioAllSequencesVisibility(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "statio_private")
	fixture.RoleReferenceCleanup(t, []string{"statio_actor"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			defer a.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			for _, q := range []string{"CREATE ROLE statio_actor", "CREATE SCHEMA statio_private", "CREATE SEQUENCE statio_private.hidden", "REVOKE ALL ON SCHEMA statio_private FROM PUBLIC", "REVOKE ALL ON SEQUENCE statio_private.hidden FROM PUBLIC"} {
				statioSequenceExec(t, p, b, admin, q)
			}
			oid := statioSequenceOID(t, p, admin, "statio_private.hidden")
			statioSequenceExec(t, p, b, a, "SET SESSION AUTHORIZATION statio_actor")
			stmt, err := a.PrepareContext(p.Ctx, statioSequenceProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: statioSequenceProbe}
			statioSequenceCheck(t, p, b, a, oid, "statio_private", "hidden", 0, 0)
			statioSequenceCheck(t, p, b, held, oid, "statio_private", "hidden", 0, 0)
			// Numeric REGCLASS input isolates permission denial from schema/name
			// lookup through the ordinary supported sequence parameter route.
			s := fixture.ImplicitError("SELECT nextval($1::regclass)", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence hidden"}, fmt.Sprint(oid))
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Prepared(s))
			statioSequenceCheck(t, p, b, held, oid, "statio_private", "hidden", 0, 0)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT last_value FROM pg_sequences WHERE schemaname='statio_private' AND sequencename='hidden'", []string{"last_value"}, []string{"INT8"}, [][]any{{nil}}))
			statioSequenceCheck(t, p, b, held, oid, "statio_private", "hidden", 0, 0)
			statioSequenceExec(t, p, b, a, "RESET SESSION AUTHORIZATION")
		})
	}
}

func statioSequenceCatalogMembership(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, "SELECT c.oid,n.nspname,c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind='S'")
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]statioSequenceRow{}
	for rows.Next() {
		var r statioSequenceRow
		if err := rows.Scan(&r.oid, &r.schema, &r.name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		want[r.oid] = r
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	got := map[int64]statioSequenceRow{}
	for _, r := range statioSequenceRows(t, p, b, e, "SELECT * FROM pg_catalog.pg_statio_all_sequences") {
		if _, duplicate := got[r.oid]; duplicate {
			t.Fatal("duplicate all-view identity", r)
		}
		r.read, r.hit = 0, 0
		got[r.oid] = r
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("all-view membership differs from true SequenceRelation catalog set", got, want)
	}
}
