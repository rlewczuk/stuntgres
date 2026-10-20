package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestFDWOptionsTransform(t *testing.T) {
	old := []catalog.ForeignOption{{"a", "1"}, {"b", "2"}}
	change := func(action, name, value string) parser.ForeignOption {
		return parser.ForeignOption{Action: action, Name: parser.Identifier{Text: name}, Value: value}
	}
	got, err := transformForeignOptions(old, []parser.ForeignOption{change("set", "a", "3"), change("drop", "b", ""), change("add", "c", "4")})
	if err != nil || !reflect.DeepEqual(got, []catalog.ForeignOption{{"a", "3"}, {"c", "4"}}) || !reflect.DeepEqual(old, []catalog.ForeignOption{{"a", "1"}, {"b", "2"}}) {
		t.Fatal(got, old, err)
	}
	for _, tc := range []struct {
		changes []parser.ForeignOption
		code    string
	}{
		{[]parser.ForeignOption{change("set", "a", "bad"), change("add", "b", "duplicate")}, "42710"},
		{[]parser.ForeignOption{change("drop", "absent", "")}, "42704"},
		{[]parser.ForeignOption{change("add", "x=y", "v")}, "22023"},
	} {
		got, err = transformForeignOptions(old, tc.changes)
		var diagnostic *sqlstate.Error
		if got != nil || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || old[0].Value != "1" {
			t.Fatal(got, err, old)
		}
	}
	got, err = transformForeignOptions(nil, nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatal("empty validator list", got, err)
	}
	got, err = transformForeignOptions(nil, []parser.ForeignOption{change("add", "x", "1"), change("set", "x", "2"), change("drop", "x", ""), change("add", "x", "3")})
	if err != nil || !reflect.DeepEqual(got, []catalog.ForeignOption{{"x", "3"}}) {
		t.Fatal(got, err)
	}
}

func TestFDWCatalogOverlay(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	owner := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	wrapper := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignWrapperClassID, ObjectID: 40000}, ObjectRevision: 1, ObjectName: "unit_wrapper", Owner: owner, Options: []catalog.ForeignOption{{"a", "1"}}}
	server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 40001}, ObjectRevision: 1, ObjectName: "unit_server", Owner: owner, Wrapper: wrapper.Address()}
	server.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}}
	mapping := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 40002}, ObjectRevision: 1, Server: server.Address(), MappedRole: owner, MappedName: "postgres"}
	mapping.ObjectName = catalog.ForeignMappingName(server.Address(), owner)
	public := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 40003}, ObjectRevision: 1, Server: server.Address()}
	public.ObjectName = catalog.ForeignMappingName(server.Address(), authorization.PrincipalRef{})
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	for _, d := range []*catalog.ForeignObject{wrapper, server, mapping, public} {
		if err := stage.Create(d); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := base.Descriptor(wrapper.Address()); ok {
		t.Fatal("private descriptor leaked into base")
	}
	if _, ok := e.stageRead(stage).Descriptor(wrapper.Address()); !ok {
		t.Fatal("stage overlay lost descriptor")
	}
	beforeStorage := len(e.storage.Relations)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	if len(e.storage.Relations) != beforeStorage {
		t.Fatal("SQL/MED metadata allocated physical storage")
	}
	if got := descriptorReferences(mapping); !slices.Equal(got, []authorization.PrincipalRef{owner}) {
		t.Fatal("mapping lost mapped-role dependency", got)
	}
	if got := descriptorReferences(public); len(got) != 0 {
		t.Fatal("PUBLIC acquired a principal dependency", got)
	}
	mappedOwner, _, _ := access.DescriptorACL(mapping)
	publicOwner, _, _ := access.DescriptorACL(public)
	if mappedOwner != owner || publicOwner != (authorization.PrincipalRef{}) {
		t.Fatal("mapping ownership is not mapped identity", mappedOwner, publicOwner)
	}
	e.mu.Lock()
	auth := catalogAuthorization(e, e.catalog.read(e.catalog.state))
	e.mu.Unlock()
	for _, d := range []*catalog.ForeignObject{wrapper, server} {
		ref := foreignRef(auth, d)
		for _, grantOptions := range []bool{false, true} {
			rights, err := auth.Effective(ref, grantOptions)
			if err != nil || rights != authorization.UsagePrivilege {
				t.Fatal("foreign owner mask", rights, err)
			}
		}
		if err := auth.CanUse(ref); err != nil {
			t.Fatal(err)
		}
		if err := auth.Check(access.Requirement{Object: ref, Privileges: authorization.UsagePrivilege}); err != nil {
			t.Fatal(err)
		}
	}
	auth.State.CurrentUser = auth.Roles.LookupRef("pg_read_all_data")
	if auth.State.CurrentUser == (authorization.PrincipalRef{}) {
		t.Fatal("missing real predefined reader")
	}
	for _, d := range []*catalog.ForeignObject{wrapper, server} {
		ref := foreignRef(auth, d)
		want := authorization.Privileges(0)
		if d == server {
			want = authorization.UsagePrivilege
		}
		rights, err := auth.Effective(ref, false)
		if err != nil || rights != want {
			t.Fatal("foreign nonowner mask", rights, want, err)
		}
		if err = auth.CanUse(ref); (err == nil) != (want != 0) {
			t.Fatal("foreign CanUse bypassed exact ACL", err)
		}
		rights, err = auth.Effective(ref, true)
		if err != nil || rights != 0 {
			t.Fatal("PUBLIC usage acquired grant options", rights, err)
		}
	}
	if access.ClassPrivileges(mapping) != 0 || access.ClassPrivileges(public) != 0 {
		t.Fatal("mapping exposed a grantable ACL mask")
	}
	alter := e.BeginCatalogStage(e.CatalogRead())
	next := *wrapper
	next.ObjectRevision++
	next.Options = []catalog.ForeignOption{{"a", "candidate"}}
	if err = alter.Replace(&next); err != nil {
		t.Fatal(err)
	}
	e.DiscardCatalogStage(alter)
	if committed, _ := e.CatalogRead().Descriptor(wrapper.Address()); committed.(*catalog.ForeignObject).Options[0].Value != "1" {
		t.Fatal("discard mutated committed options")
	}
}

