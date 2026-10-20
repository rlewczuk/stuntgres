package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Exercise the private binder/stager directly, not SQL execution wrappers.
func materializedUnitStage(t *testing.T, e *Engine) (*Operation, func(), *CatalogStage, *catalog.Relation) {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	statements, err := parser.Parse("CREATE MATERIALIZED VIEW public.unit_mv AS SELECT 1::int AS i WITH NO DATA")
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*parser.CreateQueryTable)
	input, _ := sessionPresentation(op.session)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	b, err := describe(context.Background(), e.analysisHost(op), s, auth, ids.ObjectRef{}, input, nil, nil)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	stage, descriptor, skipped, err := e.prepareQueryTableLocked(op, auth, s, b)
	e.mu.Unlock()
	if err != nil || skipped {
		t.Fatalf("private stage: %v, skipped %v", err, skipped)
	}
	return op, done, stage, descriptor
}

func TestMaterializedViewDescriptor(t *testing.T) {
	e := New()
	_, done, stage, d := materializedUnitStage(t, e)
	defer done()
	view := e.stageRead(stage)
	if !d.RelationKind.Stored() || d.Populated || d.View == nil || viewOf(d).rule == 0 {
		t.Fatal("missing independent storage/definition/population identity")
	}
	if err := (relationProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	populated := *d
	populated.Populated = true
	if err := (relationProvider{}).Validate(view, &populated); err != nil {
		t.Fatal("empty populated descriptor:", err)
	}
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.ViewRelation, catalog.CatalogRelation} {
		invalid := populated
		invalid.RelationKind = kind
		if err := (relationProvider{}).Validate(view, &invalid); err == nil {
			t.Fatalf("accepted populated non-materialization %v", kind)
		}
	}
	invalid := *d
	invalid.View = nil
	if err := (relationProvider{}).Validate(view, &invalid); err == nil {
		t.Fatal("accepted missing definition")
	}
}

