package core_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func queryTemporaryAdmissionError(t *testing.T, err error, code, message, position, context string) {
	t.Helper()
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position, Context: context}
	var got *sqlstate.Error
	if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
		t.Fatalf("diagnostic = %#v (%v), want %#v", got, err, want)
	}
}

// Capture actual roots rather than deriving an expectation from the candidate.
func queryTemporaryAdmissionRoots(t *testing.T, e *core.Engine, s *session.Session) func() {
	t.Helper()
	journal := s.Transactions.Catalog
	unchanged := queryTemporaryAdmissionPublishedRoots(t, e, s)
	return func() {
		t.Helper()
		unchanged()
		if s.Transactions.Catalog != journal {
			t.Fatal("rejected/skipped CTAS changed active journal root")
		}
	}
}

func queryTemporaryAdmissionPublishedRoots(t *testing.T, e *core.Engine, s *session.Session) func() {
	t.Helper()
	catalog, namespace := e.Catalog.State, s.TemporaryNamespace
	tables, indexes, sequences := maps.Clone(e.Storage.Relations), maps.Clone(e.Storage.IndexRoots), maps.Clone(e.Storage.Sequences)
	types := queryCreateTypes(e)
	rows, versions, frontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier(), e.Visibility.CommitSeq()
	return func() {
		t.Helper()
		if e.Catalog.State != catalog || s.TemporaryNamespace != namespace || !maps.Equal(e.Storage.Relations, tables) || !maps.Equal(e.Storage.IndexRoots, indexes) || !maps.Equal(e.Storage.Sequences, sequences) || e.Storage.RowFrontier() != rows || e.Storage.VersionFrontier() != versions || e.Visibility.CommitSeq() != frontier {
			t.Fatal("rejected/skipped CTAS changed catalog/storage/index/namespace/journal roots")
		}
		queryCreateAssertTypes(t, e, types)
	}
}

func queryTemporaryAdmissionSyntax(t *testing.T, prefix string, noData bool) *parser.CreateQueryTable {
	t.Helper()
	q := prefix + " AS SELECT 7::integer AS v"
	if noData {
		q += " WITH NO DATA"
	}
	return enginetest.Statement(t, q).(*parser.CreateQueryTable)
}

func queryTemporaryAdmissionDescribe(t *testing.T, e *core.Engine, s *session.Session, syntax parser.Statement) core.Description {
	t.Helper()
	unchanged := queryTemporaryAdmissionRoots(t, e, s)
	op, scope := queryTemporaryScope(t, e, s, context.Background())
	description, err := scope.Describe(syntax)
	err = scope.Finish(err)
	op.Done()
	if err != nil {
		t.Fatal("effect-free Prepare", err)
	}
	unchanged()
	return description
}