type fdwUnitWrapper struct {
	paths    []catalog.ForeignPath
	chosen   catalog.ForeignPath
	scan     *fdwUnitScan
	beginErr error
}

func (*fdwUnitWrapper) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	return catalog.ForeignRelSize{Rows: 3}, nil
}
func (w *fdwUnitWrapper) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return w.paths, nil
}
func (w *fdwUnitWrapper) GetForeignPlan(_ context.Context, _ catalog.ForeignRequest, path catalog.ForeignPath) (catalog.ForeignPlan, error) {
	w.chosen = path
	return catalog.ForeignPlan{Attributes: path.Attributes, Private: path.Private}, nil
}
func (w *fdwUnitWrapper) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	return w.scan, w.beginErr
}

type fdwUnitScan struct {
	position, rescans, ends int
	rescanErr, endErr       error
	cleanupCanceled         bool
	onEnd                   func(context.Context) error
}

func (s *fdwUnitScan) IterateForeignScan(context.Context) (catalog.ForeignRow, bool, error) {
	if s.position == 3 {
		return nil, false, nil
	}
	s.position++
	return catalog.ForeignRow{}, true, nil
}
func (s *fdwUnitScan) ReScanForeignScan(context.Context, catalog.ForeignScanParameters) error {
	s.rescans++
	s.position = 0
	return s.rescanErr
}
func (s *fdwUnitScan) EndForeignScan(ctx context.Context) error {
	s.ends++
	s.cleanupCanceled = ctx.Err() != nil
	if s.onEnd != nil {
		return s.onEnd(ctx)
	}
	return s.endErr
}

func TestFDWRegistry(t *testing.T) {
	var missing *fdwUnitWrapper
	if _, err := ValidateForeignDataWrapper(ForeignDataWrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "handler"}, Wrapper: missing}); err == nil {
		t.Fatal("typed nil admitted")
	}
	w := &fdwUnitWrapper{}
	def, err := ValidateForeignDataWrapper(ForeignDataWrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "handler"}, Wrapper: w})
	if err != nil || def.HandlerName.Schema != "public" {
		t.Fatal(def, err)
	}
	if _, err = ValidateForeignValidator(ForeignValidatorDefinition{Name: catalog.ForeignFunctionName{Name: "validator"}}); err == nil {
		t.Fatal("nil validator admitted")
	}
	e := New()
	if err = e.InstallForeignDefinitions([]ForeignDataWrapperDefinition{def}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "handler", Signature: string(catalog.ForeignHandlerSignature)})
	procedure := e.catalog.state.MustProcedure(address)
	if procedure.Foreign.Wrapper != w || procedure.Result != 3115 || procedure.Signature != catalog.ForeignHandlerSignature {
		t.Fatal("installed declaration lost executable identity", procedure)
	}
	if err = e.catalog.Providers[ids.ProcedureClassID].Validate(e.CatalogRead(), procedure); err != nil {
		t.Fatal(err)
	}
}

