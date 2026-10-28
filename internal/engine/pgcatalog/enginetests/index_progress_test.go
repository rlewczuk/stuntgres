package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

// Projection-only facts test typed SQL NULL handling independently of active
// command callbacks. Real work/lifetime tests live in core_test.
type createIndexProjectionRuntime struct {
	core.EngineCatalogRuntime
	facts []pgcatalog.CreateIndexProgressFact
}

func (r *createIndexProjectionRuntime) VisitCreateIndexProgress(ctx context.Context, visit func(pgcatalog.CreateIndexProgressFact) error) error {
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

func TestCreateIndexProgressTypedCapture(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ProgressCreateIndexOID}
	source := view.MustRelation(address)
	if source.RowType != 12326 || len(source.Columns) != 16 || !source.VirtualView || pgcatalog.ClassKind(view, source) != "v" || source.ReturnRuleOID() != pgcatalog.ProgressCreateIndexRuleOID {
		t.Fatal(source)
	}
	runtime := &createIndexProjectionRuntime{}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: runtime}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.ProgressCreateIndexOID)
	if !ok {
		t.Fatal("missing producer")
	}
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	runtime.facts = []pgcatalog.CreateIndexProgressFact{
		{PID: 41, Database: 5, DatabaseExists: true, DatabaseName: "worker_database", Relation: 42, Command: "CREATE INDEX", Phase: "initializing", Visible: true},
		{PID: 43, Database: 5, DatabaseExists: true, DatabaseName: "worker_database", Relation: 42, Index: 55, Command: "CREATE INDEX CONCURRENTLY", Phase: "waiting for writers before build", LockersTotal: 2, CurrentLockerPID: 44, Visible: false},
		{PID: 44, Database: 6, Relation: 45, Command: "CREATE INDEX", Phase: "building index: scanning table", BlocksTotal: 2, BlocksDone: 1, Visible: true},
	}
	rows, err := produce(input)
	if err != nil || len(rows) != 3 {
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
			t.Fatal("authorized initializing NULL", i)
		}
	}
	for i, v := range rows[1] {
		if v.IsNull() != (i >= 3) {
			t.Fatal("masked cell", i, v)
		}
	}
	for i := 7; i < 16; i++ {
		n, err := rows[0][i].Integer()
		if err != nil || n != 0 {
			t.Fatal("unknown/inapplicable is zero", i, n, err)
		}
	}
	if n, err := rows[0][4].OID(); err != nil || n != 0 {
		t.Fatal("ordinary index OID", n, err)
	}
	if !rows[2][2].IsNull() {
		t.Fatal("missing database lookup must be NULL")
	}
	runtime.facts[2].BlocksDone = 2
	newer, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := rows[2][11].Integer()
	fresh, _ := newer[2][11].Integer()
	if old != 1 || fresh != 2 {
		t.Fatal("borrowed result backing", old, fresh)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = ctx
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(rows, err)
	}
}