func TestQueryTableCreateTemporaryTablespaceAdmission(t *testing.T) {
	for _, noData := range []bool{false, true} {
		for _, target := range []string{"CREATE TEMP TABLE admission_copy", "CREATE TABLE pg_temp.admission_copy", "CREATE TABLE admission_copy"} {
			for _, policy := range []string{"default-global", "temp-global", "both-global"} {
				t.Run(fmt.Sprintf("%s/%s/no-data-%t", policy, target, noData), func(t *testing.T) {
					e := engine.New()
					s := enginetest.StartSession(t, e, nil)
					if target == "CREATE TABLE admission_copy" {
						queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE TEMP TABLE admission_bootstrap(v INT)"))
						queryTemporaryCommand(t, e, s, enginetest.Statement(t, "SET search_path=pg_temp,public"))
					}
					if policy != "temp-global" {
						queryTemporaryCommand(t, e, s, enginetest.Statement(t, "SET default_tablespace='pg_global'"))
					}
					if policy != "default-global" {
						queryTemporaryCommand(t, e, s, enginetest.Statement(t, "SET temp_tablespaces='pg_global'"))
					}
					syntax := queryTemporaryAdmissionSyntax(t, target, noData)
					description := queryTemporaryAdmissionDescribe(t, e, s, syntax)
					unchanged := queryTemporaryAdmissionRoots(t, e, s)
					op, scope := queryTemporaryScope(t, e, s, context.Background())
					scope.Expected = &description
					if err := scope.Gate(syntax); err != nil {
						t.Fatal("admission failed at Gate", err)
					}
					result, err := scope.Execute(syntax, 0, nil)
					if policy == "default-global" {
						if err != nil {
							t.Fatal("TEMP consulted default_tablespace", err)
						}
						d := queryTemporaryRelation(t, e.PrivateCatalogLocked(scope.Catalog), s.SessionToken, "admission_copy")
						count := 1
						if noData {
							count = 0
						}
						if d.Persistence != "temporary" || result.Count != int64(count) || result.Rows != nil || scope.Completed || len(scope.Stage.Rows[d.Address()]) != count {
							t.Fatal("missing private resolved-TEMP destination", result, d)
						}
						queryTemporaryType(t, e.PrivateCatalogLocked(scope.Catalog), d)
						if e.CatalogRead().Get(d.Address()) != nil || e.Storage.Relations[d.Address()] != nil {
							t.Fatal("Execute published before Finish")
						}
						if err := scope.Finish(nil); err != nil {
							t.Fatal(err)
						}
						op.Done()
						if e.CatalogRead().Get(d.Address()) != d || len(e.Storage.Relations[d.Address()].Versions) != count {
							t.Fatal("Finish lost admitted destination")
						}
					} else {
						queryTemporaryAdmissionError(t, err, "22023", "only shared relations can be placed in pg_global tablespace", "", "")
						if scope.Catalog != nil || len(scope.Stage.Rows) != 0 {
							t.Fatal("rejection allocated a catalog stage or demanded rows")
						}
						unchanged()
						finishErr := scope.Finish(err)
						op.Done()
						if !errors.Is(finishErr, err) {
							t.Fatal("Finish replaced admission error", finishErr)
						}
						unchanged()
					}
				})
			}
		}
	}
}

func queryTemporaryAlign(t *testing.T, s *session.Session, assignment *session.TempTablespaceAssignment, next ids.OID) {
	t.Helper()
	spaces := []ids.OID{0, 1664}
	s.Transactions.TempTablespaces.Install(s.Transactions.Ref, assignment, spaces)
	previous := ids.OID(1664)
	if next == 1664 {
		previous = 0
	}
	for range len(spaces) {
		if s.Transactions.TempTablespaces.Next() == previous {
			return
		}
	}
	t.Fatal("real selector omitted member", previous)
}

func TestQueryTableCreateTemporaryTablespaceSelection(t *testing.T) {
	for _, noData := range []bool{false, true} {
		t.Run(fmt.Sprintf("no-data-%t", noData), func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE TEMP TABLE admission_bootstrap(v INT)"))
			ref := enginetest.TransactionBegin(t, s)
			queryTemporaryCommand(t, e, s, enginetest.Statement(t, "SET temp_tablespaces=pg_default, pg_global"))
			a := s.Settings.TempTablespaces
			if a == nil {
				t.Fatal("SET did not install its actual admitted assignment")
			}
			queryTemporaryAlign(t, s, a, 0)
			for i := range 6 {
				queryTemporaryCommand(t, e, s, enginetest.Statement(t, "SAVEPOINT admission_attempt"))
				unchanged := queryTemporaryAdmissionRoots(t, e, s)
				journal := s.Transactions.Catalog
				op, scope := queryTemporaryScope(t, e, s, context.Background())
				if op.TempTablespaceAssignment != a {
					t.Fatal("operation did not capture SET assignment")
				}
				syntax := queryTemporaryAdmissionSyntax(t, fmt.Sprintf("CREATE TEMP TABLE admission_cycle_%d", i), noData)
				result, err := scope.Execute(syntax, 0, nil)
				if i%2 == 0 {
					if err != nil {
						t.Fatal("next actual member should be default", err)
					}
					d := queryTemporaryRelation(t, e.PrivateCatalogLocked(scope.Catalog), s.SessionToken, fmt.Sprintf("admission_cycle_%d", i))
					count := int64(1)
					if noData {
						count = 0
					}
					if result.Count != count || result.Rows != nil || scope.Completed || s.Transactions.Catalog != journal || e.CatalogRead().Get(d.Address()) != nil {
						t.Fatal("successful selection escaped private journal", result)
					}
				} else {
					queryTemporaryAdmissionError(t, err, "22023", "only shared relations can be placed in pg_global tablespace", "", "")
					if scope.Catalog != nil || len(scope.Stage.Rows) != 0 {
						t.Fatal("failed selection allocated destination")
					}
					unchanged()
				}
				finishErr := scope.Finish(err)
				op.Done()
				if !errors.Is(finishErr, err) {
					t.Fatal(finishErr)
				}
				if i%2 != 0 {
					unchanged()
					queryTemporaryCommand(t, e, s, enginetest.Statement(t, "ROLLBACK TO admission_attempt"))
					if s.Transactions.Catalog == nil || !maps.Equal(s.Transactions.Catalog.Descriptors, journal.Descriptors) {
						t.Fatal("failed attempt lost predecessor descriptors")
					}
				}
				queryTemporaryCommand(t, e, s, enginetest.Statement(t, "RELEASE admission_attempt"))
				if !s.Transactions.TempTablespaces.Current(ref, a) {
					t.Fatal("unchanged-setting savepoint recovery reconstructed selector")
				}
			}
			if s.Transactions.TempTablespaces.Next() != 0 {
				t.Fatal("failed CTAS did not consume real global member")
			}
			enginetest.TransactionEnd(t, s, ref, false)
			if s.Transactions.TempTablespaces.Current(ref, a) {
				t.Fatal("ended transaction retained its selector incarnation")
			}
		})
	}
}

