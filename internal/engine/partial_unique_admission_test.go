package engine

import (
	"fmt"
	"reflect"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func partialUniqueScope(t *testing.T, f *uniquePublicationFixture) *StatementScope {
	t.Helper()
	op, done := roleOperation(t, f.e, "postgres")
	t.Cleanup(done)
	scope, err := f.e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	return scope
}

func TestPartialIndexUniqueAdmissionPrivatePrefix(t *testing.T) {
	for _, source := range []string{"own", "stage", "prefix"} {
		for _, membership := range []string{"false", "", "true"} {
			for _, candidate := range []string{"false", "", "true"} {
				t.Run(fmt.Sprintf("%s/old=%q/new=%q", source, membership, candidate), func(t *testing.T) {
					f := newPartialUniquePublication(t, true, false, "table")
					scope := partialUniqueScope(t, f)
					old := partialUniqueRow(t, f, 1, "2001-02-05", "3", membership)
					next := partialUniqueRow(t, f, 2, "2001-02-05", "3", candidate)
					var prefix [][]sqltype.Value
					scope.ownWrites = make(map[ObjectAddress][]rowVersion)
					scope.stage.rows = make(map[ObjectAddress][]rowVersion)
					switch source {
					case "own":
						scope.ownWrites[f.ref.Address] = []rowVersion{old}
					case "stage":
						scope.stage.rows[f.ref.Address] = []rowVersion{old}
					case "prefix":
						prefix = [][]sqltype.Value{old.values}
					}
					err := scope.checkUniqueRow(f.ref, &indexWriteCandidate{values: next.values}, nil, indexWriteBatch{values: prefix})
					if membership == "true" && candidate == "true" {
						nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "ui_index"`, Detail: "Key (a, b)=(2001-02-05, 3) already exists.", Schema: "public", Table: "ui_t", Constraint: "ui_index"})
					} else if err != nil {
						t.Fatal("nonmember participates in unique admission", err)
					}
					if !reflect.DeepEqual(old.values, partialUniqueRow(t, f, 1, "2001-02-05", "3", membership).values) {
						t.Fatal("private row mutated")
					}
					f.unchanged(t)
					// The synthetic overlay belongs only to this direct owner witness, not a
					// transaction installation. Remove it before closing the real scope.
					delete(scope.ownWrites, f.ref.Address)
					delete(scope.stage.rows, f.ref.Address)
				})
			}
		}
	}
}

func TestPartialIndexUniqueAdmissionCommittedAndClaims(t *testing.T) {
	for _, active := range []string{"false", "", "true"} {
		t.Run(fmt.Sprintf("active=%q", active), func(t *testing.T) {
			f := newPartialUniquePublication(t, true, false, "table")
			scope := partialUniqueScope(t, f)
			row := partialUniqueRow(t, f, 1, "2001-02-03", "1", active)
			err := scope.checkUniqueRow(f.ref, &indexWriteCandidate{values: row.values}, nil, indexWriteBatch{})
			if active == "true" {
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "ui_index"`, Detail: "Key (a, b)=(2001-02-03, 1) already exists.", Schema: "public", Table: "ui_t", Constraint: "ui_index"})
			} else {
				if err != nil {
					t.Fatal(err)
				}
				uniqueKeyBucketsEmpty(t, f.e)
			}
			f.unchanged(t)
		})
	}
	// Old-key claiming has a separate consumer from admission. Excluded deletes
	// and leaving-row predecessors must not reserve an unrelated member's key.
	for _, active := range []string{"false", ""} {
		t.Run("claim/"+active, func(t *testing.T) {
			f := newPartialUniquePublication(t, true, false, "table")
			scope := partialUniqueScope(t, f)
			row := partialUniqueRow(t, f, 1, "2001-02-03", "1", active)
			if err := scope.claimUniqueRow(f.ref, row); err != nil {
				t.Fatal(err)
			}
			uniqueKeyBucketsEmpty(t, f.e)
		})
	}
}

func TestPartialIndexUniqueAdmissionStrongSlots(t *testing.T) {
	for _, ordinary := range []bool{false, true} {
		t.Run(fmt.Sprint(ordinary), func(t *testing.T) {
			f := newPartialUniquePublication(t, true, false, "table")
			if ordinary {
				peer, done := roleOperation(t, f.e, "postgres")
				defer done()
				roleExecute(t, f.e, peer, "CREATE UNIQUE INDEX ui_ordinary ON ui_t(p)")
			}
			scope := partialUniqueScope(t, f)
			binding, err := scope.bindUniqueWrite(f.ref)
			if err != nil {
				t.Fatal(err)
			}
			want := []int(nil)
			if ordinary {
				want = []int{2}
			}
			if !reflect.DeepEqual(binding.keySlots, want) {
				t.Fatal("partial keys became strong row-lock slots", binding.keySlots, want)
			}
			old := partialUniqueRow(t, f, 1, "2001-02-05", "3", "true")
			next := partialUniqueRow(t, f, 2, "2001-02-06", "3", "false")
			next.values[2] = old.values[2]
			changed, err := scope.uniqueKeyChanged(f.ref, old.values, next.values)
			if err != nil || changed {
				t.Fatal("partial key/predicate update stamped strong history", changed, err)
			}
			if ordinary {
				next = partialUniqueRow(t, f, 2, "2001-02-06", "4", "false")
				changed, err = scope.uniqueKeyChanged(f.ref, old.values, next.values)
				if err != nil || !changed {
					t.Fatal("ordinary key control lost strength", changed, err)
				}
			}
		})
	}
}

func TestPartialIndexUniqueAdmissionNullPredicateError(t *testing.T) {
	for _, claim := range []bool{false, true} {
		t.Run(fmt.Sprint(claim), func(t *testing.T) {
			f := newPartialUniquePublication(t, false, false, "table")
			peer, done := roleOperation(t, f.e, "postgres")
			defer done()
			roleExecute(t, f.e, peer, "CREATE UNIQUE INDEX ui_error ON ui_t(a) WHERE active AND 10/b>0")
			scope := partialUniqueScope(t, f)
			row := partialUniqueRow(t, f, 1, "", "0", "true")
			var err error
			if claim {
				err = scope.claimUniqueRow(f.ref, row)
			} else {
				err = scope.checkUniqueRow(f.ref, &indexWriteCandidate{values: row.values}, nil, indexWriteBatch{})
			}
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
			uniqueKeyBucketsEmpty(t, f.e)
		})
	}
}
