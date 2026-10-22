package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const largeObjectMetadataSQL = "SELECT oid,lomowner,lomacl FROM pg_catalog.pg_largeobject_metadata WHERE oid=$1::oid"

func largeObjectMetadataStep(oid int64, rows [][]any) fixture.Step {
	return fixture.Select(largeObjectMetadataSQL, []string{"oid", "lomowner", "lomacl"}, []string{"OID", "OID", "_ACLITEM"}, rows, oid)
}

func largeObjectMetadataColumns(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 3 {
		t.Fatalf("metadata descriptors: got %d want 3", len(columns))
	}
	for i, column := range columns {
		names := []string{"oid", "lomowner", "lomacl"}
		types := []string{"OID", "OID", "_ACLITEM"}
		scan := reflect.TypeFor[int64]()
		if i == 2 {
			scan = reflect.TypeFor[string]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
		}
		nullable, known := column.Nullable()
		if column.Name() != names[i] || column.DatabaseTypeName() != types[i] || column.ScanType() != scan || known != !pg || nullable != (!pg && i == 2) {
			t.Fatalf("column %d: name=%s type=%s scan=%v nullable=%t/%t; want %s/%s/%v/%t/%t", i, column.Name(), column.DatabaseTypeName(), column.ScanType(), nullable, known, names[i], types[i], scan, !pg && i == 2, !pg)
		}
		if n, known := column.Length(); n != 0 || known {
			t.Fatalf("column %d length=%d/%t", i, n, known)
		}
		if precision, scale, known := column.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("column %d decimal=%d,%d/%t", i, precision, scale, known)
		}
	}
}

func TestReferenceLargeObjectMetadataShape(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910111)
		var ownerOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='og_object_other'").Scan(&ownerOID); err != nil {
			t.Fatal(err)
		}
		probe := func(want [][]any) {
			s := largeObjectMetadataStep(910111, want)
			var saved []*sql.ColumnType
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				var err error
				saved, err = rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				largeObjectMetadataColumns(t, saved, b.PG)
			}
			run(s)
			// ColumnType objects obtained while open remain usable after Close.
			largeObjectMetadataColumns(t, saved, b.PG)
		}
		probe(nil)
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(largeObjectValue("lo_create(910111::oid)", "OID", int64(910111)))
		})
		probe([][]any{{int64(910111), ownerOID, nil}})
	})
}

func TestReferenceLargeObjectMetadataVisibility(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910112)
		var ownerOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='og_object_other'").Scan(&ownerOID); err != nil {
			t.Fatal(err)
		}
		metadata := func(acl any) { run(largeObjectMetadataStep(910112, [][]any{{int64(910112), ownerOID, acl}})) }
		denied := func() {
			run(largeObjectError("SELECT lo_get(910112::oid)", "42501", "permission denied for large object 910112"))
			run(largeObjectError("SELECT lo_put(910112::oid,0::bigint,decode('','hex'))", "42501", "permission denied for large object 910112"))
		}
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(largeObjectValue("lo_from_bytea(910112::oid,decode('6100ff','hex'))", "OID", int64(910112)))
		})
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			metadata(nil)
			denied()
			run(largeObjectError("SELECT lo_unlink(910112::oid)", "42501", "must be owner of large object 910112"))
		})
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Exec("GRANT SELECT,UPDATE ON LARGE OBJECT 910112 TO og_object_reader WITH GRANT OPTION", 0))
		})
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			metadata("{og_object_other=rw/og_object_other,og_object_reader=r*w*/og_object_other}")
			run(largeObjectBytes(910112, "6100ff"))
			run(largeObjectPut(910112, 0, "6100ff"))
			run(largeObjectBytes(910112, "6100ff"))
		})
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910112 FROM og_object_reader", 0))
		})
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			metadata("{og_object_other=rw/og_object_other}")
			denied()
		})
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910112 FROM og_object_other", 0))
			metadata("{}")
			denied()
		})
		fixture.RoleGrantActor(run, "og_object_reader", func() { metadata("{}") })
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(largeObjectValue("lo_unlink(910112::oid)", "INT4", int64(1)))
		})
		run(largeObjectMetadataStep(910112, nil))
	})
}

func TestReferenceLargeObjectMetadataLifecycle(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910113)
		var ownerOID, readerOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='og_object_other'").Scan(&ownerOID); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='og_object_reader'").Scan(&readerOID); err != nil {
			t.Fatal(err)
		}
		stmt, err := c.PrepareContext(p.Ctx, largeObjectMetadataSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: largeObjectMetadataSQL}
		probe := func(want [][]any) {
			s := largeObjectMetadataStep(910113, want)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				largeObjectMetadataColumns(t, columns, b.PG)
			}
			// Keep the direct/per-step-prepared counterpart and the same actual
			// retained statement across all descriptor and role transitions.
			run(s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, largeObjectBackendStep(s, b))
		}
		probe(nil)
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(largeObjectValue("lo_from_bytea(910113::oid,decode('6100ff','hex'))", "OID", int64(910113)))
		})
		probe([][]any{{int64(910113), ownerOID, nil}})
		run(largeObjectBytes(910113, "6100ff"))
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Exec("GRANT SELECT,UPDATE ON LARGE OBJECT 910113 TO og_object_reader WITH GRANT OPTION", 0))
		})
		probe([][]any{{int64(910113), ownerOID, "{og_object_other=rw/og_object_other,og_object_reader=r*w*/og_object_other}"}})
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT 910113 FROM og_object_reader", 0))
		})
		probe([][]any{{int64(910113), ownerOID, "{og_object_other=rw/og_object_other}"}})
		fixture.RoleGrantActor(run, "og_object_other", func() { run(largeObjectValue("lo_unlink(910113::oid)", "INT4", int64(1))) })
		probe(nil)
		run(largeObjectMissing("SELECT lo_get(910113::oid)", 910113))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(largeObjectValue("lo_from_bytea(910113::oid,decode('7a','hex'))", "OID", int64(910113)))
		})
		probe([][]any{{int64(910113), readerOID, nil}})
		run(largeObjectBytes(910113, "7a"))
	})
}
