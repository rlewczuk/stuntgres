package core_test

import (
	"context"
	"maps"
	"slices"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Observe real preparation through the existing context-poll seam. No alternate
// activation or descriptor implementation participates in this ownership proof.
type xmlPreparationContext struct {
	context.Context
	scope *core.StatementScope
	address ids.ObjectAddress
	seen bool
	writes []storage.RowVersion
	deletes *visibility.PrivateDeletes
	wantWrites []storage.RowVersion
	wantDeletes map[ids.RowID]visibility.DeleteCandidate
	wrongFrontier bool
}

func (c *xmlPreparationContext) Err() error {
	var pcs [64]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	preparing, describing := false, false
	for {
		frame, more := frames.Next()
		preparing = preparing || strings.HasSuffix(frame.Function, ".(*StatementScope).xmlMappingPrepare")
		describing = describing || strings.HasSuffix(frame.Function, ".(*Engine).describeOperation")
		if !more { break }
	}
	if preparing && describing {
		c.seen = true
		c.writes = xmlPreparationRowsCopy(c.scope.OwnWrites[c.address])
		c.deletes = &visibility.PrivateDeletes{}
		if deleted := c.scope.OwnDeletes[c.address]; deleted != nil { c.deletes.Rows = maps.Clone(deleted.Rows) }
		c.wrongFrontier = c.wrongFrontier || !reflect.DeepEqual(c.writes, c.wantWrites) || !reflect.DeepEqual(c.deletes.Rows, c.wantDeletes)
	}
	return c.Context.Err()
}

func xmlPreparationRowsCopy(rows []storage.RowVersion) []storage.RowVersion {
	copy := slices.Clone(rows)
	for i := range copy {
		copy[i].Values = slices.Clone(rows[i].Values)
		copy[i].IndexOutputs = maps.Clone(rows[i].IndexOutputs)
		for address, output := range copy[i].IndexOutputs {
			output.Keys = slices.Clone(output.Keys)
			copy[i].IndexOutputs[address] = output
		}
	}
	return copy
}

// Keep immutable expected content independent of the live roots. Slice headers,
// value backing, delete pointers and candidate-map identity are saved separately,
// so neither an in-place mutation nor an equal replacement can pass.
type xmlPreparationRoots struct {
	writes map[ids.ObjectAddress][]storage.RowVersion
	deletes map[ids.ObjectAddress]*visibility.PrivateDeletes
	writeHeaders map[ids.ObjectAddress][]storage.RowVersion
	valueHeaders map[ids.ObjectAddress][][]sqltype.Value
	deletePointers map[ids.ObjectAddress]*visibility.PrivateDeletes
	deleteMaps map[ids.ObjectAddress]map[ids.RowID]visibility.DeleteCandidate
	wantWrites map[ids.ObjectAddress][]storage.RowVersion
	wantDeletes map[ids.ObjectAddress]*visibility.PrivateDeletes
}

func xmlPreparationSaveRoots(writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes) xmlPreparationRoots {
	saved := xmlPreparationRoots{
		writes: writes, deletes: deletes, writeHeaders: maps.Clone(writes), deletePointers: maps.Clone(deletes),
		valueHeaders: make(map[ids.ObjectAddress][][]sqltype.Value), deleteMaps: make(map[ids.ObjectAddress]map[ids.RowID]visibility.DeleteCandidate),
		wantWrites: make(map[ids.ObjectAddress][]storage.RowVersion), wantDeletes: make(map[ids.ObjectAddress]*visibility.PrivateDeletes),
	}
	for address, rows := range writes {
		saved.wantWrites[address] = xmlPreparationRowsCopy(rows)
		for _, row := range rows { saved.valueHeaders[address] = append(saved.valueHeaders[address], row.Values) }
	}
	for address, deleted := range deletes {
		saved.deleteMaps[address] = deleted.Rows
		saved.wantDeletes[address] = &visibility.PrivateDeletes{Rows: maps.Clone(deleted.Rows)}
	}
	return saved
}

func (saved xmlPreparationRoots) assert(t *testing.T, writes map[ids.ObjectAddress][]storage.RowVersion, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes) {
	t.Helper()
	if reflect.ValueOf(writes).UnsafePointer() != reflect.ValueOf(saved.writes).UnsafePointer() || reflect.ValueOf(deletes).UnsafePointer() != reflect.ValueOf(saved.deletes).UnsafePointer() {
		t.Fatal("private root map identity replaced")
	}
	// Normalize nil snapshots only for content: identity above still distinguishes
	// nil from a replacement empty map, and all actual witness roots are nonempty.
	if len(writes) != len(saved.wantWrites) || len(deletes) != len(saved.wantDeletes) { t.Fatal("private root membership changed") }
	for address, want := range saved.wantWrites {
		rows, header := writes[address], saved.writeHeaders[address]
		if !reflect.DeepEqual(rows, want) || len(rows) != len(header) || cap(rows) != cap(header) || reflect.ValueOf(rows).Pointer() != reflect.ValueOf(header).Pointer() { t.Fatal("private row content or backing changed") }
		for i, row := range rows {
			values := saved.valueHeaders[address][i]
			if len(row.Values) != len(values) || cap(row.Values) != cap(values) || reflect.ValueOf(row.Values).Pointer() != reflect.ValueOf(values).Pointer() { t.Fatal("private value backing changed") }
		}
	}
	for address, want := range saved.wantDeletes {
		deleted := deletes[address]
		if deleted != saved.deletePointers[address] || !reflect.DeepEqual(deleted, want) || reflect.ValueOf(deleted.Rows).UnsafePointer() != reflect.ValueOf(saved.deleteMaps[address]).UnsafePointer() { t.Fatal("private delete content or backing changed") }
	}
}

// refreshRestrictedPresentation intentionally recaptures settings, catalog input
// resolvers and actor identity. Preserve boundary/formatter owners, but compare
// recaptured scalar policy against the actual settings rather than the old image.
func assertXMLPreparationPresentation(t *testing.T, scope *core.StatementScope, beforeInput sqltype.InputEnvironment, beforeOutput sqltype.OutputEnvironment) {
	t.Helper()
	wantInput, wantOutput := scope.Op.EffectiveSettings().Presentation()
	input := scope.Input
	if input.Regclass == nil || input.Regtype == nil || input.TypeDiagnosticName == nil || input.TypeDiagnosticName(sqltype.Int4) == "" { t.Fatal("missing refreshed catalog input authority") }
	oid, err := input.Regclass.ResolveRegclass(context.Background(), "xr_prepare_owner")
	if err != nil || ids.OID(oid) != scope.QuerySources[0].Ref.Address.ObjectID { t.Fatal("refreshed resolver lost caller relation", oid, err) }
	wantInput.TransactionStart = beforeInput.TransactionStart
	wantInput.LockedDomainInput, wantInput.DomainChecks = beforeInput.LockedDomainInput, beforeInput.DomainChecks
	wantInput.ExactSnapshot, wantInput.RawBoxReplay = beforeInput.ExactSnapshot, beforeInput.RawBoxReplay
	wantInput.EnumMemberSafe, wantInput.FTS, wantInput.XMLInput = beforeInput.EnumMemberSafe, beforeInput.FTS, beforeInput.XMLInput
	// These are fresh catalog resolvers, not enclosing immutable formatter owners.
	// The actual relation lookup above independently checks their live authority.
	wantInput.Regclass, wantInput.Regtype, wantInput.Regrole = input.Regclass, input.Regtype, input.Regrole
	wantInput.Operator, wantInput.Regproc = input.Operator, input.Regproc
	wantInput.Regcollation, wantInput.Regnamespace, wantInput.RegTS, wantInput.ACLItem = input.Regcollation, input.Regnamespace, input.RegTS, input.ACLItem
	input.TypeDiagnosticName = nil
	if !reflect.DeepEqual(input, wantInput) { t.Fatal("refreshed input policy or enclosing boundary changed") }
	wantOutput.Regclass, wantOutput.Regtype, wantOutput.Regproc = beforeOutput.Regclass, beforeOutput.Regtype, beforeOutput.Regproc
	wantOutput.Regrole, wantOutput.Operator = beforeOutput.Regrole, beforeOutput.Operator
	wantOutput.Regcollation, wantOutput.Regnamespace, wantOutput.RegTS, wantOutput.ACLItem = beforeOutput.Regcollation, beforeOutput.Regnamespace, beforeOutput.RegTS, beforeOutput.ACLItem
	if !reflect.DeepEqual(scope.Output, wantOutput) { t.Fatal("refreshed output policy or enclosing formatter owner changed") }
	if scope.Inquiry.CurrentSchemaResult.Type() != nil { t.Fatal("actor-dependent inquiry cache was not cleared") }
	for _, cached := range core.InquirySearchPathResults(&scope.Inquiry) {
		if cached.Type() != nil { t.Fatal("actor-dependent search-path cache was not cleared") }
	}
}

func TestXMLMapRowsPreparationVisibility(t *testing.T) {
	for _, volatility := range []expr.Volatility{expr.Stable, expr.Volatile} {
		t.Run(map[expr.Volatility]string{expr.Stable: "stable", expr.Volatile: "volatile"}[volatility], func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			enginetest.RoleExecute(t, e, op, `CREATE TABLE xr_prepare_owner(x integer)`)
			enginetest.RoleExecute(t, e, op, `INSERT INTO xr_prepare_owner VALUES(1),(2)`)
			done()
			reader, _ := blockingUnitOwner(t, e)
			sessionSQL(t, reader, `INSERT INTO xr_prepare_owner VALUES(3)`)
			sessionSQL(t, reader, `DELETE FROM xr_prepare_owner WHERE x=1`)
			scope := pgrowlocksUnitScope(t, e, reader)
			address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"}), Name: "xr_prepare_owner"})
			if len(scope.OwnWrites[address]) != 1 || scope.OwnDeletes[address] == nil { t.Fatal("missing real private INSERT/DELETE") }
			integer := enginetest.Usage(e, sqltype.Int4).Type
			four, err := sqltype.Integer(integer, 4)
			if err != nil { t.Fatal(err) }
			e.Mu.Lock()
			staged, err := e.Storage.AllocateRowVersionsLocked([][]sqltype.Value{{four}}, e.Catalog.State)
			var original, removed storage.RowVersion
			for _, row := range e.Storage.Relations[address].Versions {
				value, _ := row.Values[0].Integer()
				if value == 1 { original = row }
				if value == 2 { removed = row }
			}
			e.Mu.Unlock()
			if err != nil || original.Version == 0 || removed.Version == 0 { t.Fatal("staged storage setup", err) }
			own := scope.OwnWrites[address][0]
			ownValue, ownErr := own.Values[0].Integer()
			if ownErr != nil || ownValue != 3 || own.Row == 0 || own.Version == 0 || own.Row == staged[0].Row || own.Version == staged[0].Version { t.Fatal("caller INSERT candidate is not distinct x3", own, ownErr) }
			deleted := scope.OwnDeletes[address].Rows
			if len(deleted) != 1 || deleted[original.Row].Row != original.Row || deleted[original.Row].Version != original.Version { t.Fatal("caller DELETE candidate is not original x1", deleted) }
			scope.Stage.Rows = map[ids.ObjectAddress][]storage.RowVersion{address: staged}
			scope.Stage.Deletes = map[ids.ObjectAddress]*visibility.PrivateDeletes{address: {Rows: map[ids.RowID]visibility.DeleteCandidate{removed.Row: {Row: removed.Row, Version: removed.Version}}}}
			// Real caller relation/source/view and authorization roots distinguish
			// restoration from an accidentally empty fresh lexical context.
			read := e.CatalogRead()
			relation, _ := read.Relation(address)
			if relation == nil { t.Fatal("missing caller relation descriptor") }
			ref := e.Catalog.Domain.Ref(read.Epoch(), address)
			if err := scope.AcquireRelation(enginetest.Statement(t, `SELECT x FROM xr_prepare_owner`), true); err != nil { t.Fatal(err) }
			if len(scope.QuerySources) == 0 { t.Fatal("missing caller relation source") }
			if err := core.ScopeSnapshotDataPhase(scope); err != nil { t.Fatal(err) }
			scope.Owner.Mu.Lock()
			e.Mu.Lock()
			outerView, viewErr := scope.AcquireDataViewsLocked(e, []ids.ObjectRef{ref})
			auth, authErr := core.ScopeWriteAuthorizationLocked(scope, e)
			e.Mu.Unlock()
			scope.Owner.Mu.Unlock()
			if viewErr != nil || authErr != nil { t.Fatal(viewErr, authErr) }
			scope.RoutineAuth = auth
			authority := &exec.Authority{Host: core.HostOf(scope)}
			core.SetScopeAuthority(scope, authority)
			core.SetScopeRLSDataRefs(scope, []ids.ObjectRef{ref})
			scope.Expected = &core.Description{Results: []catalog.Column{{Name: "caller_x", Usage: integer.Usage()}}}
			scope.Domains.InputCatalog = read
			scope.Inquiry.CommentCatalog = read
			scope.Identity, err = exec.CaptureIdentity(auth.Catalog, auth.Roles, auth.State, scope.Op.Session.DatabaseName)
			if err != nil { t.Fatal(err) }
			nameType, _ := read.LookupType(sqltype.Name)
			scope.Inquiry.CurrentSchemaResult = sqltype.String(nameType, "caller_cached_schema")
			*core.InquirySearchPathResults(&scope.Inquiry) = [2]sqltype.Value{sqltype.String(nameType, "caller_cached_path"), sqltype.String(nameType, "caller_cached_implicit_path")}
			scope.Input.LockedDomainInput, scope.Input.DomainChecks = scope, scope
			// A stale caller presentation must be refreshed, not restored over
			// effective settings; the enclosing execution boundary stays owned.
			if scope.Input.XMLMode == sqltype.XMLDocument { scope.Input.XMLMode = sqltype.XMLContent } else { scope.Input.XMLMode = sqltype.XMLDocument }
			scope.Output.XMLBinaryHex = !scope.Output.XMLBinaryHex
			scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: ref, Revision: relation.Revision()}}
			lexical := &proc.PLFrame{Values: []sqltype.Value{four}}
			lexicalValues := lexical.Values
			core.SetRoutineDemandLexical(core.ScopeSQLRoutineRef(scope), lexical)
			core.SetRoutineDemandPLDiagnostic(core.ScopeSQLRoutineRef(scope), "caller preparation ownership")
			core.SetRoutineDemandClosedParameters(core.ScopeSQLRoutineRef(scope), true)
			core.SetRoutineDemandScalar(core.ScopeSQLRoutineRef(scope), true)
			core.SetRoutineDemandNestedCursor(core.ScopeSQLRoutineRef(scope), true)
			leave := core.ActivateRoutineCommandPolicy(scope, &catalog.Procedure{Volatility: volatility}, false)
			defer leave()
			saved := core.ScopeSQLRoutineLexicalScratch(scope)
			demand := core.ScopeSQLRoutine(scope)
			ownRoots := xmlPreparationSaveRoots(scope.OwnWrites, scope.OwnDeletes)
			stageRoots := xmlPreparationSaveRoots(scope.Stage.Rows, scope.Stage.Deletes)
			viewData := outerView.Load()
			viewRoots := xmlPreparationSaveRoots(viewData.Own, viewData.Deleted)
			expected := scope.Expected
			expectedContent := *expected
			expectedContent.Results = slices.Clone(expected.Results)
			expectedColumns := expected.Results
			sources, sourceContent := scope.QuerySources, slices.Clone(scope.QuerySources)
			refs, refContent := core.ScopeRLSDataRefs(scope), slices.Clone(core.ScopeRLSDataRefs(scope))
			dependencies, dependencyContent := scope.RoutineDependencies, slices.Clone(scope.RoutineDependencies)
			beforeInput, beforeOutput := scope.Input, scope.Output
			// Readonly excludes the current stage; writable preparation must add
			// these exact disjoint candidates, not merely the same row count.
			wantRows := xmlPreparationRowsCopy(ownRoots.wantWrites[address])
			wantDeleted := maps.Clone(ownRoots.wantDeletes[address].Rows)
			if volatility == expr.Volatile {
				wantRows = append(wantRows, xmlPreparationRowsCopy(stageRoots.wantWrites[address])...)
				for row, candidate := range stageRoots.wantDeletes[address].Rows { wantDeleted[row] = candidate }
			}
			assertRestored := func() {
				t.Helper()
				ownRoots.assert(t, scope.OwnWrites, scope.OwnDeletes)
				stageRoots.assert(t, scope.Stage.Rows, scope.Stage.Deletes)
				viewRoots.assert(t, viewData.Own, viewData.Deleted)
				if scope.View != outerView || outerView.Load() != viewData || scope.Expected != expected || core.ScopeAuthority(scope) != authority || authority.Host != core.HostOf(scope) || scope.Domains.InputCatalog != read || scope.Inquiry.CommentCatalog != read {
					t.Fatal("caller view/source/authority/type/catalog owner changed")
				}
				if !reflect.DeepEqual(*scope.Expected, expectedContent) || reflect.ValueOf(scope.Expected.Results).Pointer() != reflect.ValueOf(expectedColumns).Pointer() || !reflect.DeepEqual(lexical.Values, []sqltype.Value{four}) || reflect.ValueOf(lexical.Values).Pointer() != reflect.ValueOf(lexicalValues).Pointer() { t.Fatal("caller description or lexical datum content/backing changed") }
				if !reflect.DeepEqual(scope.QuerySources, sourceContent) || reflect.ValueOf(scope.QuerySources).Pointer() != reflect.ValueOf(sources).Pointer() || !reflect.DeepEqual(core.ScopeRLSDataRefs(scope), refContent) || reflect.ValueOf(core.ScopeRLSDataRefs(scope)).Pointer() != reflect.ValueOf(refs).Pointer() || !reflect.DeepEqual(scope.RoutineDependencies, dependencyContent) || reflect.ValueOf(scope.RoutineDependencies).Pointer() != reflect.ValueOf(dependencies).Pointer() {
					t.Fatal("caller source/ref/dependency content or backing changed")
				}
				if !reflect.DeepEqual(saved, core.ScopeSQLRoutineLexicalScratch(scope)) || !core.SameSQLRoutineDemand(demand, core.ScopeSQLRoutine(scope)) || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(scope)) != lexical || core.ScopeActiveSQLRoutineDepth(scope) != 0 { t.Fatal("caller lexical scratch not restored") }
				// The lexical comparison intentionally excludes refreshed actor
				// and presentation fields. Assert their real semantics separately.
				e.Mu.Lock()
				liveAuth, err := scope.RoutineAuthorizationLocked()
				e.Mu.Unlock()
				if err != nil { t.Fatal(err) }
				if scope.RoutineAuth.Catalog != liveAuth.Catalog || !reflect.DeepEqual(scope.RoutineAuth.Roles, liveAuth.Roles) || scope.RoutineAuth.State != liveAuth.State || scope.RoutineAuth.Session != liveAuth.Session || scope.RoutineAuth.Statement != liveAuth.Statement { t.Fatal("refreshed authorization does not match live caller") }
				liveIdentity, err := exec.CaptureIdentity(liveAuth.Catalog, liveAuth.Roles, liveAuth.State, scope.Op.Session.DatabaseName)
				if err != nil || !reflect.DeepEqual(scope.Identity, liveIdentity) { t.Fatal("refreshed identity does not match live actor", err) }
				assertXMLPreparationPresentation(t, scope, beforeInput, beforeOutput)
			}
			text := enginetest.Usage(e, sqltype.Text).Type
			boolean := enginetest.Usage(e, sqltype.Bool).Type
			xml := enginetest.Usage(e, sqltype.XML).Type
			invoke := func(query string, operation functions.XMLMappingOperation) error {
				args := [5]sqltype.Value{sqltype.String(text, query), sqltype.Boolean(boolean, false), sqltype.Boolean(boolean, false), sqltype.String(text, "")}
				_, err := core.HostOf(scope).MapXMLRows(operation, args, xml, scope.Output)
				return err
			}
			for _, tc := range []struct{ query, state string }{
				{`SELECT x INTO xr_not_created FROM xr_prepare_owner`, "42P11"},
				{`SELECT absent INTO xr_not_created FROM xr_prepare_owner`, "42703"},
			} {
				observed := &xmlPreparationContext{Context: context.Background(), scope: scope, address: address, wantWrites: wantRows, wantDeletes: wantDeleted}
				scope.Op.Ctx = observed
				enginetest.State(t, invoke(tc.query, functions.XMLQuerySchema), tc.state)
				wantWrites, wantDeletes := 1, 1
				if volatility == expr.Volatile { wantWrites, wantDeletes = 2, 2 }
				if !observed.seen || observed.deletes == nil || observed.wrongFrontier || !reflect.DeepEqual(observed.writes, wantRows) || !reflect.DeepEqual(observed.deletes.Rows, wantDeleted) || len(observed.writes) != wantWrites || len(observed.deletes.Rows) != wantDeletes {
					t.Fatalf("preparation caller frontier: seen=%v writes=%d deletes=%v", observed.seen, len(observed.writes), observed.deletes)
				}
				assertRestored()
			}
			scope.Op.Ctx = context.Background()
			// Real execution admission still rejects writes after preparation's
			// temporary suppression; this is not a command-tag-only assertion.
			enginetest.State(t, invoke(`INSERT INTO xr_prepare_owner VALUES(5) RETURNING x`, functions.XMLQuerySchema), "0A000")
			assertRestored()
		})
	}
}
