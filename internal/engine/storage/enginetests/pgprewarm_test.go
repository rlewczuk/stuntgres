package enginetests

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/sqlstate"
)

// Geometry and incarnation-owned auxiliary facts share the real checked storage
// sizing/publication/transport boundaries; there is no test-only block model.
func TestPgPrewarmForkGeometry(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 { integer = typ }
	}
	if integer == nil { t.Fatal("missing builtin integer") }
	value, err := sqltype.Integer(integer, 1)
	if err != nil { t.Fatal(err) }
	row := []sqltype.Value{value}
	weight, err := storage.RowAllocation(t.Context(), row)
	if err != nil || weight != 40 { t.Fatal("real typed row allocation", weight, err) }
	weight, err = storage.AddSize(weight, 4)
	if err != nil || weight != 44 { t.Fatal("checked line pointer", weight, err) }
	for _, tc := range []struct{ rows, payload, blocks int64 }{{0, 0, 0}, {1, 44, 1}, {185, 8140, 1}, {186, 8184, 2}, {1000, 44000, 6}} {
		var payload int64
		for i := int64(0); i < tc.rows; i++ {
			payload, err = storage.AddSize(payload, weight)
			if err != nil { t.Fatal(err) }
		}
		if payload != tc.payload { t.Fatal("real row accumulation", tc, payload) }
		layout, err := storage.FreespacePayload(t.Context(), payload)
		if err != nil || layout.Blocks != tc.blocks { t.Fatal("compact main extent", tc, layout, err) }
		if row[0] != value { t.Fatal("sizing mutated borrowed typed row") }
	}
	// Inclusive range arithmetic must not force payload overflow, and sizing
	// cancellation must return no usable layout.
	if _, err := storage.AddSize(math.MaxInt64-3, 4); err == nil { t.Fatal("unchecked payload overflow") }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	layout, err := storage.FreespacePayload(ctx, 8184)
	if !errors.Is(err, context.Canceled) || layout != (storage.FreespaceLayout{}) { t.Fatal("canceled sizing published layout", layout, err) }
	t.Run("fork_presence_and_immutable_copy", func(t *testing.T) {
		empty, err := storage.VacuumForks(t.Context(), storage.AuxiliaryForks{}, nil)
		if err != nil || empty != (storage.AuxiliaryForks{}) { t.Fatal("empty vacuum fabricated forks", empty, err) }
		rows := []storage.RowVersion{{Row: 1, Version: 1, Values: row}}
		unfrozen, err := storage.VacuumForks(t.Context(), empty, rows)
		if err != nil || !unfrozen.FSM || unfrozen.FSMBlocks != 1 || unfrozen.VM { t.Fatal("unfrozen maintenance facts", unfrozen, err) }
		rows[0].Frozen = true
		frozen, err := storage.VacuumForks(t.Context(), unfrozen, rows)
		if err != nil || frozen != (storage.AuxiliaryForks{FSM: true, VM: true, FSMBlocks: 1, VMBlocks: 1}) { t.Fatal("actual frozen maintenance facts", frozen, err) }
		zero, err := storage.VacuumForks(t.Context(), frozen, nil)
		if err != nil || zero != (storage.AuxiliaryForks{FSM: true, VM: true}) { t.Fatal("created-empty lost presence", zero, err) }
		if n, exists := zero.Fork(2); !exists || n != 0 { t.Fatal("existing zero conflated with absence") }
		if _, exists := empty.Fork(2); exists { t.Fatal("never-created VM became present") }
		original := storage.Relation{Versions: rows, Auxiliary: frozen}
		transport := original
		shape := storage.RowShape{Original: &original, Replacement: &transport, Root: rows, ObservedLength: 1, Generation: original.Generation, ObservedAuxiliary: frozen, PreservesVisibility: true}
		if !shape.MatchesWithEnds(&original) { t.Fatal("real metadata copy rejected") }
		original.Auxiliary.VMBlocks = 0
		if shape.MatchesWithEnds(&original) || transport.Auxiliary != frozen { t.Fatal("fork publication mutated immutable predecessor or escaped fence") }
		canceled, err := storage.VacuumForks(ctx, frozen, rows)
		if !errors.Is(err, context.Canceled) || canceled != (storage.AuxiliaryForks{}) { t.Fatal("canceled maintenance published fork facts", canceled, err) }
	})
	t.Run("independent_endpoint_validation", func(t *testing.T) {
		for _, tc := range []struct{ request storage.PrewarmRequest; blocks, first, last int64 }{
			{storage.PrewarmRequest{DefaultFirst: true, DefaultLast: true}, 0, 0, -1},
			{storage.PrewarmRequest{DefaultFirst: true, DefaultLast: true}, 6, 0, 5},
			{storage.PrewarmRequest{First: 2, Last: 1}, 6, 2, 1},
			{storage.PrewarmRequest{First: math.MaxInt64-1, Last: math.MaxInt64-1}, math.MaxInt64, math.MaxInt64-1, math.MaxInt64-1},
		} {
			first, last, err := tc.request.Bounds(tc.blocks)
			if err != nil || first != tc.first || last != tc.last { t.Fatal(tc, first, last, err) }
		}
		for _, tc := range []struct{ request storage.PrewarmRequest; message string }{
			{storage.PrewarmRequest{First: -1, Last: -1}, "starting block number must be between 0 and 5"},
			{storage.PrewarmRequest{First: 6, Last: 0}, "starting block number must be between 0 and 5"},
			{storage.PrewarmRequest{DefaultFirst: true, Last: math.MaxInt64}, "ending block number must be between 0 and 5"},
		} {
			_, _, err := tc.request.Bounds(6)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: tc.message}) { t.Fatal("endpoint diagnostic", err) }
		}
	})
	t.Run("fork_snapshot_codec", func(t *testing.T) {
		record := storage.AuxiliaryForkSnapshot{Schema: "public", Relation: "forks", Forks: storage.AuxiliaryForks{FSM: true, VM: true, FSMBlocks: 1}}
		text, err := storage.EncodeAuxiliaryForkSnapshot(record)
		if err != nil { t.Fatal(err) }
		decoded, recognized, err := storage.DecodeAuxiliaryForkSnapshot(strings.TrimPrefix(text, "-- "))
		if err != nil || !recognized || decoded != record { t.Fatal("scalar provenance transport", decoded, recognized, err) }
		for _, bad := range []storage.AuxiliaryForkSnapshot{{}, {Schema: "public", Relation: "forks", Forks: storage.AuxiliaryForks{VMBlocks: 1}}, {Schema: "public", Relation: "forks", Forks: storage.AuxiliaryForks{VM: true, VMBlocks: -1}}} {
			if _, err := storage.EncodeAuxiliaryForkSnapshot(bad); err == nil { t.Fatal("invalid fork facts serialized", bad) }
		}
		for _, text := range []string{"stuntgres-auxiliary-forks-v99 e30=", storage.AuxiliaryForkSnapshotMarker+" !!!"} {
			if _, recognized, err := storage.DecodeAuxiliaryForkSnapshot(text); !recognized || err == nil { t.Fatal("invalid fork metadata accepted", text, recognized, err) }
		}
	})
}