func TestQueryTableCreateTemporaryRestrictedAdmission(t *testing.T) {
	for _, noData := range []bool{false, true} {
		for _, owned := range []bool{false, true} {
			for _, target := range []string{"CREATE TEMP TABLE admission_copy", "CREATE TABLE pg_temp.admission_copy", "CREATE TABLE admission_copy"} {
				if !owned && target == "CREATE TABLE admission_copy" {
					continue
				}
				t.Run(fmt.Sprintf("%s/owned-%t/no-data-%t", target, owned, noData), func(t *testing.T) {
					e := engine.New()
					s := enginetest.StartSession(t, e, nil)
					if owned {
						queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE TEMP TABLE admission_sentinel AS SELECT 73::integer AS v"))
					}
					if target == "CREATE TABLE admission_copy" {
						queryTemporaryCommand(t, e, s, enginetest.Statement(t, "SET search_path=pg_temp,public"))
					}
					ref := enginetest.TransactionBegin(t, s)
					queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE TABLE public.admission_predecessor(v INT)"))
					before := s.Transactions.Catalog
					descriptors := maps.Clone(before.Descriptors)
					syntax := queryTemporaryAdmissionSyntax(t, target, noData)
					description := queryTemporaryAdmissionDescribe(t, e, s, syntax)
					unchanged := queryTemporaryAdmissionRoots(t, e, s)
					publishedUnchanged := queryTemporaryAdmissionPublishedRoots(t, e, s)
					op, scope := queryTemporaryScope(t, e, s, context.Background())
					op.Restricted = &session.RestrictedInvocation{Owner: op.RoleState().CurrentUser}
					scope.Expected = &description
					if err := scope.Gate(syntax); err != nil {
						t.Fatal("resolved TEMP admission ran before Execute", err)
					}
					_, err := scope.Execute(syntax, 0, nil)
					queryTemporaryAdmissionError(t, err, "42501", "cannot create temporary table within security-restricted operation", "", "")
					if scope.Catalog != nil || len(scope.Stage.Rows) != 0 || !maps.Equal(before.Descriptors, descriptors) {
						t.Fatal("restriction allocated stage/rows or changed original journal")
					}
					unchanged()
					finishErr := scope.Finish(err)
					op.Restricted = nil
					op.Done()
					if !errors.Is(finishErr, err) {
						t.Fatal(finishErr)
					}
					// Failed explicit Finish aborts the predecessor journal, not its
					// immutable backing or any already-published root.
					if s.Transactions.State != session.TxAborted || s.Transactions.Catalog != nil || !maps.Equal(before.Descriptors, descriptors) {
						t.Fatal("failed Finish did not discard the explicit journal immutably")
					}
					publishedUnchanged()
					enginetest.TransactionEnd(t, s, ref, false)
				})
			}
		}
	}
	t.Run("restricted-skip-order-and-selector", queryTemporaryRestrictedSkip)
	t.Run("resolved-persistent-control", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		op, scope := queryTemporaryScope(t, e, s, context.Background())
		op.Restricted = &session.RestrictedInvocation{Owner: op.RoleState().CurrentUser}
		result, err := scope.Execute(queryTemporaryAdmissionSyntax(t, "CREATE TABLE public.admission_persistent", false), 0, nil)
		if err == nil {
			err = scope.Finish(nil)
		} else {
			_ = scope.Finish(err)
		}
		op.Restricted = nil
		op.Done()
		if err != nil || result.Count != 1 {
			t.Fatal("restriction rejected resolved persistent destination", result, err)
		}
	})
	t.Run("real-cluster", queryTemporaryRestrictedCluster)
}

