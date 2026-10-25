package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var pgInheritsTypes = []string{"OID", "OID", "INT4", "BOOL"}

const pgInheritsSelect = "SELECT inhrelid,inhparent,inhseqno,inhdetachpending FROM pg_catalog.pg_inherits"

func pgInheritsScenario(t *testing.T, f func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { f(t, p, b) })
	}
}

func pgInheritsID(t *testing.T, p *fixture.Pair, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, name string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE oid=$1::regclass::oid", name).Scan(&id); err != nil || id <= 0 || id > 4294967295 {
		t.Fatal("relation identity", name, id, err)
	}
	return id
}

func pgInheritsEdge(child, parent int64, ordinal int64) []any {
	return []any{child, parent, ordinal, false}
}

// Expectations are supplied from independently captured pg_class identities,
// never reconstructed by reading pg_inherits itself. The candidate set includes
// negative controls so invented links cannot escape the complete bag assertion.
func pgInheritsBag(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, names []string, want [][]any) {
	t.Helper()
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = "'" + strings.ReplaceAll(name, "'", "''") + "'"
	}
	q := pgInheritsSelect + " WHERE inhrelid IN (SELECT oid FROM pg_catalog.pg_class WHERE relname IN (" + strings.Join(quoted, ",") + "))"
	for _, prepared := range []bool{false, true} {
		s := fixture.Select(q, fixture.PGInheritsColumns, pgInheritsTypes, want)
		s.Bag, s.Prepared = true, prepared
		fixture.OidReferenceRows(t, p.Ctx, b, db, s)
	}
}

func pgInheritsHeld(t *testing.T, p *fixture.Pair, db *sql.Conn) fixture.HeldStatement {
	t.Helper()
	q := pgInheritsSelect + " WHERE inhrelid=$1::oid ORDER BY inhseqno"
	stmt, err := db.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: db, Statement: stmt, SQL: q}
}

func pgInheritsHeldRows(t *testing.T, p *fixture.Pair, b fixture.Backend, held fixture.HeldStatement, id int64, want [][]any) {
	t.Helper()
	fixture.OidReferenceRows(t, p.Ctx, b, held, fixture.Select(held.SQL, fixture.PGInheritsColumns, pgInheritsTypes, want, id))
}

func pgInheritsMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, fixture.PGInheritsColumns) {
		t.Fatal("four labels", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 4 {
		t.Fatal("four descriptors", types, err)
	}
	for i, typ := range types {
		if typ.Name() != fixture.PGInheritsColumns[i] || typ.DatabaseTypeName() != pgInheritsTypes[i] {
			t.Fatal("descriptor", i, typ)
		}
		if nullable, known := typ.Nullable(); (!pg && (!known || nullable)) || (pg && known) {
			t.Fatal("nullability", i, nullable, known)
		}
		scan := reflect.TypeFor[int64]()
		if i == 3 {
			scan = reflect.TypeFor[bool]()
		}
		if pg && i < 2 {
			scan = reflect.TypeFor[any]()
		}
		if pg && i == 2 {
			scan = reflect.TypeFor[int32]()
		}
		if typ.ScanType() != scan {
			t.Fatal("ScanType", i, typ.ScanType(), scan)
		}
		if n, known := typ.Length(); n != 0 || known {
			t.Fatal("length", i, n, known)
		}
		if n, scale, known := typ.DecimalSize(); n != 0 || scale != 0 || known {
			t.Fatal("decimal", i, n, scale, known)
		}
	}
}
