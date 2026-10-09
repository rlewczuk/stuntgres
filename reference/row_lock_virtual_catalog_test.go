package reference_test

import (
	"strconv"
	"testing"
)

func TestReferenceRowLockVirtualCatalogBoundary(t *testing.T) {
	for _, clause := range referenceRowClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceViewFixture(t)
				for _, catalog := range []struct{ source, suffix string }{{"pg_catalog.pg_class", "class"}, {"pg_catalog.pg_rewrite", "rewrite"}, {"pg_catalog.pg_views", "views"}} {
					direct, nested, stored := "virtual_"+catalog.suffix, "nested_"+catalog.suffix, "stored_"+catalog.suffix
					for _, q := range []string{"CREATE VIEW " + direct + " AS SELECT * FROM " + catalog.source, "CREATE VIEW " + nested + " AS SELECT * FROM " + direct, "CREATE VIEW " + stored + " AS SELECT * FROM " + direct + " " + clause} {
						referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(q, 0))
					}
					for _, q := range []string{"SELECT * FROM " + catalog.source + " WHERE FALSE " + clause, "SELECT * FROM " + nested + " WHERE FALSE " + clause, "SELECT * FROM " + stored + " WHERE FALSE", "SELECT t.id FROM t CROSS JOIN " + nested + " WHERE FALSE " + clause} {
						failure := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
						failure.selectRows, failure.prepared = true, prepared
						failure.errorStage = "execute"
						referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, failure)
					}
				}
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
				for _, backend := range p.backends() {
					if backend.pg {
						referenceViewPhysicalCatalog(t, p, backend, clause, prepared)
					}
				}
			})
		}
	}
}

func referenceViewPhysicalCatalog(t *testing.T, p *referencePair, backend referenceBackend, clause string, prepared bool) {
	t.Helper()
	a, b := referenceLockConnections(t, p.ctx, backend.db)
	var oid int64
	if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='t' AND relnamespace='public'::regnamespace").Scan(&oid); err != nil || oid <= 0 {
		t.Fatal(oid, err)
	}
	holder, err := a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	query := "SELECT oid FROM pg_catalog.pg_class WHERE oid=" + strconv.FormatInt(oid, 10) + " " + clause
	step := referenceSelect(query, []string{"oid"}, []string{"OID"}, [][]any{{strconv.FormatInt(oid, 10)}})
	step.prepared = prepared
	referenceRunStep(t, p.ctx, holder, backend.recorder, step)
	referenceRunStep(t, p.ctx, b, backend.recorder, referenceSelect("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
}
