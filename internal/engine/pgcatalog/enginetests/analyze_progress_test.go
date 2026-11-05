package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

// Only projection is supplied facts; command work and membership use real core
// lifecycle tests and the unchanged public ANALYZE reference scenarios.
type analyzeProjectionRuntime struct {
	pgcatalog.Runtime
	facts []pgcatalog.AnalyzeProgressFact
}

func (r *analyzeProjectionRuntime) VisitAnalyzeProgress(ctx context.Context, visit func(pgcatalog.AnalyzeProgressFact) error) error {
	for _, f := range r.facts {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(f); err != nil {
			return err
		}
	}
	return nil
}

func TestAnalyzeProgressTypedCapture(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ProgressAnalyzeOID}
	source := view.MustRelation(address)
	if source.RowType != 12311 || len(source.Columns) != 13 || !source.VirtualView || pgcatalog.ClassKind(view, source) != "v" || source.History.HasIndexes || source.ReturnRuleOID() != pgcatalog.ProgressAnalyzeRuleOID {
		t.Fatal(source)
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 2 {
		t.Fatal("missing materialized owner/public ACL", acl)
	}
	owner, public := acl.Entries[0], acl.Entries[1]
	if owner.Grantor != source.Owner || owner.Grantee != (authorization.ACLGrantee{Role: source.Owner}) || owner.Privileges != authorization.TableGrantPrivileges || public.Grantor != source.Owner || public.Grantee != (authorization.ACLGrantee{Public: true}) || public.Privileges != authorization.SelectPrivilege {
		t.Fatal("bootstrap ACL lost exact owner/public policy", acl)
	}
	rowType := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12311})
	arrayType := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12310})
	if rowType.OwningRelation != address || arrayType.OwningRelation != address || rowType.Array != 12310 || arrayType.SQLType.Element() != rowType.SQLType || rowType.Owner != source.Owner || arrayType.Owner != source.Owner {
		t.Fatal("duplicate or unowned seeded row/array", rowType, arrayType)
	}
	names := []string{"pid", "datid", "datname", "relid", "phase", "sample_blks_total", "sample_blks_scanned", "ext_stats_total", "ext_stats_computed", "child_tables_total", "child_tables_done", "current_child_table_relid", "delay_time"}
	types := []sqltype.ID{23, 26, 19, 26, 25, 20, 20, 20, 20, 20, 20, 26, 701}
	for i, c := range source.Columns {
		if c.Name != names[i] || c.Usage.Type.ID() != types[i] || !c.Nullable || !c.NullabilityKnown || c.Usage.Typmod != -1 {
			t.Fatal(i, c)
		}
	}
	// Bound columns carry NAME's default collation even when ColumnSpec uses
	// zero to retain its carrier's usage; TEXT's explicit default stays distinct.
	for i, c := range source.Columns {
		collation := uint32(0)
		if i == 2 {
			collation = 950
		}
		if i == 4 {
			collation = 100
		}
		if c.Usage.Collation.ID != collation {
			t.Fatal(i, c)
		}
	}
	claims := 0
	for installedAddress, descriptor := range view.Descriptors() {
		if installedAddress.ObjectID == pgcatalog.ProgressAnalyzeRuleOID {
			t.Fatal("rule collides with descriptor", installedAddress)
		}
		if err := catalog.VisitDefinitionOIDs(descriptor, func(id ids.OID) error {
			if id == pgcatalog.ProgressAnalyzeRuleOID {
				claims++
				if descriptor != source {
					t.Fatal("foreign rule owner", descriptor)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if claims != 1 {
		t.Fatal("return rule ownership", claims)
	}
	runtime := &analyzeProjectionRuntime{}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: runtime}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.ProgressAnalyzeOID)
	if !ok {
		t.Fatal("missing producer")
	}
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	runtime.facts = []pgcatalog.AnalyzeProgressFact{
		{PID: 41, Database: 5, DatabaseExists: true, DatabaseName: "worker_database", Relation: 42, Phase: "initializing", Visible: true},
		{PID: 43, Database: 5, DatabaseExists: true, DatabaseName: "worker_database", Relation: 42, Phase: "computing extended statistics", ExtendedStatsTotal: 1, ExtendedStatsComputed: 2},
		{PID: 44, Database: 6, Relation: 45, Phase: "acquiring sample rows", SampleBlocksTotal: 2, SampleBlocksScanned: 1, Visible: true},
		{PID: 45, Database: 6, Relation: 46, Phase: "computing extended statistics", ExtendedStatsTotal: 1, ExtendedStatsComputed: 2, Visible: true},
	}
	rows, err := produce(input)
	if err != nil || len(rows) != 4 {
		t.Fatal(rows, err)
	}
	for _, row := range rows {
		for i, v := range row {
			if v.Type() != source.Columns[i].Usage.Type || !v.ValidResult(source.Columns[i].Usage) {
				t.Fatal(i, v)
			}
		}
	}
	for i, v := range rows[0] {
		if v.IsNull() {
			t.Fatal("authorized zero became NULL", i)
		}
	}
	for i, v := range rows[1] {
		if v.IsNull() != (i >= 3) {
			t.Fatal("public identity/redaction", i, v)
		}
	}
	for _, i := range []int{5, 6, 7, 8, 9, 10} {
		if n, err := rows[0][i].Integer(); err != nil || n != 0 {
			t.Fatal(i, n, err)
		}
	}
	if n, err := rows[0][11].OID(); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if n, err := rows[0][12].Float(); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if !rows[2][2].IsNull() || rows[2][3].IsNull() {
		t.Fatal("database LEFT JOIN miss masked unrelated fields")
	}
	if n, _ := rows[3][8].Integer(); n != 2 {
		t.Fatal("retained computed clamped by producer", n)
	}
	runtime.facts[2].SampleBlocksScanned = 2
	newer, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := rows[2][6].Integer()
	fresh, _ := newer[2][6].Integer()
	if old != 1 || fresh != 2 {
		t.Fatal("borrowed typed row backing", old, fresh)
	}
	copy := *source
	input.Source = &copy
	if _, err := produce(input); err == nil {
		t.Fatal("copied source admitted")
	}
	input.Source = source
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = ctx
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(rows, err)
	}
}
