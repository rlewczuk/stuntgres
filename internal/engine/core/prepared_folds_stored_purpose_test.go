package core_test

import (
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// The semantic revision below is a private catalog fixture, not SQL routine
// replacement support or a claim that PostgreSQL can observe private revisions.
func TestPreparedFoldStoredOperandPurpose(t *testing.T) {
	for _, executable := range []bool{false, true} {
		name := "OperandOnly"
		if executable {
			name = "ExecutableWins"
		}
		t.Run(name, func(t *testing.T) {
			e := rootengine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE FUNCTION fold_identifier_provider(integer) RETURNS integer LANGUAGE SQL AS $$SELECT abs($1)$$")
			enginetest.SessionSQL(t, s, "CREATE VIEW fold_identifier AS SELECT 'fold_identifier_provider(integer)'::regprocedure AS r")
			enginetest.SessionSQL(t, s, "CREATE VIEW fold_identifier_nested AS SELECT r FROM fold_identifier")
			enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
			// Resolve the actual frozen datum, not the direct input resolver's
			// optional provider-owner operands (which omit direct identities).
			_, operand := sequencePreparedDescription(t, e, s, "SELECT 'fold_identifier_provider(integer)'::regprocedure")
			if operand.Regtypes == nil || len(operand.Regtypes.Constants) != 1 {
				t.Fatal("missing regprocedure capture")
			}
			oid, err := operand.Regtypes.Constants[0].Value.ProcedureOID()
			if err != nil {
				t.Fatal(err)
			}
			ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(oid)})
			frozen := stored.ViewOf(primaryKeyUnitRelation(t, e, "fold_identifier"))
			seen := false
			for _, dependency := range frozen.Dependencies() {
				if dependency.Ref == ref {
					seen = true
					if dependency.Purpose != catalog.OperandDependency {
						t.Fatal("freeze promoted identifier-only routine to executable")
					}
				}
			}
			if !seen {
				t.Fatal("frozen view lost identifier dependency")
			}
			query := preparedFoldPoint
			if executable {
				query += ", fold_identifier_provider(-7::integer) AS invoked"
			}
			query += " FROM fold_identifier_nested"
			ast, d := sequencePreparedDescription(t, e, s, query)
			preparedFoldExecute(t, e, s, ast, &d)
			for _, name := range []string{"fold_identifier", "fold_identifier_nested"} {
				source := primaryKeyUnitRelation(t, e, name)
				sourceRef := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), source.Address())
				sourceSeen := false
				for _, dependency := range d.Analysis.Dependencies {
					if dependency.Ref == sourceRef {
						sourceSeen = true
						if dependency.Purpose != catalog.DefinitionDependency {
							t.Fatal("actual nested rewrite source lost executable purpose", name)
						}
					}
				}
				if !sourceSeen {
					t.Fatal("actual nested rewrite source lost analysis dependency", name)
				}
			}
			before := snapshotFoldDescription(d)
			preparedFoldEntry(t, before, sqltype.Text, "(0.10000000000000002,1)")
			seen = false
			for _, dependency := range d.Analysis.Dependencies {
				if dependency.Ref == ref {
					seen = true
					want := catalog.OperandDependency
					if executable {
						want = catalog.DefinitionDependency
					}
					if dependency.Purpose != want {
						t.Fatal("nested closure lost dependency purpose", dependency.Purpose, want)
					}
				}
			}
			if !seen {
				t.Fatal("nested closure suppressed ordinary analysis dependency")
			}
			enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
			// Same identity/signature, different semantic declaration. The
			// supported SQL body and its ABS kernel remain unchanged.
			e.Mu.Lock()
			current := e.Catalog.Current().MustProcedure(ref.Address)
			revised := *current
			revised.ObjectRevision = current.Revision() + 1
			if revised.Volatility == expr.Stable {
				revised.Volatility = expr.Volatile
			} else {
				revised.Volatility = expr.Stable
			}
			state := catalog.CloneState(e.Catalog.State)
			state.SetDescriptor(ref.Address, &revised)
			e.Catalog.PublishLocked(state)
			e.Mu.Unlock()
			if preparedRebindValid(t, e, s, d.Analysis) {
				t.Fatal("semantic operand revision disappeared from ordinary analysis")
			}
			preparedFoldExecute(t, e, s, ast, &d)
			if d.Analysis == before.Analysis {
				t.Fatal("ordinary analysis did not refresh")
			}
			refreshed := false
			for _, dependency := range d.Analysis.Dependencies {
				if dependency.Ref == ref {
					refreshed = true
					if dependency.Revision != revised.Revision() {
						t.Fatal("ordinary analysis retained the stale operand revision")
					}
				}
			}
			if !refreshed {
				t.Fatal("ordinary reanalysis suppressed the revised operand")
			}
			if executable {
				if d.Folds.Current().Generation == before.Accepted.Generation || &d.Folds.Current().Entries[0] == &before.Accepted.Entries[0] {
					t.Fatal("actual executable use failed to retire fold generation/backing")
				}
				preparedFoldEntry(t, d, sqltype.Text, "(0.1,1)")
			} else {
				if d.Folds.Current().Generation != before.Accepted.Generation || &d.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
					t.Fatal("identifier-only revision retired unrelated root fold")
				}
				preparedFoldEntry(t, d, sqltype.Text, "(0.10000000000000002,1)")
			}
			preparedFoldEntry(t, before, sqltype.Text, "(0.10000000000000002,1)")
		})
	}
}

func TestPreparedFoldStoredRegclassPurpose(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE fold_regclass_target(v integer)")
	enginetest.SessionSQL(t, s, "CREATE VIEW fold_regclass_operand AS SELECT 'fold_regclass_target'::regclass AS r")
	target := primaryKeyUnitRelation(t, e, "fold_regclass_target")
	ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), target.Address())
	seen := false
	for _, dependency := range stored.ViewOf(primaryKeyUnitRelation(t, e, "fold_regclass_operand")).Dependencies() {
		if dependency.Ref == ref {
			seen = true
			if dependency.Purpose != catalog.OperandDependency {
				t.Fatal("frozen regclass datum acquired executable source purpose")
			}
		}
	}
	if !seen {
		t.Fatal("frozen regclass datum lost its ordinary dependency")
	}
	for _, query := range []string{
		"SELECT r FROM fold_regclass_operand, fold_regclass_target",
		"SELECT r FROM fold_regclass_target, fold_regclass_operand",
	} {
		_, description := sequencePreparedDescription(t, e, s, query)
		seen = false
		for _, dependency := range description.Analysis.Dependencies {
			if dependency.Ref == ref {
				seen = true
				if dependency.Purpose != catalog.DefinitionDependency {
					t.Fatal("actual relation source lost executable purpose", query)
				}
			}
		}
		if !seen {
			t.Fatal("actual relation source lost its ordinary dependency", query)
		}
	}
}
