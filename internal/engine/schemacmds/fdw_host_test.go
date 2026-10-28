package schemacmds_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// TestFDWStageTable pins fdw.StageTable over the statement's ddlHost:
// CREATE FOREIGN TABLE stages the relation and returns a table validator and
// raw configured-column schedules. Preparing those schedules invokes genuine
// column callbacks; DROP stages removal. Nothing is published.
func TestFDWStageTable(t *testing.T) {
	e := engine.New()
	var seen []catalog.ForeignObjectKind
	mutateOptions := false
	validator, err := fdw.ValidateValidator(fdw.ValidatorDefinition{
		Name: catalog.ForeignFunctionName{Name: "host_validator"},
		Validate: func(_ context.Context, kind catalog.ForeignObjectKind, options []catalog.ForeignOption) error {
			seen = append(seen, kind)
			if mutateOptions && len(options) != 0 {
				options[0].Value = "callback-mutated"
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.InstallForeignDefinitions(nil, []fdw.ValidatorDefinition{validator}, nil, nil); err != nil {
		t.Fatal(err)
	}
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE FOREIGN DATA WRAPPER host_fdw VALIDATOR host_validator")
	enginetest.SessionSQL(t, s, "CREATE SERVER host_server FOREIGN DATA WRAPPER host_fdw")
	enginetest.SessionSQL(t, s, "CREATE FOREIGN TABLE host_old(id integer) SERVER host_server")
	op, auth := conversionUnitAuth(t, e)
	before := e.Catalog.State
	stageTable := func(sql string) (*core.CatalogStage, []fdw.Validation, []ids.ObjectAddress, error) {
		stage := e.BeginCatalogStage(auth.Catalog)
		t.Cleanup(func() { e.DiscardCatalogStage(stage) })
		e.Mu.Lock()
		defer e.Mu.Unlock()
		stage.AuthorizeLocked(auth, op.Session)
		jobs, roots, err := fdw.StageTable(tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, enginetest.Statement(t, sql).(*parser.ForeignDDL), sqltype.InputEnvironment{})
		return stage, jobs, roots, err
	}

	stage, jobs, roots, err := stageTable("CREATE FOREIGN TABLE host_new(a integer OPTIONS(tag 'a'), b text OPTIONS(tag 'b')) SERVER host_server OPTIONS (tag 'x')")
	if err != nil || len(roots) != 0 || len(jobs) != 3 {
		t.Fatal("create staged", len(jobs), roots, err)
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"}), Name: "host_new"}
	d, ok := stage.Get(stage.LookupName(key)).(*catalog.Relation)
	server := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.ForeignServerClassID, Name: "host_server"})
	if !ok || d.RelationKind != catalog.ForeignRelation || d.Foreign == nil || d.Foreign.Server != server || len(d.Foreign.Columns) != 0 {
		t.Fatal("staged foreign relation", d)
	}
	seen = nil
	for _, scheduled := range jobs {
		job := scheduled
		if job.Attribute != 0 {
			job, _, err = fdw.PrepareColumnValidation(auth.Catalog, scheduled)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err = fdw.RunValidations(context.Background(), nil, []fdw.Validation{job}); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(seen, []catalog.ForeignObjectKind{catalog.ForeignTable, catalog.ForeignAttribute, catalog.ForeignAttribute}) {
		t.Fatal("validator jobs", seen)
	}
	column, installation, err := fdw.PrepareColumnValidation(auth.Catalog, jobs[1])
	if err != nil {
		t.Fatal(err)
	}
	mutateOptions = true
	if err = fdw.RunValidations(context.Background(), nil, []fdw.Validation{column}); err != nil {
		t.Fatal(err)
	}
	mutateOptions = false
	if !slices.Equal(installation, []catalog.ForeignOption{{Name: "tag", Value: "a"}}) {
		t.Fatal("application validator mutated the installation payload", installation)
	}
	stage, jobs, roots, err = stageTable("CREATE FOREIGN TABLE host_empty(a integer, b text) SERVER host_server")
	if err != nil || len(roots) != 0 || len(jobs) != 1 {
		t.Fatal("optionless columns staged validator jobs", len(jobs), roots, err)
	}
	seen = nil
	if err = fdw.RunValidations(context.Background(), nil, jobs); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []catalog.ForeignObjectKind{catalog.ForeignTable}) {
		t.Fatal("optionless-column validation", seen)
	}

	old := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: key.Namespace, Name: "host_old"})
	stage, jobs, roots, err = stageTable("DROP FOREIGN TABLE host_old")
	if err != nil || len(jobs) != 0 || !slices.Equal(roots, []ids.ObjectAddress{old}) || stage.Get(old) != nil {
		t.Fatal("drop staged", len(jobs), roots, err)
	}
	if e.Catalog.State != before {
		t.Fatal("staging published")
	}
}