func queryTemporaryRestrictedSkip(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	queryTemporaryCommand(t, e, s, enginetest.Statement(t, "CREATE TEMP TABLE admission_existing ON COMMIT PRESERVE ROWS AS SELECT 73::integer AS v"))
	d := queryTemporaryRelation(t, e.CatalogRead(), s.SessionToken, "admission_existing")
	store := e.Storage.Relations[d.Address()]
	rows := append(store.Versions[:0:0], store.Versions...)
	ref := enginetest.TransactionBegin(t, s)
	queryTemporaryCommand(t, e, s, enginetest.Statement(t, "SET temp_tablespaces=pg_default, pg_global"))
	for _, witness := range []string{"skip", "skip-no-data", "duplicate", "missing-source"} {
		unchanged := queryTemporaryAdmissionRoots(t, e, s)
		op, scope := queryTemporaryScope(t, e, s, context.Background())
		op.Restricted = &session.RestrictedInvocation{Owner: op.RoleState().CurrentUser}
		op.RefreshInvocationSettingsLocked(op.EffectiveSettings())
		a := op.TempTablespaceAssignment
		queryTemporaryAlign(t, s, a, 1664)
		q := "CREATE TABLE IF NOT EXISTS pg_temp.admission_existing(changed) ON COMMIT DROP AS SELECT 1/0 AS v"
		if witness == "skip-no-data" {
			q += " WITH NO DATA"
		}
		if witness == "duplicate" {
			q = "CREATE TABLE pg_temp.admission_existing AS SELECT 1/0 AS v"
		}
		if witness == "missing-source" {
			q = "CREATE TABLE IF NOT EXISTS pg_temp.admission_existing AS SELECT * FROM admission_missing_source"
		}
		result, err := scope.Execute(enginetest.Statement(t, q), 0, nil)
		if strings.HasPrefix(witness, "skip") {
			if err != nil || result.Count != 0 || result.Rows != nil {
				t.Fatal("restricted existing target did not skip", result, err)
			}
			want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "admission_existing" already exists, skipping`}}
			if notices := op.DetachNotices(); !reflect.DeepEqual(notices, want) {
				t.Fatal("skip notice", notices, want)
			}
		} else if witness == "duplicate" {
			queryTemporaryAdmissionError(t, err, "42P07", `relation "admission_existing" already exists`, "", "")
		} else {
			queryTemporaryAdmissionError(t, err, "42P01", `relation "admission_missing_source" does not exist`, strconv.Itoa(strings.Index(q, "admission_missing_source")+1), "")
		}
		if scope.Catalog != nil || len(scope.Stage.Rows) != 0 || s.Transactions.TempTablespaces.Next() != 1664 {
			t.Fatal("skip/earlier error consumed TEMP policy or allocated destination")
		}
		unchanged()
		finishErr := scope.Finish(err)
		op.Restricted = nil
		op.Done()
		if !errors.Is(finishErr, err) {
			t.Fatal(finishErr)
		}
		if e.CatalogRead().Get(d.Address()) != d || d.OnCommit != parser.OnCommitPreserve || e.Storage.Relations[d.Address()] != store || !reflect.DeepEqual(store.Versions, rows) {
			t.Fatal("skip replaced original action/rows")
		}
		if err != nil {
			// Recover the actual explicit owner before the next independent witness.
			enginetest.TransactionEnd(t, s, ref, false)
			ref = enginetest.TransactionBegin(t, s)
			queryTemporaryCommand(t, e, s, enginetest.Statement(t, "SET temp_tablespaces=pg_default, pg_global"))
		}
	}
	enginetest.TransactionEnd(t, s, ref, false)
}