func TestFDWProcedureIdentity(t *testing.T) {
	e := New()
	w := &fdwUnitWrapper{}
	def, _ := ValidateForeignDataWrapper(ForeignDataWrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "handler"}, Wrapper: w})
	if err := e.InstallForeignDefinitions([]ForeignDataWrapperDefinition{def}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "handler", Signature: string(catalog.ForeignHandlerSignature)}
	oldAddress := e.catalog.state.NameAddress(key)
	wrapper := &catalog.ForeignObject{ObjectName: "wrapper", Handler: oldAddress}
	if token, err := foreignHandlerFor(e.CatalogRead(), wrapper); err != nil || token.Wrapper != w {
		t.Fatal(token, err)
	}
	replacement := catalog.CloneState(e.catalog.state)
	copy := *replacement.MustProcedure(oldAddress)
	replacement.RemoveDescriptor(oldAddress)
	copy.ObjectAddress.ObjectID = 40010
	replacement.SetDescriptor(copy.Address(), &copy)
	replacement.BindName(key, copy.Address())
	_, err := foreignHandlerFor(e.catalog.read(replacement), wrapper)
	var diagnostic *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: `foreign-data wrapper "wrapper" has no registered handler`}
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal("retired address retargeted by same SQL name or lost defensive diagnostic", err)
	}
	uncallable := catalog.CloneState(e.catalog.state)
	procedure := *uncallable.MustProcedure(oldAddress)
	procedure.Foreign = nil
	uncallable.SetDescriptor(oldAddress, &procedure)
	_, err = foreignHandlerFor(e.catalog.read(uncallable), wrapper)
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal("a stored procedure name masqueraded as an executable handler", err)
	}
}

func TestFDWProcedureACLCOW(t *testing.T) {
	e := New()
	w := &fdwUnitWrapper{}
	handler, err := ValidateForeignDataWrapper(ForeignDataWrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "handler"}, Wrapper: w})
	if err != nil {
		t.Fatal(err)
	}
	validator, err := ValidateForeignValidator(ForeignValidatorDefinition{Name: catalog.ForeignFunctionName{Name: "validator"}, Validate: func(context.Context, catalog.ForeignObjectKind, []catalog.ForeignOption) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.InstallForeignDefinitions([]ForeignDataWrapperDefinition{handler}, []ForeignValidatorDefinition{validator}, nil, nil); err != nil {
		t.Fatal(err)
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, name := range []string{"handler", "validator"} {
		t.Run(name, func(t *testing.T) {
			base := e.CatalogRead()
			selected, count := selectRoutine(base, namespaceSelection{qualified: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}}, name, nil)
			if count != 1 {
				t.Fatal("registered procedure identity not unique", count)
			}
			ref := e.catalog.Domain.Ref(base.Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: selected.ID})
			original := base.MustProcedure(ref.Address)
			retained := catalogAuthorization(e, base)
			retained.State.CurrentUser = retained.Roles.LookupRef("pg_read_all_data")
			if rights, err := retained.Effective(ref, false); err != nil || rights != authorization.ExecutePrivilege {
				t.Fatal("initial authority unavailable", rights, err)
			}
			request := &parser.ObjectGrant{
				Kind:       "routine",
				Privileges: []parser.GrantPrivilege{{Name: "execute"}},
				Routines:   []parser.GrantRoutineTarget{{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}},
				Grantees:   []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "postgres"}}},
			}
			if err := applyObjectGrantUnit(e, op, request); err != nil {
				t.Fatal(err)
			}
			materializedCatalog := e.CatalogRead()
			materialized := materializedCatalog.MustProcedure(ref.Address)
			if materialized == original || !materialized.ACL.Explicit || original.ACL.Explicit {
				t.Fatal("default-equivalent edit did not privately materialize ACL")
			}
			request.Revoke = true
			request.Grantees = []parser.GrantGrantee{{Public: true}}
			if err := applyObjectGrantUnit(e, op, request); err != nil {
				t.Fatal(err)
			}
			current := e.CatalogRead()
			next := current.MustProcedure(ref.Address)
			if next == materialized || next.Foreign != original.Foreign || next.Revision() != original.Revision() || next.Owner != original.Owner || next.Namespace() != original.Namespace() || next.Signature != original.Signature {
				t.Fatal("ACL publication replaced foreign capability or definition identity")
			}
			if err := e.catalog.Providers[ids.ProcedureClassID].Validate(current, next); err != nil {
				t.Fatal("ACL copy lost native payload eligibility", err)
			}
			fresh := catalogAuthorization(e, current)
			fresh.State.CurrentUser = retained.State.CurrentUser
			if rights, err := fresh.Effective(ref, false); err != nil || rights != 0 {
				t.Fatal("original reference ignored current ACL", rights, err)
			}
			materializedAuthority := catalogAuthorization(e, materializedCatalog)
			materializedAuthority.State.CurrentUser = retained.State.CurrentUser
			if rights, err := materializedAuthority.Effective(ref, false); err != nil || rights != authorization.ExecutePrivilege {
				t.Fatal("revoke mutated materialized authority snapshot", rights, err)
			}
			if rights, err := retained.Effective(ref, false); err != nil || rights != authorization.ExecutePrivilege {
				t.Fatal("publication mutated retained authority", rights, err)
			}
			stable, roles := e.catalog.state, e.roles.published
			if err := applyObjectGrantUnit(e, op, request); err != nil || e.catalog.state != stable || e.roles.published != roles {
				t.Fatal("explicit no-op republished foreign procedure", err)
			}
		})
	}
}

