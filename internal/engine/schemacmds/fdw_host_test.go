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
	if d.TableMethod != 0 || e.Storage.Relations[d.Address()] != nil {
		t.Fatal("foreign declaration acquired local storage authority")
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
	stage, _, _, err = stageTable("CREATE FOREIGN TABLE host_identity(id integer GENERATED ALWAYS AS IDENTITY) SERVER host_server")
	if err != nil {
		t.Fatal(err)
	}
	identityKey := key
	identityKey.Name = "host_identity"
	identityTable := stage.Get(stage.LookupName(identityKey)).(*catalog.Relation)
	identity, exists := identityTable.ColumnMeta.Identities[identityTable.Attributes[0]]
	if !exists {
		t.Fatal("foreign identity was not staged")
	}
	sequence := stage.Get(identity.Sequence).(*catalog.Relation)
	owned := identityTable.Address()
	owned.SubID = identityTable.Attributes[0]
	if sequence.Sequence == nil || sequence.Sequence.Owned != owned || !sequence.Sequence.Identity || identityTable.TableMethod != 0 || e.Storage.Relations[identityTable.Address()] != nil {
		t.Fatal("foreign identity lost its ordinary owned sequence or gained heap storage")
	}
	read := stage.StageRead()
	if err := core.RelationProvider.Validate(read, sequence); err != nil {
		t.Fatal("foreign identity sequence failed ordinary descriptor validation", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*catalog.Relation)
	}{
		{"wrong_kind", func(r *catalog.Relation) { r.RelationKind = catalog.ViewRelation }},
		{"wrong_owner", func(r *catalog.Relation) { r.Owner = e.RoleStore.Domain.Principal(9999) }},
		{"wrong_schema", func(r *catalog.Relation) { r.ObjectNamespace.ObjectID = ids.PGCatalogNamespaceOID }},
		{"missing_attribute", func(r *catalog.Relation) { r.Attributes = nil }},
		{"missing_identity", func(r *catalog.Relation) { r.ColumnMeta.Identities = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.CloneState(read.State())
			changed := *identityTable
			tc.change(&changed)
			state.SetDescriptor(changed.Address(), &changed)
			if err := core.RelationProvider.Validate(read.WithState(state), sequence); err == nil {
				t.Fatal("invalid ownership graph accepted")
			}
			if read.MustRelation(identityTable.Address()) != identityTable {
				t.Fatal("validation retargeted the private predecessor")
			}
		})
	}
	stage, _, _, err = stageTable("CREATE FOREIGN TABLE host_child() INHERITS(host_old) SERVER host_server")
	if err != nil {
		t.Fatal(err)
	}
	childKey := key
	childKey.Name = "host_child"
	child := stage.Get(stage.LookupName(childKey)).(*catalog.Relation)
	if child.Foreign == nil || child.Foreign.Server != server || len(child.Columns) != 1 || child.Inheritance == nil || len(child.Inheritance.Parents) != 1 || child.Inheritance.Columns[child.Attributes[0]].Local || child.TableMethod != 0 || e.Storage.Relations[child.Address()] != nil {
		t.Fatal("foreign inheritance did not retain server/origin authority without local storage")
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

func TestFDWStageTableOwnedSequences(t *testing.T) {
	for _, tc := range []struct {
		name, drop          string
		dependent, restrict bool
	}{
		{name: "table", drop: "DROP FOREIGN TABLE host_identity"},
		{name: "table_restrict", drop: "DROP FOREIGN TABLE host_identity", dependent: true, restrict: true},
		{name: "table_cascade", drop: "DROP FOREIGN TABLE host_identity CASCADE", dependent: true},
		{name: "server_cascade", drop: "DROP SERVER host_server CASCADE", dependent: true},
		{name: "wrapper_cascade", drop: "DROP FOREIGN DATA WRAPPER host_fdw CASCADE", dependent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE FOREIGN DATA WRAPPER host_fdw")
			enginetest.SessionSQL(t, s, "CREATE SERVER host_server FOREIGN DATA WRAPPER host_fdw")
			enginetest.SessionSQL(t, s, "CREATE FOREIGN TABLE host_identity(id integer GENERATED ALWAYS AS IDENTITY) SERVER host_server")
			enginetest.SessionSQL(t, s, "CREATE SEQUENCE host_unrelated")
			if tc.dependent {
				enginetest.SessionSQL(t, s, "CREATE VIEW host_sequence_user AS SELECT nextval('host_identity_id_seq')")
			}
			op, auth := conversionUnitAuth(t, e)
			base := auth.Catalog
			ns := base.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
			named := func(name string) ids.ObjectAddress {
				return base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name})
			}
			owner := base.MustRelation(named("host_identity"))
			sequence := owner.ColumnMeta.Identities[owner.Attributes[0]].Sequence
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			syntax := enginetest.Statement(t, tc.drop).(*parser.ForeignDDL)
			e.Mu.Lock()
			stage.AuthorizeLocked(auth, op.Session)
			host := tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}
			jobs, roots, err := fdw.StageDDL(host, auth, syntax, sqltype.InputEnvironment{})
			e.Mu.Unlock()
			if err != nil || len(jobs) != 0 {
				t.Fatal("drop staging", err, len(jobs))
			}
			for _, root := range roots {
				stage.MarkDrop(root, catalog.DropProvenance{Original: true})
			}
			drop := core.StageDropProvider(t.Context(), stage, nil, nil, nil)
			dependencies, failure := drop.PrepareExpressionDependencies(roots, syntax.Mode, ids.ObjectAddress{})
			err = failure
			if err == nil {
				err = stage.ApplySelectedDropRoots(dependencies)
			}
			if err == nil {
				err = drop.ApplyExpressionDependencies(dependencies)
			}
			if err == nil {
				err = drop.ApplyOwnedExpressionDependencies(dependencies)
			}
			if tc.restrict {
				enginetest.State(t, err, "2BP01")
				if stage.Removed(sequence) || stage.Removed(named("host_sequence_user")) {
					t.Fatal("RESTRICT admitted owned sequence or its consumer")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !stage.Removed(owner.Address()) || !stage.Removed(sequence) || stage.Get(sequence) != nil {
					t.Fatal("foreign retirement left its owned identity sequence")
				}
				if tc.dependent && !stage.Removed(named("host_sequence_user")) {
					t.Fatal("CASCADE left an owned sequence consumer")
				}
			}
			if stage.Removed(named("host_unrelated")) || e.Catalog.State != base.State() || base.MustRelation(sequence).Sequence.Owned.ObjectID != owner.Address().ObjectID {
				t.Fatal("private retirement changed unrelated or committed state")
			}
		})
	}
}
