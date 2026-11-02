package schemacmds_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestExtensionDependencyScriptPath(t *testing.T) {
	t.Run("target_required_catalog_temp_and_principal", func(t *testing.T) {
		e := engine.New()
		schemas := dependencyUnitNamespaces(t, e, "path_target", "path_first", "path_second", "path_hostile")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "CREATE ROLE path_reader")
		enginetest.RoleExecute(t, e, op, "CREATE TEMP TABLE path_seed(v integer)")
		enginetest.RoleExecute(t, e, op, "SET search_path=path_hostile,pg_temp,public")
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		temp := auth.TemporaryNamespace()
		if temp == (ids.ObjectAddress{}) {
			t.Fatal("missing owned TEMP namespace")
		}
		providers := dependencyUnitRegister(e, map[string][]string{"path_root": {"path_r1", "path_r2", "path_catalog", "path_temp"}, "path_r1": nil, "path_r2": nil, "path_catalog": nil, "path_temp": nil})
		for name, schema := range map[string]string{"path_r1": "path_first", "path_r2": "path_second", "path_catalog": "pg_catalog", "path_temp": auth.Catalog.MustNamespace(temp).Name()} {
			value := schema
			providers[name].control.Schema = &value
		}
		host, err := dependencyUnitStage(t, e, op, dependencyUnitCreation("path_root", "path_target"))
		if err != nil {
			t.Fatal(err)
		}
		ext := host.CurrentRead().MustExtension(host.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "path_root"}))
		e.Mu.Lock()
		beforePath, _, err := core.ExplicitPath(host.Auth)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		add := func(namespace ids.ObjectAddress, name string) *catalog.Procedure {
			t.Helper()
			oid, err := host.AllocateOID()
			if err != nil {
				t.Fatal(err)
			}
			revision, err := host.Revision()
			if err != nil {
				t.Fatal(err)
			}
			p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: namespace, ObjectRevision: revision, ObjectName: name, Signature: "23", RoutineKind: catalog.FunctionRoutine, Owner: op.RoleState().CurrentUser, Result: 23}
			if err := host.Create(p); err != nil {
				t.Fatal(err)
			}
			return p
		}
		e.Mu.Lock()
		target := add(schemas["path_target"], "pick_target")
		add(schemas["path_first"], "pick_target")
		first := add(schemas["path_first"], "pick_required")
		add(schemas["path_second"], "pick_required")
		add(temp, "pick_required")
		pgCatalog := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		catalogFirst := add(pgCatalog, "pick_catalog")
		add(schemas["path_target"], "pick_catalog")
		add(temp, "temp_only")
		add(schemas["path_hostile"], "caller_only")
		helpers := []extension.ScriptFunction{{Name: "pick_target", Signature: "23"}, {Name: "pick_required", Signature: "23"}, {Name: "pick_catalog", Signature: "23"}, {Name: "temp_only", Signature: "23"}, {Name: "caller_only", Signature: "23"}}
		found, err := host.ScriptFunctions(op.RoleState().CurrentUser, ext, helpers)
		e.Mu.Unlock()
		if err != nil || !reflect.DeepEqual(found, []*catalog.Procedure{target, first, catalogFirst, nil, nil}) {
			t.Fatal("script path precedence/caller/TEMP exclusion", found, err)
		}
		e.Mu.Lock()
		afterPath, _, err := core.ExplicitPath(host.Auth)
		e.Mu.Unlock()
		if err != nil || !reflect.DeepEqual(beforePath, afterPath) {
			t.Fatal("script helper lookup changed caller path", afterPath, err)
		}
		e.Mu.Lock()
		_, err = host.ScriptFunctions(host.Auth.Roles.LookupRef("path_reader"), ext, helpers)
		e.Mu.Unlock()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for schema path_target" {
			t.Fatal("actual script principal bypassed schema USAGE", err)
		}
		e.Mu.Lock()
		reader := host.Auth.Roles.LookupRef("path_reader")
		targetNamespace := *host.CurrentRead().MustNamespace(schemas["path_target"])
		targetNamespace.Owner = reader
		err = host.Replace(&targetNamespace)
		if err == nil {
			_, err = host.ScriptFunctions(reader, ext, helpers)
		}
		e.Mu.Unlock()
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for schema path_first" {
			t.Fatal("required schema USAGE was not checked as the script principal", err)
		}
		// An explicit pg_catalog target keeps its explicit position and does not
		// obtain a second lower-priority entry from the same prerequisite.
		moved := *ext
		moved.TargetNamespace = pgCatalog
		e.Mu.Lock()
		err = host.Replace(&moved)
		if err == nil {
			found, err = host.ScriptFunctions(op.RoleState().CurrentUser, &moved, helpers[2:3])
		}
		e.Mu.Unlock()
		if err != nil || len(found) != 1 || found[0] != catalogFirst {
			t.Fatal("explicit catalog target", found, err)
		}
	})
	t.Run("genuine_cube_visible_only_in_candidate", func(t *testing.T) {
		e := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		host, err := dependencyUnitStage(t, e, op, &parser.CreateExtension{Name: parser.Identifier{Text: "earthdistance"}, Options: []parser.ExtensionOption{{Name: "cascade"}}})
		if err != nil {
			t.Fatal(err)
		}
		base, current := host.Read(), host.CurrentRead()
		if base.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}) != (ids.ObjectAddress{}) {
			t.Fatal("base read changed during closure")
		}
		cube := current.MustExtension(current.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
		earth := current.MustExtension(current.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "earthdistance"}))
		if err := extension.StockCube.ValidateExtension(current, cube); err != nil {
			t.Fatal(err)
		}
		if err := extension.StockEarthdistance.ValidateExtension(current, earth); err != nil {
			t.Fatal(err)
		}
		domain := current.MustType(earth.Members[0])
		typ := current.MustType(cube.Members[0])
		if domain.SQLType.DomainBase().Type != typ.SQLType || len(domain.DomainChecks) != 3 || domain.Extension != earth.Address() {
			t.Fatal("candidate used a loose cube or lost real checks")
		}
	})
}
