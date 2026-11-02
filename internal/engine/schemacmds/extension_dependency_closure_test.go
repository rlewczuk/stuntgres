package schemacmds_test

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// These providers exist only in candidate-owner units. Their real descriptors
// and sealed cohorts exercise StageCreate; no synthetic package enters SQL or
// the production registry, and no copied graph installer runs in this fixture.
type dependencyUnitProvider struct {
	control catalog.ExtensionControl
	after   func(extension.Stage, *catalog.Extension) error
}

func (p *dependencyUnitProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*dependencyUnitProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1", "2", "3", "9"}}
}
func (p *dependencyUnitProvider) ValidateExtension(read catalog.Read, ext *catalog.Extension) error {
	if !ext.Installation.Valid() || len(ext.Members) != 1 || ext.Installation.Implementation != p {
		return fmt.Errorf("invalid graph cohort")
	}
	member, ok := read.Procedure(ext.Members[0])
	if !ok || member.Extension != ext.Address() || member.Namespace() != ext.TargetNamespace || member.Owner != ext.Owner || !catalog.SameInstallation(member, ext.Installation) {
		return fmt.Errorf("invalid graph member")
	}
	return catalog.ValidateExtensionMembership(read, ext)
}
func (p *dependencyUnitProvider) Stage(stage extension.Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	read := stage.CurrentRead()
	for _, required := range p.control.Requires {
		dependency, ok := read.Extension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: required}))
		if !ok || !dependency.Installation.Valid() {
			return fmt.Errorf("prerequisite not complete before dependent")
		}
	}
	oid, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	member := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: ext.Name() + "_member", RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 23, Implementation: p, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "graph_unit"}}
	ext.Members = []ids.ObjectAddress{member.Address()}
	if err := stage.Create(member); err != nil {
		return err
	}
	if p.after != nil {
		return p.after(stage, ext)
	}
	return nil
}

func dependencyUnitRegister(e *core.Engine, edges map[string][]string) map[string]*dependencyUnitProvider {
	providers := make(map[string]*dependencyUnitProvider, len(edges))
	for name, requires := range edges {
		p := &dependencyUnitProvider{control: catalog.ExtensionControl{Version: "2", Trusted: true, Relocatable: true, Requires: requires}}
		e.Catalog.Owner.Extensions[name] = p
		providers[name] = p
	}
	return providers
}

func dependencyUnitNamespaces(t *testing.T, e *core.Engine, names ...string) map[string]ids.ObjectAddress {
	t.Helper()
	e.Mu.Lock()
	stage := e.BeginCatalogStage(e.Catalog.Current())
	defer func() { e.Mu.Unlock(); e.DiscardCatalogStage(stage) }()
	out := make(map[string]ids.ObjectAddress, len(names))
	for _, name := range names {
		oid, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			t.Fatal(err)
		}
		revision, err := e.Catalog.RevisionLocked()
		if err != nil {
			t.Fatal(err)
		}
		ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}, ObjectName: name, ObjectRevision: revision, Owner: e.RoleStore.Domain.Principal(10)}
		if err := stage.Create(ns); err != nil {
			t.Fatal(err)
		}
		out[name] = ns.Address()
	}
	e.Catalog.PublishLocked(stage.Candidate.State())
	return out
}

func dependencyUnitStage(t *testing.T, e *core.Engine, op *session.Operation, creation *parser.CreateExtension) (*schemacmds.ExtensionHost, error) {
	t.Helper()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	host := &schemacmds.ExtensionHost{Engine: e, Op: op, Auth: auth}
	_, err = extension.StageCreate(host, auth, auth.View, creation)
	e.Mu.Unlock()
	t.Cleanup(func() { e.DiscardCatalogStage(host.Stage) })
	return host, err
}

func dependencyUnitCreation(name, schema string) *parser.CreateExtension {
	creation := &parser.CreateExtension{Name: parser.Identifier{Text: name}, Options: []parser.ExtensionOption{{Name: "cascade"}, {Name: "version", Value: parser.Identifier{Text: "9"}}}}
	if schema != "" {
		creation.Options = append(creation.Options, parser.ExtensionOption{Name: "schema", Value: parser.Identifier{Text: schema}})
	}
	return creation
}

