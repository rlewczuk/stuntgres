package core_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
)

func ownershipParserFixture(t *testing.T) (*core.Engine, *catalog.Extension, *catalog.TSParser, *catalog.TSConfiguration) {
	t.Helper()
	e, ext := ownershipUnitInstall(t, "unaccent")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage, err := parserStage(t, e, op, parserCreateNode("eo_parser"))
	if err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, op, stage)
	enginetest.RoleExecute(t, e, op, `CREATE TEXT SEARCH CONFIGURATION public.eo_parser_config (PARSER=public.eo_parser)`)
	enginetest.RoleExecute(t, e, op, `ALTER TEXT SEARCH CONFIGURATION public.eo_parser_config ADD MAPPING FOR asciiword WITH pg_catalog.simple`)
	view := e.CatalogRead()
	d := view.MustTSParser(view.NameAddress(ids.NameKey{ClassID: ids.TSParserClassID, Namespace: ext.TargetNamespace, Name: "eo_parser"}))
	configuration := view.MustTSConfiguration(view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ext.TargetNamespace, Name: "eo_parser_config"}))
	return e, ext, d, configuration
}

func ownershipParserMembershipAuthority(t *testing.T) {
	e, ext, original, configuration := ownershipParserFixture(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	provider := catalog.TSParserProvider{}
	closure, err := catalog.ExtensionMemberClosure(before, original)
	if err != nil || len(closure) != 1 || closure[0] != original {
		t.Fatal("callbacks/configuration became members", closure, err)
	}
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent ADD TEXT SEARCH PARSER public.eo_parser`)
	view := e.CatalogRead()
	d := view.MustTSParser(original.Address())
	if d == original || d.Extension != ext.Address() || d.Revision() <= original.Revision() || original.Extension != (ids.ObjectAddress{}) || before.Get(d.Address()) != original || view.Get(configuration.Address()) != configuration {
		t.Fatal("membership mutated old backing or configuration")
	}
	payload := *d
	payload.Extension, payload.ObjectRevision = original.Extension, original.ObjectRevision
	if payload != *original || d.Headline != 0 {
		t.Fatal("membership changed callback payload")
	}
	owner, acl, columns := access.DescriptorACL(d)
	if owner != (authorization.PrincipalRef{}) || acl.Explicit || len(acl.Entries) != 0 || columns != nil || access.ClassPrivileges(d) != 0 || catalog.DescriptorOrigin(d) != nil {
		t.Fatal("ordinary parser acquired owner/ACL/provenance")
	}
	copyOutside := *d
	if err := provider.Validate(view, &copyOutside); err == nil {
		t.Fatal("copied descriptor outside supplied view admitted")
	}
	if _, err := catalog.ResolveParserRuntime(view, &copyOutside); err == nil {
		t.Fatal("runtime bypassed supplied-view residency")
	}
	withoutHeader := catalog.CloneState(view.State())
	withoutHeader.RemoveDescriptor(ext.Address())
	headerless := view.WithState(withoutHeader)
	if err := provider.Validate(headerless, d); err == nil {
		t.Fatal("publication admitted a missing reciprocal header")
	}
	if _, err := catalog.ResolveParserRuntime(headerless, d); err != nil {
		t.Fatal("runtime acquired live e-header gate", err)
	}
	state := catalog.CloneState(view.State())
	resident := *d
	resident.ObjectRevision++
	state.SetDescriptor(d.Address(), &resident)
	current := view.WithState(state)
	if err := provider.Validate(current, &resident); err != nil {
		t.Fatal("genuine revised resident rejected", err)
	}
	if err := provider.Validate(current, d); err == nil {
		t.Fatal("old descriptor admitted in replacement view")
	}
	// The local detached descriptor is valid; only its unchanged parent's graph
	// is stale. Runtime admission must not grow a live extension-header gate.
	detached := resident
	detached.Extension = ids.ObjectAddress{}
	state.SetDescriptor(d.Address(), &detached)
	if err := provider.Validate(current, &detached); err != nil {
		t.Fatal("lawful local detach rejected", err)
	}
	if err := catalog.ValidateExtensionMembership(current, current.MustExtension(ext.Address())); err == nil {
		t.Fatal("stale parent reciprocal graph admitted")
	}
	if _, err := catalog.ResolveParserRuntime(current, &detached); err != nil {
		t.Fatal("runtime acquired live e-header requirement", err)
	}
	parent := *current.MustExtension(ext.Address())
	parent.Members = slices.DeleteFunc(slices.Clone(parent.Members), func(a ids.ObjectAddress) bool { return a == d.Address() })
	state.SetDescriptor(parent.Address(), &parent)
	if err := catalog.ValidateExtensionMembership(current, &parent); err != nil {
		t.Fatal("coherent detach rejected", err)
	}
	enginetest.RoleExecute(t, e, op, `CREATE EXTENSION fuzzystrmatch`)
	foreignView := e.CatalogRead()
	foreign := foreignView.MustExtension(foreignView.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"}))
	foreignParent := *foreign
	foreignParser := detached
	foreignParser.Extension = foreign.Address()
	foreignParent.Members = append(slices.Clone(foreign.Members), d.Address())
	state = catalog.CloneState(foreignView.State())
	current = foreignView.WithState(state)
	state.SetDescriptor(parent.Address(), &parent)
	state.SetDescriptor(foreign.Address(), &foreignParent)
	state.BindName(catalog.DescriptorNameKey(&foreignParent), foreign.Address())
	state.SetDescriptor(d.Address(), &foreignParser)
	if err := provider.Validate(current, &foreignParser); err != nil {
		t.Fatal("coherent foreign membership rejected", err)
	}
	if err := catalog.ValidateExtensionMembership(current, &foreignParent); err != nil {
		t.Fatal(err)
	}
	// Mutate a real represented callback's signature/result, not a fake loader
	// declaration. dsimple_init has exactly the lextypes SQL signature.
	for _, signature := range []bool{true, false} {
		candidate := catalog.CloneState(view.State())
		callback := *view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725})
		bad := *d
		bad.LexType = callback.Address().ObjectID
		candidate.SetDescriptor(bad.Address(), &bad)
		candidate.SetDescriptor(callback.Address(), &callback)
		checked := view.WithState(candidate)
		if err := provider.Validate(checked, &bad); err != nil {
			t.Fatal("real callback baseline", err)
		}
		if signature {
			callback.Signature = "23"
		} else {
			callback.Result = 23
		}
		if err := provider.Validate(checked, &bad); err == nil {
			t.Fatal("corrupt genuine callback admitted")
		}
	}
	ownershipParserOwnerCodec(t, e, d)
	enginetest.RoleExecute(t, e, op, `CREATE SCHEMA eo_parser_destination`)
	enginetest.RoleExecute(t, e, op, `CREATE TEXT SEARCH PARSER eo_parser_destination.eo_parser (START=pg_catalog.prsd_start, GETTOKEN=pg_catalog.prsd_nexttoken, END=pg_catalog.prsd_end, LEXTYPES=pg_catalog.prsd_lextype)`)
	atomic := e.CatalogRead()
	err = extensionUnitExecute(t, e, op, enginetest.Statement(t, `ALTER EXTENSION unaccent SET SCHEMA eo_parser_destination`))
	extensionUnitError(t, err, "42710")
	for a, old := range atomic.Descriptors() {
		if e.CatalogRead().Get(a) != old {
			t.Fatal("late parser collision published earlier moves", a)
		}
	}
	// Exercise the actual generic lower gate with a genuine nonsuperuser. The
	// same-namespace return precedes it; a collision must not mask it.
	actor := roleAttributeCreate(t, e, op, "eo_parser_actor", true, parser.RoleOptions{})
	enginetest.RoleExecute(t, e, op, `GRANT CREATE ON SCHEMA eo_parser_destination TO eo_parser_actor`)
	actorOp, actorDone := enginetest.RoleOperation(t, e, actor.Name)
	defer actorDone()
	e.Mu.Lock()
	auth, authErr := e.AuthorizationLocked(actorOp)
	if authErr != nil {
		e.Mu.Unlock()
		t.Fatal(authErr)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, actorOp.Session)
	host := schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: actorOp, CatalogStage: stage}}
	_, sameErr := ddl.StageObjectNamespace(host, auth, d, d.Namespace())
	destination := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "eo_parser_destination"})
	_, moveErr := ddl.StageObjectNamespace(host, auth, d, destination)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if sameErr != nil {
		t.Fatal("same-namespace lower move was not a no-op", sameErr)
	}
	extensionUnitError(t, moveErr, "42501")
	if !strings.Contains(moveErr.Error(), "must be superuser to set schema of text search parser") || stage.DescriptorCount() != 0 || e.CatalogRead().Get(d.Address()) != d {
		t.Fatal("lower authority/collision precedence or atomicity", moveErr)
	}
	if err := extensionUnitExecute(t, e, op, extensionUnitGrant(e.CatalogRead().MustDatabase(catalog.CurrentDatabaseAddress).Name(), actor.Name)); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, actorOp, `CREATE SCHEMA eo_parser_actor_schema`)
	enginetest.RoleExecute(t, e, actorOp, `CREATE EXTENSION "uuid-ossp" SCHEMA eo_parser_actor_schema`)
	enginetest.RoleExecute(t, e, op, `CREATE TEXT SEARCH PARSER eo_parser_actor_schema.owned_schema_parser (START=pg_catalog.prsd_start, GETTOKEN=pg_catalog.prsd_nexttoken, END=pg_catalog.prsd_end, LEXTYPES=pg_catalog.prsd_lextype)`)
	for _, action := range []string{"ADD", "DROP"} {
		if action == "DROP" {
			enginetest.RoleExecute(t, e, op, `ALTER EXTENSION "uuid-ossp" ADD TEXT SEARCH PARSER eo_parser_actor_schema.owned_schema_parser`)
		}
		snapshot := e.CatalogRead()
		err := extensionUnitExecute(t, e, actorOp, enginetest.Statement(t, `ALTER EXTENSION "uuid-ossp" `+action+` TEXT SEARCH PARSER eo_parser_actor_schema.owned_schema_parser`))
		extensionUnitError(t, err, "42501")
		if err.Error() != "stuntgres: must be superuser" {
			t.Fatal("membership invented parser owner or ordinary CREATE diagnostic", err)
		}
		for a, old := range snapshot.Descriptors() {
			if e.CatalogRead().Get(a) != old {
				t.Fatal("denied membership published", action, a)
			}
		}
	}
	seed := e.CatalogRead().MustTSParser(parserSeedAddress)
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent ADD TEXT SEARCH PARSER pg_catalog.default`)
	defer enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent DROP TEXT SEARCH PARSER pg_catalog.default`)
	for _, mode := range []parser.DropMode{parser.DropRestrict, parser.DropCascade} {
		failed, err := parserStage(t, e, op, &parser.DropParser{Names: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "default"}}}}, Mode: mode})
		e.DiscardCatalogStage(failed)
		extensionUnitError(t, err, "2BP01")
		if !strings.Contains(err.Error(), "because it is required by the database system") {
			t.Fatal("e membership masked pinned target rejection", err)
		}
	}
	seedMember := e.CatalogRead().MustTSParser(parserSeedAddress)
	payload = *seedMember
	payload.Extension, payload.ObjectRevision = seed.Extension, seed.ObjectRevision
	if payload != *seed || seedMember.Extension != ext.Address() {
		t.Fatal("seed membership changed fixed parser definition")
	}
}

func ownershipParserOwnerCodec(t *testing.T, e *core.Engine, d *catalog.TSParser) {
	t.Helper()
	view := e.CatalogRead()
	ext := view.MustExtension(d.Extension)
	text, err := extension.OwnerMetadata(view, e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(text, "-- ")))
	if err != nil || !found {
		t.Fatal(found, err)
	}
	index := -1
	for i, member := range record.Members {
		if member.Kind == "parser" {
			index = i
			if member.Schema != "public" || member.Name != d.Name() || member.Signature != "" || member.Declaration != nil || member.Version != "" || member.Owner != "" || member.ACL != nil {
				t.Fatal("ordinary parser acquired installed authority", member)
			}
		}
	}
	if index < 0 {
		t.Fatal("ordinary parser omitted")
	}
	if sql, err := extension.MemberSQL(view, d); err != nil || sql != `TEXT SEARCH PARSER "public"."eo_parser"` {
		t.Fatal(sql, err)
	}
	if changed, err := extension.ImportOwners(context.Background(), view.WithState(catalog.CloneState(view.State())), e.Roles(), []extension.OwnerRecord{record}); changed || err != nil {
		t.Fatal("genuine ordinary import", changed, err)
	}
	for _, field := range []string{"declaration", "version", "owner", "acl", "signature", "identity", "missing"} {
		bad := record
		bad.Members = slices.Clone(record.Members)
		m := &bad.Members[index]
		switch field {
		case "declaration":
			n := 999
			m.Declaration, m.Version = &n, ext.Version
		case "version":
			m.Version = ext.Version
		case "owner":
			m.Owner = "postgres"
		case "acl":
			m.ACL = &extension.MemberACL{}
		case "signature":
			m.Signature = "internal"
		case "identity":
			m.Name = "missing_parser"
		case "missing":
			bad.Members = slices.Delete(bad.Members, index, index+1)
		}
		data, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		_, _, decodeErr := extension.DecodeOwners(extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data))
		if field == "version" || field == "owner" || field == "acl" || field == "signature" {
			if decodeErr == nil {
				t.Fatal("forged ordinary metadata decoded", field)
			}
		}
		candidate := catalog.CloneState(view.State())
		if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), e.Roles(), []extension.OwnerRecord{bad}); changed || err == nil {
			t.Fatal("forged ordinary parser authority imported", field, changed, err)
		}
		for a, old := range view.Descriptors() {
			if candidate.Get(a) != old {
				t.Fatal("failed import mutated candidate", field, a)
			}
		}
	}
	candidate := catalog.CloneState(view.State())
	parent := *ext
	parent.Members = slices.DeleteFunc(slices.Clone(parent.Members), func(a ids.ObjectAddress) bool { return a == d.Address() })
	candidate.SetDescriptor(parent.Address(), &parent)
	if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), e.Roles(), []extension.OwnerRecord{record}); changed || err == nil {
		t.Fatal("import admitted nonreciprocal parent", changed, err)
	}
	parent.Members = append(slices.Clone(ext.Members), d.Address())
	if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), e.Roles(), []extension.OwnerRecord{record}); changed || err == nil {
		t.Fatal("import admitted duplicate parent member", changed, err)
	}
}

func ownershipParserRemap(t *testing.T) {
	e, ext, original, configuration := ownershipParserFixture(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent ADD TEXT SEARCH PARSER public.eo_parser`)
	enginetest.RoleExecute(t, e, op, `CREATE SCHEMA eo_parser_moved`)
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent SET SCHEMA eo_parser_moved`)
	view := e.CatalogRead()
	d := view.MustTSParser(original.Address())
	if d.Namespace() == original.Namespace() || d.Extension != ext.Address() || d.Start != original.Start || d.Token != original.Token || d.End != original.End || d.Headline != 0 || d.LexType != original.LexType || view.Get(configuration.Address()) != configuration {
		t.Fatal("mechanical move changed callbacks/configuration")
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		ext.Address():           {ClassID: ids.ExtensionClassID, ObjectID: 950001},
		d.Address():             {ClassID: ids.TSParserClassID, ObjectID: 950002},
		d.Namespace():           {ClassID: ids.NamespaceClassID, ObjectID: 950003},
		configuration.Address(): {ClassID: ids.TSConfigurationClassID, ObjectID: 950004},
	}
	for i, oid := range []ids.OID{d.Start, d.Token, d.End, d.LexType} {
		mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(950010 + i)}
	}
	remap := extension.DescriptorRemapper(mapping)
	mapped := remap(d).(*catalog.TSParser)
	mappedConfiguration := remap(configuration).(*catalog.TSConfiguration)
	mappedParent := remap(view.MustExtension(ext.Address())).(*catalog.Extension)
	if mapped.Address() != mapping[d.Address()] || mapped.Namespace() != mapping[d.Namespace()] || mapped.Extension != mappedParent.Address() || !slices.Contains(mappedParent.Members, mapped.Address()) || mappedConfiguration.Parser != mapped.Address() || mapped.Headline != 0 {
		t.Fatal("shared parser/member/configuration remap diverged")
	}
	for i, oid := range []ids.OID{mapped.Start, mapped.Token, mapped.End, mapped.LexType} {
		if oid != ids.OID(950010+i) {
			t.Fatal("callback identity did not follow shared map", i, oid)
		}
	}
	withHeadline := *d
	withHeadline.Headline = 3720
	mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3720}] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 950014}
	if got := extension.RemapDescriptor(&withHeadline, mapping).(*catalog.TSParser); got.Headline != 950014 {
		t.Fatal("nonzero optional callback not remapped")
	}
	fixed := extension.RemapDescriptor(d, map[ids.ObjectAddress]ids.ObjectAddress{d.Address(): mapping[d.Address()]}).(*catalog.TSParser)
	if fixed.Start != d.Start || fixed.Token != d.Token || fixed.End != d.End || fixed.LexType != d.LexType || fixed.Headline != 0 || original.Extension != (ids.ObjectAddress{}) || configuration.Parser != original.Address() {
		t.Fatal("remap mutated source or fixed callbacks")
	}
}
