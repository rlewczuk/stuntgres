package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const pgSeclabelSelect = "SELECT objoid,classoid,objsubid,provider,label FROM pg_catalog.pg_seclabel"

var pgSeclabelColumns = []string{"objoid", "classoid", "objsubid", "provider", "label"}
var pgSeclabelTypes = []string{"OID", "OID", "INT4", "TEXT", "TEXT"}

// Expectations come from PostgreSQL 18 pg_seclabel.h, gram.y, seclabel.c and
// utility.c, not from native output. No provider is installed by this fixture.
func pgSeclabelScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			t.Cleanup(func() {
				c.ExecContext(p.Ctx, "ROLLBACK")
				c.ExecContext(p.Ctx, "RESET ROLE")
				c.ExecContext(p.Ctx, "GRANT SELECT ON pg_catalog.pg_seclabel TO PUBLIC")
			})
			body(t, p, b, c)
		})
	}
}

func pgSeclabelExec(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 0))
	}
}

func pgSeclabelDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func pgSeclabelProvider(name string) fixture.Diagnostic {
	return pgSeclabelDiagnostic("22023", "security label provider \""+name+"\" is not loaded")
}

func pgSeclabelAbsent() fixture.Diagnostic {
	return pgSeclabelDiagnostic("22023", "no security label providers have been loaded")
}

func pgSeclabelError(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, want fixture.Diagnostic) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, want)
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
}

func pgSeclabelHeld(t *testing.T, p *fixture.Pair, x fixture.Executor, q string) fixture.HeldStatement {
	t.Helper()
	s, err := x.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("Prepare must not execute utility/provider resolution", q, err)
	}
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: x, Statement: s, SQL: q}
}

func pgSeclabelEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT count(*) FROM pg_catalog.pg_seclabel", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func pgSeclabelMetadata(b fixture.Backend, q string, args ...any) fixture.Step {
	s := fixture.Select(q, pgSeclabelColumns, pgSeclabelTypes, nil, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 5 {
			t.Fatal("five catalog descriptors", types, err)
		}
		for i, typ := range types {
			if typ.Name() != pgSeclabelColumns[i] || typ.DatabaseTypeName() != pgSeclabelTypes[i] {
				t.Fatal("catalog descriptor", i, typ)
			}
			if nullable, known := typ.Nullable(); b.PG && known || !b.PG && (!known || nullable) {
				t.Fatal("NOT NULL catalog descriptor", i, nullable, known)
			}
		}
	}
	return s
}

func pgSeclabelIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	// Bootstrap role names differ (oracle stuntgres, native postgres), but the
	// catalog must share pg_class's bootstrap owner, not the session actor.
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT c.oid::bigint,n.oid::bigint,c.relname::text,c.relkind::text,c.relpersistence::text,c.relhasindex,c.relowner=boot.relowner AS bootstrap_owner FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_class boot ON boot.oid=1259 WHERE c.oid='pg_catalog.pg_seclabel'::regclass", []string{"oid", "oid", "relname", "relkind", "relpersistence", "relhasindex", "bootstrap_owner"}, []string{"INT8", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(3596), int64(11), "pg_seclabel", "r", "p", true, true}}))
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT t.typname::text,t.typtype::text,t.typrelid=c.oid AS owns_relation,a.typname::text,a.typelem=t.oid AS owns_element FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_seclabel'::regclass", []string{"typname", "typtype", "owns_relation", "typname", "owns_element"}, []string{"TEXT", "TEXT", "BOOL", "TEXT", "BOOL"}, [][]any{{"pg_seclabel", "c", true, "_pg_seclabel", true}}))
}

func TestReferencePgSeclabelMetadata(t *testing.T) {
	pgSeclabelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{pgSeclabelSelect, "SELECT * FROM pg_seclabel", pgSeclabelSelect + " WHERE FALSE", pgSeclabelSelect + " LIMIT 0"} {
				s := pgSeclabelMetadata(b, q)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		held := pgSeclabelHeld(t, p, c, pgSeclabelSelect+" WHERE provider=$1")
		for _, arg := range []any{"missing", "selinux", nil, "missing"} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgSeclabelMetadata(b, held.SQL, arg))
		}
		pgSeclabelIdentity(t, p, b, c)
		var want [][]any
		for i, name := range pgSeclabelColumns {
			oid, collation := int64(26), int64(0)
			if i == 2 { oid = 23 }
			if i > 2 { oid, collation = 25, 950 }
			want = append(want, []any{int64(i + 1), name, oid, true, int64(-1), int64(0), collation})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT attnum::int4,attname::text,atttypid::bigint,attnotnull,atttypmod,attndims,attcollation::bigint FROM pg_catalog.pg_attribute WHERE attrelid='pg_catalog.pg_seclabel'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attndims", "attcollation"}, []string{"INT4", "TEXT", "INT8", "BOOL", "INT4", "INT2", "INT8"}, want))
		rows, err := c.QueryContext(p.Ctx, pgSeclabelSelect)
		if err != nil { t.Fatal(err) }
		types, err := rows.ColumnTypes()
		if err != nil { rows.Close(); t.Fatal(err) }
		if rows.Next() || rows.Err() != nil { rows.Close(); t.Fatal("expected clean empty EOF", rows.Err()) }
		if err := rows.Close(); err != nil { t.Fatal(err) }
		var names []string
		for _, typ := range types { names = append(names, typ.Name()) }
		if !reflect.DeepEqual(names, pgSeclabelColumns) { t.Fatal("retained metadata", names) }
		pgSeclabelEmpty(t, p, b, c)
	})
}
