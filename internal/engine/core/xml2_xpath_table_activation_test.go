package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestXML2XPathTableActivationCleanup(t *testing.T) {
	for _, test := range []struct {
		name                                     string
		key, document, relation, paths, criteria string
		cancel                                   bool
		wantRows                                 int
	}{
		{name: "success", wantRows: 2},
		{name: "multiple_commands", criteria: "false; INSERT INTO xt_owner_audit VALUES (1); SELECT 9,'<doc><a>10</a><a>20</a></doc>'", wantRows: 2},
		{name: "escaped_presentation", criteria: "false; SET DateStyle TO 'German, DMY'; SELECT 9,'<doc><a>10</a><a>20</a></doc>'", wantRows: 2},
		{name: "parse", criteria: "id = )"},
		{name: "analyze", key: "missing_xt_key"},
		{name: "drain", document: "(1/(id-1))::text"},
		{name: "width", key: "id,id", criteria: "false"},
		{name: "nonselect", criteria: "false; INSERT INTO xt_owner_audit VALUES (2)"},
		{name: "xml", paths: "["},
		{name: "input", key: "'not-an-integer'"},
		{name: "cancellation", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE EXTENSION xml2")
			enginetest.Run(t, e, "CREATE TABLE xt_owner_source(id integer,doc text)")
			enginetest.Run(t, e, "CREATE TABLE xt_owner_audit(id integer)")
			enginetest.Run(t, e, "CREATE DOMAIN xt_owner_number AS integer")
			enginetest.Run(t, e, "INSERT INTO xt_owner_source VALUES(1,'<doc><a>10</a><a>20</a></doc>')")
			n := xpathUnitExpression(t, e, "xpath_table", "integer")
			s := enginetest.StartSession(t, e, nil)
			enginetest.TransactionBegin(t, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scope.Owner.Mu.Lock()
			core.ScopeAttachTransactionLocked(scope)
			scope.Owner.Mu.Unlock()
			core.SetScopeWorked(scope, true)
			defer scope.Finish(context.Canceled)
			scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
			e.Mu.Lock()
			auth, err := core.ScopeWriteAuthorizationLocked(scope, e)
			if err == nil {
				err = e.CaptureElementInput(auth, op, &scope.Input)
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			scope.RoutineAuth = auth
			// Start inside a genuine ordinary STABLE policy. Table SPI must
			// override it lexically, not falsify the selected declaration.
			restoreOuter := scope.WriteHost().ActivateContribRoutineCommand(n.Procedure)
			defer restoreOuter()
			beforeView, beforeInput, beforeOutput := scope.View, scope.Input, scope.Output
			beforeAuthority, beforeExpected := core.ScopeAuthority(scope), scope.Expected
			beforeCatalog, beforePrincipal := scope.RoutineAuth.Catalog, scope.RoutineAuth.State.CurrentUser
			beforeDependencies := append([]catalog.ExpressionDependency(nil), scope.RoutineDependencies...)
			// Catalog resolver closures may be rebuilt after a reached nested
			// command. Assert lexical owners/boundaries exactly, then assert
			// their effective resolver and codec behavior independently.
			unchanged := func() bool {
				input, output := scope.Input, scope.Output
				checks := [...]struct {
					name string
					same bool
				}{
					{"view", scope.View == beforeView},
					{"authority", core.ScopeAuthority(scope) == beforeAuthority},
					{"expected", scope.Expected == beforeExpected},
					{"catalog_domain", scope.RoutineAuth.Catalog.Domain() == beforeCatalog.Domain()},
					{"catalog_epoch", scope.RoutineAuth.Catalog.Epoch() == beforeCatalog.Epoch()},
					{"principal", scope.RoutineAuth.State.CurrentUser == beforePrincipal},
					{"dependencies", reflect.DeepEqual(scope.RoutineDependencies, beforeDependencies)},
					{"transaction_start", input.TransactionStart == beforeInput.TransactionStart},
					{"exact_snapshot", input.ExactSnapshot == beforeInput.ExactSnapshot},
					{"raw_box_replay", input.RawBoxReplay == beforeInput.RawBoxReplay},
					{"locked_domain_input", reflect.DeepEqual(input.LockedDomainInput, beforeInput.LockedDomainInput)},
					{"domain_checks", reflect.DeepEqual(input.DomainChecks, beforeInput.DomainChecks)},
					{"input_type_io", reflect.DeepEqual(input.TypeIO, beforeInput.TypeIO)},
					{"input_conversion", reflect.DeepEqual(input.Conversion, beforeInput.Conversion)},
					{"index_support", reflect.DeepEqual(input.IndexSupport, beforeInput.IndexSupport)},
					{"fts", reflect.DeepEqual(input.FTS, beforeInput.FTS)},
					{"xml_input", reflect.DeepEqual(input.XMLInput, beforeInput.XMLInput)},
					{"output_type_io", reflect.DeepEqual(output.TypeIO, beforeOutput.TypeIO)},
					{"output_conversion", reflect.DeepEqual(output.Conversion, beforeOutput.Conversion)},
					{"output_regclass", reflect.DeepEqual(output.Regclass, beforeOutput.Regclass)},
					{"output_regtype", reflect.DeepEqual(output.Regtype, beforeOutput.Regtype)},
					{"output_regproc", reflect.DeepEqual(output.Regproc, beforeOutput.Regproc)},
					{"output_regrole", reflect.DeepEqual(output.Regrole, beforeOutput.Regrole)},
					{"output_operator", reflect.DeepEqual(output.Operator, beforeOutput.Operator)},
					{"output_regcollation", reflect.DeepEqual(output.Regcollation, beforeOutput.Regcollation)},
					{"output_regnamespace", reflect.DeepEqual(output.Regnamespace, beforeOutput.Regnamespace)},
					{"output_regts", reflect.DeepEqual(output.RegTS, beforeOutput.RegTS)},
					{"output_aclitem", reflect.DeepEqual(output.ACLItem, beforeOutput.ACLItem)},
				}
				same := true
				for _, check := range checks {
					if !check.same {
						t.Errorf("lexical invariant changed: %s", check.name)
						same = false
					}
				}
				return same
			}
			relationID, err := scope.Input.Regclass.ResolveRegclass(ctx, "xt_owner_source")
			if err != nil {
				t.Fatal("actual relation input capture", err)
			}
			domainID, err := scope.Input.Regtype.ResolveRegtype(ctx, "xt_owner_number")
			if err != nil {
				t.Fatal("actual type input capture", err)
			}
			wantStyle := beforeInput.DateStyle
			assertCurrent := func() {
				t.Helper()
				if scope.Input.Regclass == nil || scope.Input.Regtype == nil {
					t.Fatal("demanded catalog resolver capture lost")
				}
				relation, relationErr := scope.Input.Regclass.ResolveRegclass(ctx, "xt_owner_source")
				domain, domainErr := scope.Input.Regtype.ResolveRegtype(ctx, "xt_owner_number")
				if relationErr != nil || domainErr != nil || relation != relationID || domain != domainID {
					t.Fatal("resolver recapture lost genuine current object identity", relation, domain, relationErr, domainErr)
				}
				// Diagnostic naming is demand-driven, not an eagerly required
				// SQL input resolver. A recaptured namer must still name the
				// genuine current type correctly; absence keeps codec defaults.
				if name := scope.Input.TypeDiagnosticName; name != nil && name(sqltype.ID(domainID)) != "xt_owner_number" {
					t.Fatal("current diagnostic naming capture lost its type meaning")
				}
				if scope.Input.DateStyle != wantStyle || scope.Output.DateStyle != wantStyle {
					t.Fatal("current effective presentation lost", scope.Input.DateStyle, scope.Output.DateStyle, wantStyle)
				}
				d, ok := scope.RoutineAuth.Catalog.LookupType(sqltype.ID(domainID))
				if !ok {
					t.Fatal("current domain descriptor absent")
				}
				value, err := d.InputWithEnvironment(ctx, "42", -1, scope.Input)
				if err != nil {
					t.Fatal("current domain INPUT lost", err)
				}
				text, err := d.OutputWithEnvironment(ctx, value, scope.Output)
				if err != nil || text != "42" {
					t.Fatal("current domain OUTPUT lost", text, err)
				}
				if test.name == "escaped_presentation" && wantStyle.Style == sqltype.DateGerman {
					date, _ := scope.RoutineAuth.Catalog.LookupType(sqltype.Date)
					value, err := date.InputWithEnvironment(ctx, "31.12.2020", -1, scope.Input)
					if err != nil {
						t.Fatal("escaped SET did not reach actual date INPUT", err)
					}
					text, err := date.OutputWithEnvironment(ctx, value, scope.Output)
					if err != nil || text != "31.12.2020" {
						t.Fatal("escaped SET did not reach actual date OUTPUT", text, err)
					}
				}
			}
			assertCurrent()
			args := [5]string{"id", "doc", "xt_owner_source", "/doc/a", "true"}
			for i, value := range []string{test.key, test.document, test.relation, test.paths, test.criteria} {
				if value != "" {
					args[i] = value
				}
			}
			if test.cancel {
				cancel()
			}
			values, err := scope.WriteHost().Contrib().XML2XPathTable(args, n.Usage.Type, n.Procedure)
			if test.wantRows != 0 {
				if err != nil || len(values) != test.wantRows {
					t.Fatal("actual nested source", len(values), err)
				}
			} else if err == nil || values != nil {
				t.Fatal("failure published a materialized result", values, err)
			}
			if test.cancel && !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary cancellation lost", err)
			}
			if test.name == "escaped_presentation" {
				wantStyle = sqltype.DateStyle{Style: sqltype.DateGerman, Order: sqltype.DateDMY}
			}
			if !test.cancel {
				assertCurrent()
			}
			if !unchanged() {
				t.Fatal("nested activation leaked lexical caller state")
			}
			if test.name == "success" {
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
				cursor, err := n.OpenSource(&evaluation)
				if err != nil {
					t.Fatal(err)
				}
				value, present, err := cursor.Next(ctx)
				if err != nil || !present {
					t.Fatal("missing owned source", present, err)
				}
				cursor.Close()
				if cursor.Values != nil || cursor.State != nil || !unchanged() {
					t.Fatal("closed source retained invocation state")
				}
				if _, err := value.Type().Output(ctx, value); err != nil {
					t.Fatal("close invalidated owned value", err)
				}
			}
			if !test.cancel {
				host := scope.WriteHost()
				const text = "INSERT INTO xt_owner_audit VALUES (99)"
				read, statements, err := host.ParseNestedSQL(ctx, text)
				if err != nil || len(statements) != 1 {
					t.Fatal("policy probe parse", err)
				}
				sink := &xpathActivationSink{}
				err = host.PrepareNestedSQL(statements[0], read)
				if err == nil {
					err = host.ExecuteNestedSQL(statements[0], read, exec.NestedRequest{Text: text, FreshCommand: true, CloseFailed: true}, sink)
				}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "INSERT is not allowed in a non-volatile function" || sink.reached || !unchanged() {
					t.Fatal("table exit failed to restore prior ordinary STABLE policy", sink.reached, err)
				}
				assertCurrent()
			}
		})
	}
}

// The probe uses the real statement/journal path. No row delivery is expected:
// the restored ordinary routine policy must reject the INSERT before execution.
type xpathActivationSink struct{ reached bool }

func (*xpathActivationSink) NestedCommand(parser.Statement) (bool, error) { return false, nil }
func (*xpathActivationSink) NestedAnalyzed(parser.Statement) error        { return nil }
func (s *xpathActivationSink) NestedRows(_ parser.Statement, rows exec.RowResult) error {
	s.reached = true
	if rows != nil {
		return rows.Close()
	}
	return nil
}
