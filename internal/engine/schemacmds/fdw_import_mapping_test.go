package schemacmds_test

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Only metadata planning is under test; no embedded callback may be invoked.
// An absent optional SchemaImporter must not prevent request construction.
type importMappingCapability struct {
	catalog.ForeignDataWrapper
}

func TestFDWImportMappingPolicy(t *testing.T) {
	for _, policy := range []string{"absent", "present_empty", "public", "exact", "exact_over_public"} {
		t.Run(policy, func(t *testing.T) {
			e := engine.New()
			provider := &importMappingCapability{}
			definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "import_mapping_handler"}, Wrapper: provider})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			session := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, session, "CREATE FOREIGN DATA WRAPPER import_mapping HANDLER import_mapping_handler OPTIONS(tag 'wrapper')")
			enginetest.SessionSQL(t, session, "CREATE SERVER import_mapping FOREIGN DATA WRAPPER import_mapping OPTIONS(tag 'server')")
			var want []catalog.ForeignOption
			switch policy {
			case "present_empty":
				enginetest.SessionSQL(t, session, "CREATE USER MAPPING FOR CURRENT_USER SERVER import_mapping")
			case "public", "exact_over_public":
				enginetest.SessionSQL(t, session, "CREATE USER MAPPING FOR PUBLIC SERVER import_mapping OPTIONS(tag 'public')")
				want = []catalog.ForeignOption{{Name: "tag", Value: "public"}}
			}
			if policy == "exact" || policy == "exact_over_public" {
				enginetest.SessionSQL(t, session, "CREATE USER MAPPING FOR CURRENT_USER SERVER import_mapping OPTIONS(tag 'exact')")
				want = []catalog.ForeignOption{{Name: "tag", Value: "exact"}}
			}
			op, auth := conversionUnitAuth(t, e)
			stage := e.BeginCatalogStage(auth.Catalog)
			defer e.DiscardCatalogStage(stage)
			raw := enginetest.Statement(t, "IMPORT FOREIGN SCHEMA remote LIMIT TO(a,b) FROM SERVER import_mapping INTO public OPTIONS(import_not_null 'false',import_not_null 'true')").(*parser.ForeignDDL)
			e.Mu.Lock()
			stage.AuthorizeLocked(auth, op.Session)
			selection, request, wrapperName, err := fdw.PlanImport(tablecmds.TableHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, raw, auth.RestrictedRelations())
			e.Mu.Unlock()
			if err != nil || selection.Procedure == nil || selection.Procedure.Foreign.Wrapper != provider || wrapperName != "import_mapping" {
				t.Fatal("import admission", selection, wrapperName, err)
			}
			if request.MappingPresent != (policy != "absent") || !slices.Equal(request.MappingOptions, want) {
				t.Fatalf("mapping request=%+v want presence=%v options=%+v", request, policy != "absent", want)
			}
			if request.RemoteSchema != "remote" || request.LocalSchema != "public" || request.Server != "import_mapping" || request.Role != "postgres" || request.Except || !slices.Equal(request.Tables, []string{"a", "b"}) || !slices.Equal(request.Options, []catalog.ForeignOption{{Name: "import_not_null", Value: "false"}, {Name: "import_not_null", Value: "true"}}) {
				t.Fatalf("request lost declaration metadata or ordered duplicate options: %+v", request)
			}
			server := auth.Catalog.MustForeign(auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.ForeignServerClassID, Name: "import_mapping"}))
			wrapper := auth.Catalog.MustForeign(server.Wrapper)
			mapping, present := fdw.FindMapping(auth.View, server)
			strict, strictErr := fdw.MappingFor(auth.View, server)
			if present != request.MappingPresent || present && (strictErr != nil || strict != mapping) {
				t.Fatal("strict/optional selection disagrees", mapping, strict, strictErr)
			}
			if !present {
				var diagnostic *sqlstate.Error
				if strict != nil || !errors.As(strictErr, &diagnostic) || diagnostic.Code != "42704" || diagnostic.Message != `user mapping not found for user "postgres", server "import_mapping"` {
					t.Fatal("strict missing-mapping diagnostic", strict, strictErr)
				}
				if request.MappingOptions != nil {
					t.Fatal("absent mapping owns unexpected options")
				}
			}
			wantBases := []catalog.Descriptor{server, wrapper, auth.Catalog.Get(wrapper.Handler), auth.Catalog.Get(auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"}))}
			if present {
				wantBases = append(wantBases, mapping)
			}
			if len(stage.DomainBases) != len(wantBases) {
				t.Fatalf("observed bases=%v want=%v", stage.DomainBases, wantBases)
			}
			for _, base := range wantBases {
				if base == nil || !slices.Contains(stage.DomainBases, base) {
					t.Fatal("missing concrete discovery dependency", base)
				}
			}
			if present {
				role := authorization.PrincipalRef{}
				if policy != "public" {
					role = auth.State.CurrentUser
				}
				if mapping.Name() != catalog.ForeignMappingName(server.Address(), role) {
					t.Fatal("wrong selected mapping identity", mapping)
				}
				if len(request.MappingOptions) != 0 {
					request.MappingOptions[0].Value = "provider-mutated"
					if !slices.Equal(mapping.Options, want) {
						t.Fatal("request shares selected mapping option backing")
					}
				}
			}
			request.WrapperOptions[0].Value = "provider-mutated"
			request.ServerOptions[0].Value = "provider-mutated"
			request.Options[0].Value = "provider-mutated"
			request.Tables[0] = "provider-mutated"
			if wrapper.Options[0].Value != "wrapper" || server.Options[0].Value != "server" || raw.Options[0].Value != "false" || raw.ImportFilter[0].Text != "a" {
				t.Fatal("request aliases immutable catalog or statement inputs")
			}
			if !stage.DomainBasesCurrent(auth.Catalog.State(), stage.DomainBases, false) {
				t.Fatal("unchanged discovery bases rejected")
			}
			if present {
				changed := catalog.CloneState(auth.Catalog.State())
				next := *mapping
				next.Options = []catalog.ForeignOption{{Name: "tag", Value: "replacement"}}
				changed.SetDescriptor(next.Address(), &next)
				if stage.DomainBasesCurrent(changed, stage.DomainBases, false) {
					t.Fatal("selected mapping replacement escaped discovery fence")
				}
			}
		})
	}
}