func TestFDWPlanningSelection(t *testing.T) {
	request := catalog.ForeignRequest{Columns: []catalog.ForeignColumn{{Attribute: 1}, {Attribute: 3}}, RequiredAttributes: []int32{3}}
	w := &fdwUnitWrapper{paths: []catalog.ForeignPath{{TotalCost: 10, Attributes: []int32{1, 3}, Private: "expensive"}, {TotalCost: 2, Attributes: []int32{3}, Private: "first-cheapest"}, {TotalCost: 2, Attributes: []int32{3}, Private: "tie"}}}
	f := &foreignQuerySource{Wrapper: w}
	execution := &foreignExecution{}
	if err := f.Plan(context.Background(), request, execution); err != nil {
		t.Fatal(err)
	}
	if w.chosen.Private != "first-cheapest" || execution.Plan.Private != "first-cheapest" || !slices.Equal(execution.Slots, []int{1}) {
		t.Fatal(w.chosen, execution)
	}
	for _, paths := range [][]catalog.ForeignPath{nil, {{TotalCost: math.NaN(), Attributes: []int32{3}}}, {{TotalCost: 1, Attributes: []int32{1}}}, {{TotalCost: 1, Attributes: []int32{3, 3}}}, {{StartupCost: 2, TotalCost: 1, Attributes: []int32{3}}}} {
		w.paths = paths
		if err := f.Plan(context.Background(), request, &foreignExecution{}); err == nil {
			t.Fatal("invalid path admitted", paths)
		}
	}
	t.Run("ForeignMetadataPolicy", testFDWForeignMetadataPolicy)
}

