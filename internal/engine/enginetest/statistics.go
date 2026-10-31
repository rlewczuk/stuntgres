package enginetest

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func AnalyzeFacts(e *core.Engine, address ids.ObjectAddress) *storage.RelationStatistics {
	if metadata := e.Storage.RelationMetadata[address]; metadata != nil {
		return metadata.Statistics
	}
	return nil
}

func StatisticsUnitCreate() *parser.CreateStatistics {
	return &parser.CreateStatistics{Name: ConfigName("statistics_definition"), Relation: ConfigName("statistics_input"), Elements: []parser.Expr{parser.ColumnExpr{Name: ConfigName("c")}, parser.ColumnExpr{Name: ConfigName("a")}}, Kinds: []parser.Identifier{{Text: "mcv"}, {Text: "dependencies"}}}
}

func StatisticsUnitStage(t testing.TB, e *core.Engine, op *session.Operation, s *parser.CreateStatistics) (*core.CatalogStage, error) {
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

func StatisticsUnitDescriptor(t testing.TB, stage *core.CatalogStage) *catalog.Statistics {
	t.Helper()
	for _, d := range stage.Descriptors() {
		if d, ok := d.(*catalog.Statistics); ok {
			return d
		}
	}
	t.Fatal("missing staged statistics")
	return nil
}

func VacuumStore(t testing.TB, e *core.Engine, name string) (ids.ObjectRef, *storage.Relation) {
	t.Helper()
	ref := CatalogRef(t, e, e.CatalogRead(), name)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	store := e.Storage.Relations[ref.Address]
	if store == nil {
		t.Fatal("missing store", name)
	}
	return ref, store
}