func TestMaterializedViewInitialStore(t *testing.T) {
	e := New()
	_, done, stage, d := materializedUnitStage(t, e)
	defer done()
	d.Populated = true
	value, err := sqltype.Integer(d.Columns[0].Usage.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	rows := [][]sqltype.Value{{value}}
	if err := e.stageQueryTableRows(context.Background(), stage, d, rows, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	store := stage.initialTables[d.Address()]
	if &store.Versions[0].Values[0] != &rows[0][0] {
		t.Fatal("finalized row buffer copied")
	}
	if err := stage.validateInitialTables(); err != nil {
		t.Fatal(err)
	}
	d.Populated = false
	if err := stage.validateInitialTables(); err == nil {
		t.Fatal("unpopulated store accepted rows")
	}
	d.Populated, d.RelationKind = false, catalog.ViewRelation
	if err := stage.validateInitialTables(); err == nil {
		t.Fatal("dynamic view accepted physical rows")
	}
}

func TestMaterializedViewDefinition(t *testing.T) {
	e := New()
	op, done, stage, d := materializedUnitStage(t, e)
	defer done()
	source := &catalog.Relation{ObjectNamespace: d.Namespace(), Attributes: []int32{1, 3}}
	ref := e.catalog.Domain.Ref(stage.Base.Epoch(), d.Address())
	b := analyze.Binding{Query: viewOf(d).query, QuerySources: []exec.Leaf{{Ref: ref, Relation: source, Uses: []int{1}}}, Dependencies: []catalog.ExpressionDependency{{Ref: ref, Revision: d.Revision()}}}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	definition, err := e.freezeViewDefinitionLocked(auth, viewOf(d).syntax, b, false, false, 0, catalog.MaterializedViewRelation, false)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	source.Attributes[1] = 9
	if definition.query != b.Query || definition.syntax != viewOf(d).syntax || definition.rule == 0 || definition.rule == viewOf(d).rule {
		t.Fatal("definition rebound or lost fresh rule identity")
	}
	if !reflect.DeepEqual(definition.sources[0].attributes, []int32{1, 3}) || !reflect.DeepEqual(definition.sources[0].reads, []int32{3}) || definition.dependencies[0] != b.Dependencies[0] {
		t.Fatal("frozen hidden-attribute/source dependencies changed")
	}
}

func TestMaterializedViewSnapshotCodec(t *testing.T) {
	encode := func(raw string) string {
		return materializedSnapshotMarker + " " + base64.StdEncoding.EncodeToString([]byte(raw))
	}
	raw := `{"schema":"quote\"\\雪","name":"newline\n*/--","populated":true,"rows":"INSERT INTO public.unit_mv VALUES (NULL);\n"}`
	record, recognized, err := decodeMaterializedSnapshot(encode(raw))
	if err != nil || !recognized || record.Schema != "quote\"\\雪" || record.Name != "newline\n*/--" || record.Rows != "INSERT INTO public.unit_mv VALUES (NULL);\n" {
		t.Fatalf("lossy codec: %#v %v", record, err)
	}
	if _, recognized, err := decodeMaterializedSnapshot("ordinary comment"); recognized || err != nil {
		t.Fatal("ordinary comment gained import authority")
	}
	for _, raw := range []string{
		`{"schema":"public","name":"unit_mv","rows":""}`,
		`{"schema":"public","name":"unit_mv","populated":true,"rows":"","extra":1}`,
		`{"schema":"public","name":"unit_mv","populated":true,"rows":"","name":"again"}`,
		`{"schema":"public","name":"unit_mv","populated":null,"rows":""}`,
		`{"schema":"public","name":"unit_mv","populated":false,"rows":"INSERT"}`,
		`{"schema":"public","name":"unit_mv","populated":true,"rows":""} {}`,
		`{"schema":"","name":"unit_mv","populated":true,"rows":""}`,
	} {
		if _, recognized, err := decodeMaterializedSnapshot(encode(raw)); !recognized || err == nil {
			t.Fatalf("accepted malformed envelope %s", raw)
		}
	}
	for _, text := range []string{"stuntgres-matview-v2 e30=", "stuntgres-matview-v1 !!!", "stuntgres-matview-v1 " + base64.StdEncoding.EncodeToString([]byte{0xff})} {
		if _, recognized, err := decodeMaterializedSnapshot(text); !recognized || err == nil {
			t.Fatalf("accepted framing %q", text)
		}
	}
}

func TestMaterializedViewSnapshotImport(t *testing.T) {
	for _, rows := range []string{
		"INSERT INTO public.other VALUES (1);",
		"INSERT INTO public.unit_mv (i) VALUES (1);",
		"INSERT INTO public.unit_mv VALUES (1,2);",
		"INSERT INTO public.unit_mv VALUES ('bad');",
		"INSERT INTO public.unit_mv SELECT 1;",
		"INSERT INTO public.unit_mv VALUES ($1);",
		"INSERT INTO public.unit_mv DEFAULT VALUES;",
		"INSERT INTO public.unit_mv VALUES (DEFAULT);",
		"INSERT INTO public.unit_mv VALUES (nextval('missing'));",
		"INSERT INTO public.unit_mv VALUES ((SELECT 1));",
	} {
		t.Run(rows, func(t *testing.T) {
			e := New().restoreStage()
			op, done, stage, d := materializedUnitStage(t, e)
			defer done()
			e.catalog.state = e.stageRead(stage).State()
			e.mu.Lock()
			e.storage.InstallRelationLocked(d.Address(), &storage.Relation{})
			e.mu.Unlock()
			before, store := e.catalog.state, e.storage.Relations[d.Address()]
			if err := e.importMaterializedSnapshots(context.Background(), op, []materializedSnapshot{{"public", "unit_mv", true, rows}}); err == nil {
				t.Fatal("accepted noncanonical/effectful snapshot")
			}
			if e.catalog.state != before || e.storage.Relations[d.Address()] != store || d.Populated {
				t.Fatal("failed private import published a prefix")
			}
		})
	}
	e := New().restoreStage()
	op, done, stage, d := materializedUnitStage(t, e)
	defer done()
	e.catalog.state = e.stageRead(stage).State()
	e.mu.Lock()
	e.storage.InstallRelationLocked(d.Address(), &storage.Relation{})
	e.mu.Unlock()
	record := materializedSnapshot{"public", "unit_mv", true, "INSERT INTO public.unit_mv VALUES (7),(7),(NULL);"}
	if err := e.importMaterializedSnapshots(context.Background(), op, []materializedSnapshot{record, record}); err == nil {
		t.Fatal("duplicate target accepted")
	}
	if err := e.importMaterializedSnapshots(context.Background(), op, []materializedSnapshot{record}); err != nil {
		t.Fatal(err)
	}
	current := e.catalog.state.MustRelation(d.Address())
	stored := e.storage.Relations[d.Address()].Versions
	if current == d || !current.Populated || d.Populated || len(stored) != 3 || !sqltype.SameDatum(stored[0].Values[0], stored[1].Values[0]) || !stored[2].Values[0].IsNull() {
		t.Fatal("import lost immutable descriptor, duplicate or NULL")
	}
}

func TestMaterializedViewPublication(t *testing.T) {
	e := New()
	_, done, stage, d := materializedUnitStage(t, e)
	defer done()
	d.Populated = true
	before := e.catalog.state
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value, err := sqltype.Integer(d.Columns[0].Usage.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	rows := [][]sqltype.Value{{value}}
	if err := e.stageQueryTableRows(ctx, stage, d, rows, ids.TxRef{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if stage.initialTables != nil || e.catalog.state != before {
		t.Fatal("canceled stage published rows/catalog")
	}
	e.catalog.state = catalog.CloneState(e.catalog.state)
	e.catalog.state.SetEpoch(e.catalog.state.Epoch() + 1)
	if err := e.stageQueryTableRows(context.Background(), stage, d, rows, ids.TxRef{}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	if stage.initialTables != nil {
		t.Fatal("stale stage retained rows")
	}
}