func TestFDWScanState(t *testing.T) {
	ctx := context.Background()
	endFailure := errors.New("end outcome")
	scan := &fdwUnitScan{endErr: endFailure}
	w := &fdwUnitWrapper{paths: []catalog.ForeignPath{{}}, scan: scan}
	source := &querySource{Anonymous: [][]sqltype.Value{}, Foreign: &foreignQuerySource{Wrapper: w}}
	owner := &foreignScanOwner{}
	owner.Add(source.Foreign)
	state := &queryState{Source: source}
	stage, err := OpenForeignStage(ctx, state, source, &plan.SourceNode{})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if present, err := stage.Next(ctx, nil); err != nil || !present {
			t.Fatal("zero-width row became EOF", present, err)
		}
	}
	old := slices.Clone(stage.Selection)
	if present, err := stage.Next(ctx, nil); err != nil || present {
		t.Fatal(present, err)
	}
	if err = stage.Close(); err != nil {
		t.Fatal(err)
	}
	if scan.ends != 0 {
		t.Fatal("borrowed invocation ended query-owned scan")
	}
	again, err := OpenForeignStage(ctx, state, source, &plan.SourceNode{})
	if err != nil {
		t.Fatal(err)
	}
	if present, err := again.Next(ctx, nil); err != nil || !present {
		t.Fatal(present, err)
	}
	if scan.rescans != 1 || len(source.Foreign.Executions) != 1 || !slices.Equal(old, []int{0, 1, 2}) || again.Selection[0] != 3 {
		t.Fatal("rescan replaced occurrence identities", old, again.Selection, scan)
	}
	scan.rescanErr = errors.New("restart failed")
	if changed, err := again.Rewind(ctx); changed || !errors.Is(err, scan.rescanErr) {
		t.Fatal(changed, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = FinishForeignSources(canceled, source); !errors.Is(err, endFailure) {
		t.Fatal(err)
	}
	if err = FinishForeignSources(ctx, source); !errors.Is(err, endFailure) || scan.ends != 1 || scan.cleanupCanceled {
		t.Fatal("cleanup was canceled, duplicated or lost outcome", err, scan)
	}
	t.Run("CursorCleanupOwner", testFDWCursorCleanupOwner)
}

func testFDWCursorCleanupOwner(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	txn := s.transactions
	failure := errors.New("owned cursor cleanup")
	var blocked []string
	scan := &fdwUnitScan{onEnd: func(context.Context) error {
		check := func(name string, try func() bool, unlock func()) {
			if !try() {
				blocked = append(blocked, name)
			} else {
				unlock()
			}
		}
		check("engine", e.mu.TryLock, e.mu.Unlock)
		check("session", s.mu.TryLock, s.mu.Unlock)
		check("settings", s.settings.mu.TryLock, s.settings.mu.Unlock)
		check("transaction", txn.mu.TryLock, txn.mu.Unlock)
		check("replacement", e.replacementMu.TryLock, e.replacementMu.Unlock)
		return failure
	}}
	owner := &foreignScanOwner{}
	source := &foreignQuerySource{Executions: []*foreignExecution{{Scan: scan}}}
	owner.Add(source)
	cursor := &sqlCursor{ordinal: 1, foreignOwner: owner}
	s.cursors.entries = map[string]*sqlCursor{"child": cursor}
	e.replacementMu.RLock()
	e.mu.Lock()
	s.settings.mu.Lock()
	s.mu.Lock()
	txn.mu.Lock()
	txn.cursorCleanup = append(txn.cursorCleanup, s.cursors.rollback(0)...)
	premature := scan.ends
	txn.mu.Unlock()
	s.mu.Unlock()
	s.settings.mu.Unlock()
	e.mu.Unlock()
	e.replacementMu.RUnlock()
	if premature != 0 {
		t.Fatal("locked registry detach ran application cleanup")
	}
	if err := txn.drainCursorCleanup(context.Background(), nil); !errors.Is(err, failure) {
		t.Fatal("cleanup outcome was swallowed", err)
	}
	if len(blocked) != 0 || scan.ends != 1 || len(s.cursors.entries) != 0 || len(txn.cursorCleanup) != 0 || !source.LifetimeClosed || cursor.foreignOwner != nil {
		t.Fatal("owner/lock boundary", blocked, scan.ends)
	}
	if err := cursor.dispose(context.Background()); !errors.Is(err, failure) || scan.ends != 1 {
		t.Fatal("idempotent cursor disposal lost outcome", err, scan.ends)
	}
}

func testFDWForeignMetadataPolicy(t *testing.T) {
	plain := &parser.CreateTable{}
	if foreignTableSyntax(plain) != plain {
		t.Fatal("plain foreign syntax was unnecessarily copied")
	}
	column := &parser.Column{Name: "keep"}
	like := &parser.TableLike{Options: []parser.LikeOption{{Name: "all", Including: true}}}
	syntax := &parser.CreateTable{Elements: []parser.TableElement{{Like: like}, {Column: column}}}
	got := foreignTableSyntax(syntax)
	want := tableLikeMask(likeAll) &^ (likeIndexes | likeIdentity | likeStorage | likeCompression)
	if got == syntax || got.Elements[0].Like == like || tableLikeOptions(got.Elements[0].Like.Options) != want || tableLikeOptions(like.Options) != likeAll || got.Elements[1].Column != column {
		t.Fatal("foreign LIKE changed syntax or copied physical properties")
	}
	relation := &catalog.Relation{Attributes: []int32{1, 4, 7}, Columns: make([]catalog.Column, 3), ColumnMeta: catalog.ColumnMetadata{Generations: map[int32]catalog.ColumnGeneration{7: {Kind: catalog.GenerationVirtual, Planned: plan.ColumnExpr{Slot: 0}}}}}
	for _, tc := range []struct {
		uses []int
		want []int32
	}{{[]int{2}, []int32{1}}, {nil, []int32{1, 4}}, {[]int{2, 1, 0}, []int32{1, 4}}, {[]int{}, []int32{}}} {
		attributes, err := foreignRequiredAttributes(relation, tc.uses)
		if err != nil || !slices.Equal(attributes, tc.want) {
			t.Fatal("virtual source demand", attributes, tc.want, err)
		}
	}
	if _, err := foreignRequiredAttributes(relation, []int{3}); err == nil {
		t.Fatal("invalid required slot admitted")
	}
}
