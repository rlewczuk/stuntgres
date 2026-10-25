package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pgNamespaceTypes = []string{"OID", "NAME", "OID", "_ACLITEM"}

func pgNamespaceScenario(t *testing.T, schemas, roles []string, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, roles)
	fixture.SchemaReferenceCleanup(t, schemas...)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func pgNamespaceExec(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Exec(q, 0))
	}
}

func pgNamespaceCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, s fixture.Step) {
	t.Helper()
	// The common catalog recorder checks raw OID/NAME/ACL carriers before normalization.
	fixture.PGDatabaseCheck(t, p, b, db, s)
}

func pgNamespaceID(t *testing.T, p *fixture.Pair, db fixture.Executor, name string) int64 {
	t.Helper()
	rows, err := db.QueryContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_namespace WHERE nspname=$1", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var id int64
	if !rows.Next() {
		t.Fatal("missing namespace", name, rows.Err())
	}
	if err := rows.Scan(&id); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("namespace identity multiplicity", rows.Err())
	}
	if id <= 0 || id > math.MaxUint32 {
		t.Fatalf("invalid namespace identity %d", id)
	}
	return id
}

func pgNamespaceRow(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, name string, id, owner int64, acl any) {
	t.Helper()
	s := fixture.Select("SELECT * FROM pg_catalog.pg_namespace WHERE nspname=$1", fixture.PGNamespaceColumns, pgNamespaceTypes, [][]any{{id, name, owner, acl}}, name)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgNamespaceMetadata(t, rows, b.PG) }
	pgNamespaceCheck(t, p, b, db, s)
	pgNamespaceCheck(t, p, b, db, fixture.Prepared(s))
}

func pgNamespaceMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, fixture.PGNamespaceColumns) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 4 {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		if typ.Name() != fixture.PGNamespaceColumns[i] || typ.DatabaseTypeName() != pgNamespaceTypes[i] {
			t.Fatal(i, typ.Name(), typ.DatabaseTypeName())
		}
		if nullable, known := typ.Nullable(); (pg && known) || (!pg && (!known || nullable != (i == 3))) {
			t.Fatal(i, nullable, known)
		}
		scan := reflect.TypeFor[string]()
		if i == 0 || i == 2 {
			scan = reflect.TypeFor[int64]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if typ.ScanType() != scan {
			t.Fatal(i, typ.ScanType(), scan)
		}
		if n, known := typ.Length(); n != 0 || known {
			t.Fatal(i, n, known)
		}
		if n, scale, known := typ.DecimalSize(); n != 0 || scale != 0 || known {
			t.Fatal(i, n, scale, known)
		}
	}
}

func pgNamespaceRoleID(t *testing.T, p *fixture.Pair, db fixture.Executor, name string) int64 {
	t.Helper()
	rows, err := db.QueryContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var id int64
	if !rows.Next() {
		t.Fatal("missing role", name, rows.Err())
	}
	if err := rows.Scan(&id); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("role identity multiplicity", rows.Err())
	}
	if id <= 0 || id > math.MaxUint32 {
		t.Fatal("invalid role identity", id)
	}
	return id
}

func pgNamespaceCurrentRole(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) (string, int64) {
	t.Helper()
	rows, err := db.QueryContext(p.Ctx, "SELECT current_user")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing current actor", rows.Err())
	}
	var name string
	if err := rows.Scan(&name); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("current actor multiplicity", rows.Err())
	}
	rows.Close()
	want := "postgres"
	if b.PG {
		want = "stuntgres"
	}
	if name != want {
		t.Fatal("fixture bootstrap actor", name, want)
	}
	id := pgNamespaceRoleID(t, p, db, name)
	if id != 10 {
		t.Fatal("fixture bootstrap identity", id)
	}
	return name, id
}