func TestExtensionDependencyClosure(t *testing.T) {
	for _, diamond := range []bool{false, true} {
		name := "chain"
		if diamond {
			name = "diamond"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			edges := map[string][]string{"a": {"b"}, "b": {"d"}, "d": nil}
			if diamond {
				edges["a"] = []string{"b", "c"}
				edges["c"] = []string{"d"}
			}
			providers := dependencyUnitRegister(e, edges)
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			before := e.Catalog.State
			host, err := dependencyUnitStage(t, e, op, dependencyUnitCreation("a", ""))
			if err != nil {
				t.Fatal(err)
			}
			read := host.CurrentRead()
			addresses := make(map[ids.ObjectAddress]bool)
			for name, provider := range providers {
				ext := read.MustExtension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name}))
				if err := provider.ValidateExtension(read, ext); err != nil {
					t.Fatal(name, err)
				}
				want := "2"
				if name == "a" {
					want = "9"
				}
				if ext.Version != want || ext.Address().ObjectID == 0 || addresses[ext.Address()] {
					t.Fatal("default/version/unique header", ext)
				}
				addresses[ext.Address()] = true
				required, err := catalog.ExtensionPrerequisites(read, ext)
				if err != nil || len(required) != len(edges[name]) {
					t.Fatal("genuine edges", name, required, err)
				}
				for index, address := range required {
					if read.MustExtension(address).Name() != edges[name][index] {
						t.Fatal("required order/identity", name, required)
					}
				}
			}
			counts := make(map[string]int)
			for _, d := range host.Stage.Descriptors() {
				if ext, ok := d.(*catalog.Extension); ok {
					counts[ext.Name()]++
				}
			}
			for name := range edges {
				if counts[name] != 1 {
					t.Fatal("duplicate/missing coherent header", counts)
				}
			}
			if e.Catalog.State != before || host.Read().State() != before || before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "d"}) != (ids.ObjectAddress{}) {
				t.Fatal("private closure published a prefix")
			}
		})
	}
	t.Run("reuse_and_original_schema_through_fixed_intermediate", func(t *testing.T) {
		e := engine.New()
		schemas := dependencyUnitNamespaces(t, e, "graph_addons", "graph_old")
		providers := dependencyUnitRegister(e, map[string][]string{"a": {"b", "d"}, "b": {"c", "d"}, "c": nil, "d": nil})
		fixed := "pg_catalog"
		providers["b"].control.Schema = &fixed
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		oldHost, err := dependencyUnitStage(t, e, op, dependencyUnitCreation("d", "graph_old"))
		if err != nil {
			t.Fatal(err)
		}
		e.Catalog.PublishLocked(oldHost.Stage.Candidate.State())
		read := e.CatalogRead()
		incumbent := read.MustExtension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "d"}))
		member := read.Get(incumbent.Members[0])
		providers["d"].control.Version = "3"
		host, err := dependencyUnitStage(t, e, op, dependencyUnitCreation("a", "graph_addons"))
		if err != nil {
			t.Fatal(err)
		}
		candidate := host.CurrentRead()
		if host.Stage.Get(incumbent.Address()) != nil || host.Read().Get(incumbent.Address()) != incumbent {
			t.Fatal("incumbent reuse changed staged-only Get or immutable base semantics")
		}
		if candidate.Get(incumbent.Address()) != incumbent || candidate.Get(member.Address()) != member || incumbent.TargetNamespace != schemas["graph_old"] || incumbent.Version != "9" {
			t.Fatal("reused prerequisite replaced/upgraded/relocated")
		}
		for _, name := range []string{"a", "c"} {
			if ext := candidate.MustExtension(candidate.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name})); ext.TargetNamespace != schemas["graph_addons"] {
				t.Fatal("original explicit schema lost", name, ext.TargetNamespace)
			}
		}
		if ext := candidate.MustExtension(candidate.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "b"})); ext.TargetNamespace.ObjectID != ids.PGCatalogNamespaceOID {
			t.Fatal("fixed schema not honored")
		}
	})
	t.Run("cycle_has_no_publishable_prefix", func(t *testing.T) {
		e := engine.New()
		dependencyUnitRegister(e, map[string][]string{"a": {"b"}, "b": {"a"}})
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		before := e.Catalog.State
		host, err := dependencyUnitStage(t, e, op, dependencyUnitCreation("a", ""))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P19" || diagnostic.Message != `cyclic dependency detected between extensions "a" and "b"` {
			t.Fatal("cycle diagnostic", err)
		}
		if host.Stage == nil || host.Stage.DescriptorCount() != 0 || e.Catalog.State != before {
			t.Fatal("cycle left a completed or published prefix")
		}
	})
}
