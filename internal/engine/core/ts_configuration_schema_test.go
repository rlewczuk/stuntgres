package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func configCreate(name string) *parser.CreateConfiguration {
	return &parser.CreateConfiguration{Name: enginetest.ConfigName(name), Options: []parser.ConfigurationOption{{Name: parser.Identifier{Text: "copy"}, Value: enginetest.ConfigName("simple"), Supplied: true}}}
}

func configStage(t *testing.T, e *core.Engine, op *session.Operation, stmt parser.Statement) (*core.CatalogStage, error) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	host := schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}
	switch s := stmt.(type) {
	case *parser.CreateConfiguration:
		err = ddl.StageCreateConfiguration(host, auth, s)
	case *parser.AlterConfiguration:
		err = ddl.StageAlterConfiguration(host, auth, s)
	}
	return stage, err
}

func TestTSConfigurationCopyOwnership(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	base := e.CatalogRead()
	seed := base.MustTSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748})
	stage, err := configStage(t, e, op, configCreate("owned_copy"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	var copy *catalog.TSConfiguration
	for _, d := range stage.Descriptors() {
		if d, ok := d.(*catalog.TSConfiguration); ok {
			copy = d
		}
	}
	if copy == nil || copy.Owner != op.RoleState().CurrentUser || !reflect.DeepEqual(copy.Mappings, seed.Mappings) || copy.Parser != seed.Parser || &copy.Mappings[0] == &seed.Mappings[0] || &copy.Mappings[0].Dictionaries[0] == &seed.Mappings[0].Dictionaries[0] {
		t.Fatal("COPY lost immutable ownership", copy)
	}
	copy.Mappings[0].Dictionaries[0].ObjectID++
	if seed.Mappings[0].Dictionaries[0].ObjectID != 3765 || base.Get(seed.Address()) != seed {
		t.Fatal("candidate aliases published mapping backing")
	}
}

func TestTSConfigurationMappingActions(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage, err := configStage(t, e, op, configCreate("mapping_actions"))
	if err != nil {
		t.Fatal(err)
	}
	enginetest.PublishOperationStage(t, e, op, stage)
	before := e.CatalogRead()
	address := before.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "mapping_actions"})
	original := before.MustTSConfiguration(address)
	alter := &parser.AlterConfiguration{Name: enginetest.ConfigName("mapping_actions"), Action: "mapping", MappingAction: parser.ConfigurationAlterMapping, Tokens: []parser.Identifier{{Text: "asciiword"}, {Text: "asciiword"}}, Dictionaries: []parser.ObjectName{enginetest.ConfigName("simple"), enginetest.ConfigName("simple")}}
	stage, err = configStage(t, e, op, alter)
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	next := e.StageRead(stage).MustTSConfiguration(address)
	want := []ids.ObjectAddress{{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}, {ClassID: ids.TSDictionaryClassID, ObjectID: 3765}}
	if !reflect.DeepEqual(next.Mapping(1), want) || len(original.Mapping(1)) != 1 || next.Revision() <= original.Revision() {
		t.Fatal("ordered mapping replacement aliases state", next, original)
	}
	alter.Tokens = append(alter.Tokens, parser.Identifier{Text: "not_a_token"})
	bad, err := configStage(t, e, op, alter)
	defer e.DiscardCatalogStage(bad)
	enginetest.State(t, err, "22023")
	if e.CatalogRead().State() != before.State() {
		t.Fatal("failed mapping stage published")
	}
}

func TestTSConfigurationPublicationFences(t *testing.T) {
	e := engine.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	before := e.CatalogRead()
	stage, err := configStage(t, e, op, configCreate("canceled_candidate"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	if !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State() {
		t.Fatal("canceled configuration candidate crossed final publication fence", err)
	}
	t.Run("CapturedSourceRevision", func(t *testing.T) {
		e := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		stage, err := configStage(t, e, op, configCreate("revision_candidate"))
		if err != nil {
			t.Fatal(err)
		}
		defer e.DiscardCatalogStage(stage)
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		base := e.CatalogRead()
		seed := *base.MustTSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748})
		e.Mu.Lock()
		winner := e.BeginCatalogStage(base)
		seed.ObjectRevision, err = e.Catalog.RevisionLocked()
		if err == nil {
			err = winner.Replace(&seed)
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		defer e.DiscardCatalogStage(winner)
		enginetest.PublishOperationStage(t, e, op, winner)
		incumbent := e.CatalogRead()
		if _, err = e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
			t.Fatal("stale COPY source published")
		}
		if e.CatalogRead().State() != incumbent.State() {
			t.Fatal("rejected stale candidate modified winner")
		}
	})
}
