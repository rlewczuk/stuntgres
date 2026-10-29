package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func hostSnapshotRow(t *testing.T, e *core.Engine, query string) []sqltype.Value {
	t.Helper()
	result := enginetest.Run(t, e, query)
	if result.Rows == nil {
		t.Fatal("metadata query returned no row owner")
	}
	row := make([]sqltype.Value, len(result.Rows.Schema()))
	present, err := result.Rows.Next(context.Background(), row)
	if err != nil || !present {
		t.Fatal(query, present, err)
	}
	extra := make([]sqltype.Value, len(row))
	if present, err = result.Rows.Next(context.Background(), extra); err != nil || present {
		t.Fatal("extra metadata row", query, present, err)
	}
	return row
}

// Real pg_proc snapshots must coexist with genuine SQL/PL and native C/internal
// declarations. A Go implementation display is not a claim that a host scalar
// lacks its actual language identity, nor that a handler utility owns one.
func TestHostProcedureDeclarationSnapshot(t *testing.T) {
	callbacks := 0
	e, original := hostOwnerEngine(t, func() error { callbacks++; return nil })
	for _, query := range []string{
		"CREATE FUNCTION owner_host_default(v hstore DEFAULT 'a=>7,b=>NULL'::hstore) RETURNS hstore LANGUAGE owner_host AS 'increment'",
		"CREATE LANGUAGE owner_host_alias HANDLER owner_host_handler",
		"CREATE FUNCTION owner_host_alias_fn(v hstore) RETURNS hstore LANGUAGE owner_host_alias AS 'increment'",
		"CREATE FUNCTION owner_snapshot_sql(v integer DEFAULT 1) RETURNS integer LANGUAGE sql AS 'SELECT v+1'",
		"CREATE FUNCTION owner_snapshot_pl(v integer DEFAULT 1) RETURNS integer LANGUAGE plpgsql AS 'BEGIN RETURN v+1; END;'",
	} {
		enginetest.Run(t, e, query)
	}
	check := func(hostName, sqlName, plName, internalName, cName string) {
		t.Helper()
		before := e.CatalogRead().State()
		for _, spec := range []struct{ name, language string }{{"owner_host_default", hostName}, {"owner_host_alias_fn", "owner_host_alias"}, {"owner_snapshot_sql", sqlName}, {"owner_snapshot_pl", plName}} {
			query := "SELECT p.prolang=l.oid AS v FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.proname='" + spec.name + "' AND l.lanname='" + spec.language + "'"
			matched, err := hostSnapshotRow(t, e, query)[0].Boolean()
			if err != nil || !matched {
				t.Fatal("actual routine language identity", spec, matched, err)
			}
		}
		for _, language := range []string{internalName, cName} {
			matched, err := hostSnapshotRow(t, e, "SELECT count(*)>0 AS v FROM pg_proc p JOIN pg_language l ON p.prolang=l.oid WHERE l.lanname='"+language+"'")[0].Boolean()
			if err != nil || !matched {
				t.Fatal("released native language identity", language, matched, err)
			}
		}
		for _, value := range hostSnapshotRow(t, e, "SELECT pronargdefaults=1 AS count_ok,proargdefaults IS NOT NULL AS defaults_ok,prosqlbody IS NULL AS body_ok,prosrc='increment' AS source_ok FROM pg_proc WHERE proname='owner_host_default'") {
			matched, err := value.Boolean()
			if err != nil || !matched {
				t.Fatal("host declaration snapshot/default ownership", matched, err)
			}
		}
		for _, value := range hostSnapshotRow(t, e, "SELECT parameter_name='v' AS name_ok,parameter_mode='IN' AS mode_ok,udt_name='hstore' AS type_ok,parameter_default IS NOT NULL AS default_ok FROM information_schema.parameters WHERE specific_schema='public' AND specific_name LIKE 'owner_host_default_%'") {
			matched, err := value.Boolean()
			if err != nil || !matched {
				t.Fatal("host parameter default disclosure", matched, err)
			}
		}
		row := hostSnapshotRow(t, e, "SELECT pg_get_functiondef('owner_host_default(hstore)'::regprocedure),pg_get_function_arguments('owner_host_default(hstore)'::regprocedure),pg_get_function_identity_arguments('owner_host_default(hstore)'::regprocedure),pg_get_function_result('owner_host_default(hstore)'::regprocedure)")
		definition, err := row[0].Text()
		if err != nil || !strings.Contains(definition, "LANGUAGE "+hostName+"\n") || !strings.Contains(definition, "increment") || strings.Contains(definition, "LANGUAGE go\n") {
			t.Fatal("host definition language/source", definition, err)
		}
		arguments, err := row[1].Text()
		if err != nil || !strings.Contains(arguments, "DEFAULT") || !strings.Contains(arguments, "hstore") {
			t.Fatal("host argument default inquiry", arguments, err)
		}
		identity, err := row[2].Text()
		if err != nil || identity != "v hstore" {
			t.Fatal("host identity argument inquiry", identity, err)
		}
		result, err := row[3].Text()
		if err != nil || result != "hstore" {
			t.Fatal("host result inquiry", result, err)
		}
		view := e.CatalogRead()
		for _, descriptor := range view.Descriptors() {
			p, ok := descriptor.(*catalog.Procedure)
			if !ok || p.HostHandler == nil && p.HostConversion == nil {
				continue
			}
			address, err := catalog.ProcedureLanguageAddress(view, p)
			if err != nil || address != (ids.ObjectAddress{}) || p.Display.Language != catalog.GoRoutineLanguage || p.SQL != nil || p.Host != nil {
				t.Fatal("native host utility acquired scalar language/body", p.Name(), address, err)
			}
		}
		if callbacks != 0 || e.CatalogRead().State() != before {
			t.Fatal("metadata inquiry executed a callback or mutated catalog", callbacks)
		}
	}
	check("owner_host", "sql", "plpgsql", "internal", "c")
	for _, query := range []string{"ALTER LANGUAGE owner_host RENAME TO owner_host_renamed", "ALTER LANGUAGE sql RENAME TO owner_snapshot_sql_language", "ALTER LANGUAGE plpgsql RENAME TO owner_snapshot_pl_language", "ALTER LANGUAGE internal RENAME TO owner_snapshot_internal_language", "ALTER LANGUAGE c RENAME TO owner_snapshot_c_language"} {
		enginetest.Run(t, e, query)
	}
	check("owner_host_renamed", "owner_snapshot_sql_language", "owner_snapshot_pl_language", "owner_snapshot_internal_language", "owner_snapshot_c_language")
	view := e.CatalogRead()
	current, _ := view.Procedure(original.Address())
	for _, modify := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Language = ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 14} }, func(p *catalog.Procedure) { p.Display.Language = catalog.SQLRoutineLanguage }} {
		copy := *current
		modify(&copy)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(copy.Address(), &copy)
		if _, err := catalog.ProcedureLanguageAddress(e.Catalog.Read(state), &copy); err == nil {
			t.Fatal("forged host language/display became declaration authority")
		}
	}
	for _, descriptor := range view.Descriptors() {
		utility, ok := descriptor.(*catalog.Procedure)
		if !ok || utility.HostHandler == nil && utility.HostConversion == nil {
			continue
		}
		copy := *utility
		copy.Language = current.Language
		state := catalog.CloneState(view.State())
		state.SetDescriptor(copy.Address(), &copy)
		if _, err := catalog.ProcedureLanguageAddress(e.Catalog.Read(state), &copy); err == nil {
			t.Fatal("native utility borrowed scalar language authority")
		}
	}
}
