package enginetests_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

type copyProgressRuntime struct {
	core.EngineCatalogRuntime
	facts []pgcatalog.CopyProgressFact
}

func (r *copyProgressRuntime) VisitCopyProgress(ctx context.Context, visit func(pgcatalog.CopyProgressFact) error) error {
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

func TestCopyProgressTypedCapture(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ProgressCopyOID}
	source := view.MustRelation(address)
	if source.RowType != 12335 || len(source.Columns) != 11 || !source.VirtualView || pgcatalog.ClassKind(view, source) != "v" {
		t.Fatal("view/type identity", source)
	}
	runtime := &copyProgressRuntime{}
	input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Owner: view.Owner(), Runtime: runtime}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.ProgressCopyOID)
	if !ok {
		t.Fatal("missing producer")
	}
	if rows, err := produce(input); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("idle must be nonnil empty", rows, err)
	}
	runtime.facts = []pgcatalog.CopyProgressFact{
		{PID: 41, Database: 5, DatabaseName: "worker_database", DatabaseExists: true, Relation: 42, Command: "COPY FROM", BytesProcessed: 123, TuplesProcessed: 7, TuplesExcluded: 2, TuplesSkipped: 1, Visible: true},
		{PID: 43, Database: 5, DatabaseName: "worker_database", DatabaseExists: true, Relation: 42, Command: "COPY TO", Visible: false},
		{PID: 44, Database: 6, Relation: 0, Command: "COPY TO", Visible: true},
	}
	rows, err := produce(input)
	if err != nil || len(rows) != 3 {
		t.Fatal(rows, err)
	}
	for _, row := range rows {
		for i, v := range row {
			if v.Type() != source.Columns[i].Usage.Type || !v.ValidResult(source.Columns[i].Usage) {
				t.Fatalf("borrowed/incorrect cell %d", i)
			}
		}
	}
	for i, v := range rows[1] {
		if v.IsNull() != (i >= 3) {
			t.Fatalf("masked column%d null=%v", i, v.IsNull())
		}
	}
	if !rows[2][2].IsNull() {
		t.Fatal("missing database must be SQL NULL")
	}
	if relation, err := rows[2][3].OID(); err != nil || relation != 0 {
		t.Fatal("query relation zero", relation, err)
	}
	if total, err := rows[0][7].Integer(); err != nil || total != 0 {
		t.Fatal("unknown PIPE total", total, err)
	}
	runtime.facts[0].BytesProcessed = 999
	runtime.facts[0].TuplesProcessed = 99
	newer, err := produce(input)
	if err != nil {
		t.Fatal(err)
	}
	if old, _ := rows[0][6].Integer(); old != 123 {
		t.Fatal("retained byte snapshot changed", old)
	}
	if fresh, _ := newer[0][6].Integer(); fresh != 999 {
		t.Fatal("fresh snapshot failed to advance", fresh)
	}
	runtime.facts = nil
	if _, err := produce(input); err != nil {
		t.Fatal(err)
	}
	if old, _ := rows[0][8].Integer(); old != 7 {
		t.Fatal("cleanup altered retained tuple snapshot", old)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input.Context = ctx
	if rows, err := produce(input); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation precedence", rows, err)
	}
}

func TestCopyProgressViewDefinitionNames(t *testing.T) {
	for _, pretty := range []bool{false, true} {
		definition := pgcatalog.BuiltinViewDefinition(pgcatalog.ProgressCopyOID, pretty, func(name pgcatalog.BuiltinViewName) string {
			if name.IdentifierOnly {
				return `"` + name.Name + `"`
			}
			if name.Namespace != ids.PGCatalogNamespaceOID {
				t.Fatal("lost captured catalog namespace", name)
			}
			if name.Class == ids.ProcedureClassID && (name.Name != "pg_stat_get_progress_info" || name.Signature != "25" || name.RoutineOID == 0) {
				t.Fatal("lost exact backing routine identity", name)
			}
			return `"pg_catalog"."` + name.Name + `"`
		})
		for _, expected := range []string{
			`"s"."pid"`,
			`"pg_catalog"."pg_stat_get_progress_info"('COPY'::"pg_catalog"."text")`,
			`LEFT JOIN "pg_catalog"."pg_database" "d" ON`,
			`"s"."datid" = "d"."oid"`,
		} {
			if !strings.Contains(definition, expected) {
				t.Fatalf("pretty=%v lost typed qualification %q in %s", pretty, expected, definition)
			}
		}
	}
}
