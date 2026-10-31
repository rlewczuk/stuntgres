package writes_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func statisticsMCVSnapshotCatalog(t *testing.T) (*core.Engine, *catalog.Statistics) {
	t.Helper()
	e, _ := statisticsUnitEngine(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	syntax := statisticsUnitCreate()
	syntax.Kinds = []parser.Identifier{{Text: "mcv"}}
	stage, err := statisticsUnitStage(t, e, op, syntax)
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	definition := statisticsUnitDescriptor(t, stage)
	enginetest.PublishOperationStage(t, e, op, stage)
	return e, definition
}

func TestStatisticsMCVSnapshotRemint(t *testing.T) {
	source, definition := statisticsMCVSnapshotCatalog(t)
	read := source.CatalogRead()
	relation := read.MustRelation(definition.Relation.Address)
	codec := core.StatisticsSnapshotCodec(t.Context(), source, read)
	usage := relation.Columns[relation.IndexForAttribute(1)].Usage
	one, err := usage.Type.Input(t.Context(), "1", usage.Typmod)
	if err != nil {
		t.Fatal(err)
	}
	two, err := usage.Type.Input(t.Context(), "2", usage.Typmod)
	if err != nil {
		t.Fatal(err)
	}
	a, err := codec.Encode(usage, one)
	if err != nil {
		t.Fatal(err)
	}
	b, err := codec.Encode(usage, two)
	if err != nil {
		t.Fatal(err)
	}
	record := storage.ExtendedStatisticsSnapshot{Schema: "public", Name: definition.Name(), RelationSchema: "public", RelationName: relation.Name(), Keys: []int32{1, 4}, Kinds: "m", Usages: []storage.NDistinctUsageRecord{{TypeSchema: "pg_catalog", TypeName: "int4", Typmod: -1}, {TypeSchema: "pg_catalog", TypeName: "int4", Typmod: -1}}, Subsets: [][]int16{}, Counts: []uint64{}, MCV: &storage.ExtendedMCVSnapshot{Rows: 5, Dictionaries: [][]json.RawMessage{{a, b}, {a, b}}, Items: []storage.ExtendedMCVSnapshotItem{
		{Nulls: []bool{false, false}, Indexes: []uint16{0, 0}, Count: 2, MarginalCounts: []uint64{2, 2}},
		{Nulls: []bool{false, false}, Indexes: []uint16{1, 1}, Count: 2, MarginalCounts: []uint64{2, 2}},
		{Nulls: []bool{true, true}, Indexes: []uint16{0, 0}, Count: 1, MarginalCounts: []uint64{1, 1}},
	}}}
	metadata, err := storage.RestoreExtendedStatisticsSnapshots(t.Context(), read, []storage.ExtendedStatisticsSnapshot{record}, codec)
	if err != nil {
		t.Fatal(err)
	}
	var original *storage.ExtendedStatistics
	for _, fact := range metadata[relation.Address()].Statistics.Extended {
		original = fact
	}
	if original == nil || original.MCVData.Rows != 5 || original.MCV.IsNull() {
		t.Fatal("missing built retained state")
	}
	raw, err := original.MCV.Type().AppendBinaryWithEnvironment(t.Context(), nil, original.MCV, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	frequency := float64(2) / 5
	baseline := frequency * frequency
	if len(raw) != 144 || raw[122] != 1 || raw[123] != 1 || math.Float64frombits(binary.LittleEndian.Uint64(raw[80:])) != frequency || math.Float64frombits(binary.LittleEndian.Uint64(raw[88:])) != baseline || math.Float64frombits(binary.LittleEndian.Uint64(raw[124:])) != float64(1)/5 {
		t.Fatal("retained counts, full-data baseline or genuine NULL flags changed")
	}
	// No live rows are consulted: deliberately stale five-row state is genuine.
	captured, err := storage.CaptureExtendedStatisticsSnapshots(t.Context(), read, metadata, codec)
	if err != nil || len(captured) != 1 {
		t.Fatal(captured, err)
	}
	before, err := storage.EncodeExtendedStatisticsSnapshot(captured[0])
	if err != nil {
		t.Fatal(err)
	}
	destination, destinationDefinition := statisticsMCVSnapshotCatalog(t)
	destinationRead := destination.CatalogRead()
	destinationCodec := core.StatisticsSnapshotCodec(t.Context(), destination, destinationRead)
	restored, err := storage.RestoreExtendedStatisticsSnapshots(t.Context(), destinationRead, captured, destinationCodec)
	if err != nil {
		t.Fatal(err)
	}
	var reminted *storage.ExtendedStatistics
	for key, fact := range restored[destinationDefinition.Relation.Address].Statistics.Extended {
		if key.Definition.Domain() != destinationRead.Domain() || key.Definition.Epoch != destinationRead.Epoch() {
			t.Fatal("foreign definition authority")
		}
		reminted = fact
	}
	if reminted == original || reminted.MCVData == original.MCVData || reminted.Usages[0].Type == original.Usages[0].Type || reminted.MCVData.Rows != 5 || reminted.MCVData.Items[0].Count != 2 {
		t.Fatal("remint lost immutable construction authority")
	}
	recaptured, err := storage.CaptureExtendedStatisticsSnapshots(t.Context(), destinationRead, restored, destinationCodec)
	if err != nil || len(recaptured) != 1 {
		t.Fatal(err)
	}
	after, err := storage.EncodeExtendedStatisticsSnapshot(recaptured[0])
	if err != nil || before != after {
		t.Fatal("semantic redump changed", err)
	}
	// Duplicate logical dictionary values and noncanonical typed constructors fail
	// before exposing any replacement metadata or touching a published store root.
	sentinel := destination.Storage.RelationMetadata
	for _, bad := range []json.RawMessage{a, json.RawMessage(`{"scalar":"1","kind":"scalar"}`), json.RawMessage(`{"kind":"null"}`), json.RawMessage(`{"kind":"scalar","scalar":"1","extra":false}`)} {
		copyRecord := record
		copyMCV := *record.MCV
		copyMCV.Dictionaries = [][]json.RawMessage{{a, bad}, {a, b}}
		copyRecord.MCV = &copyMCV
		if result, err := storage.RestoreExtendedStatisticsSnapshots(t.Context(), destinationRead, []storage.ExtendedStatisticsSnapshot{copyRecord}, destinationCodec); err == nil || result != nil {
			t.Fatal("corrupt constructor accepted", string(bad))
		}
		if len(destination.Storage.RelationMetadata) != len(sentinel) || destination.CatalogRead().State() != destinationRead.State() {
			t.Fatal("failed restore published state")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := storage.RestoreExtendedStatisticsSnapshots(ctx, destinationRead, captured, destinationCodec); !errors.Is(err, context.Canceled) || result != nil {
		t.Fatal("canceled import returned prefix", err)
	}
	// Genuine retention with unchanged usages preserves construction backing.
	reowned, err := storage.ReownRetainedExtendedStatistics(t.Context(), reminted, destinationDefinition, destinationRead, destinationCodec, destinationCodec)
	if err != nil || reowned.MCVData != reminted.MCVData {
		t.Fatal("unchanged retained construction copied", err)
	}
}

func statisticsUnitEngine(t *testing.T) (*core.Engine, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	syntax := &parser.CreateTable{Name: configName("statistics_input")}
	for _, name := range []string{"a", "b", "unselected", "c"} {
		syntax.Elements = append(syntax.Elements, parser.TableElement{Column: &parser.Column{Name: name, TypeName: parser.TypeName{Name: "integer"}}})
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	err = tablecmds.SchemaTableLocked(e, op, auth, stage, syntax, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	e.Mu.Unlock()
	if err != nil {
		e.DiscardCatalogStage(stage)
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	enginetest.PublishOperationStage(t, e, op, stage)
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "statistics_input"})
	return e, view.MustRelation(address)
}

func statisticsUnitCreate() *parser.CreateStatistics {
	return &parser.CreateStatistics{Name: configName("statistics_definition"), Relation: configName("statistics_input"), Elements: []parser.Expr{parser.ColumnExpr{Name: configName("c")}, parser.ColumnExpr{Name: configName("a")}}, Kinds: []parser.Identifier{{Text: "mcv"}, {Text: "dependencies"}}}
}

func statisticsUnitStage(t *testing.T, e *core.Engine, op *session.Operation, s *parser.CreateStatistics) (*core.CatalogStage, error) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	err = ddl.StageCreateStatistics(core.DDLHost{E: e, Op: op, CatalogStage: stage}, auth, s, sqltype.InputEnvironment{})
	return stage, err
}

func statisticsUnitDescriptor(t *testing.T, stage *core.CatalogStage) *catalog.Statistics {
	t.Helper()
	for _, d := range stage.Descriptors() {
		if d, ok := d.(*catalog.Statistics); ok {
			return d
		}
	}
	t.Fatal("missing staged statistics")
	return nil
}

func configName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}
}
