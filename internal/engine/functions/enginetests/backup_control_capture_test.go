package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Frozen queries render their captured syntax, not BackupControl.Render. Keep
// lexical operands separate from the full selected formal routine identity.
func TestBackupControlStoredNamedReplay(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		call   string
		kind   functions.BackupControlKind
		slots  [2]uint8
		labels []string
	}{
		{"reordered-null-label", "pg_backup_start(fast=>false,label=>NULL::text)", functions.BackupStart, [2]uint8{1, 0}, []string{"fast", "label"}},
		{"reordered-null-fast", "pg_backup_start(fast=>NULL::boolean,label=>'retained label')", functions.BackupStart, [2]uint8{1, 0}, []string{"fast", "label"}},
		{"default-fast", "pg_backup_start(label=>'default fast')", functions.BackupStart, [2]uint8{}, []string{"label"}},
		{"positional-then-named", "pg_backup_start('named fast',fast=>true)", functions.BackupStart, [2]uint8{0, 1}, []string{"", "fast"}},
		{"stop-named-null", "pg_backup_stop(wait_for_archive=>NULL::boolean)", functions.BackupStop, [2]uint8{}, []string{"wait_for_archive"}},
		{"stop-named-false", "pg_backup_stop(wait_for_archive=>false)", functions.BackupStop, [2]uint8{}, []string{"wait_for_archive"}},
		{"stop-default", "pg_backup_stop()", functions.BackupStop, [2]uint8{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, destination := engine.New(), engine.New()
			b := enginetest.ExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
			raw := enginetest.Statement(t, "SELECT "+tc.call).(*parser.Select).Targets[0]
			bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			n := bound.(functions.BackupControl)
			before := n
			if n.Kind != tc.kind || n.Slots != tc.slots || int(n.Arity) != len(tc.labels) {
				t.Fatal("source formal destinations", n)
			}
			procedure, err := exec.ExpressionRoutineDescriptor(n)
			if err != nil {
				t.Fatal(err)
			}
			wantOID, wantSignature := ids.OID(2172), "25,16"
			if tc.kind == functions.BackupStop {
				wantOID, wantSignature = 2739, "16"
			}
			if procedure == nil || procedure.Address().ObjectID != wantOID || string(procedure.Signature) != wantSignature {
				t.Fatal("full selected formal identity", procedure)
			}

			path := analyze.ViewLiteralPath{Query: b.FrameID(), Clause: analyze.ViewTargetLiteral}
			capturedNodes := 0
			if err := b.CaptureViewLiteralTree(raw, n, path, &capturedNodes); err != nil {
				t.Fatal(err)
			}
			ref := source.CatalogRead().Domain().Ref(source.CatalogRead().Epoch(), procedure.Address())
			if len(b.Capture.RoutineRefs) != 1 || b.Capture.RoutineRefs[path] != ref {
				t.Fatal("selected occurrence identity", b.Capture.RoutineRefs)
			}
			if tc.name == "reordered-null-label" {
				booleanPath, textPath := path, path
				booleanPath.Node, textPath.Node = 1, 3
				boolean, text := b.Capture.Literals[booleanPath], b.Capture.Literals[textPath]
				fast, booleanError := boolean.Value.Boolean()
				if capturedNodes != 4 || len(b.Capture.Literals) != 2 || booleanError != nil || fast || text.Value.Type() == nil || text.Value.Type().ID() != sqltype.Text || !text.Value.IsNull() {
					t.Fatal("lexical bool/text literal ownership", b.Capture.Literals, capturedNodes)
				}
			}
			normalized := b.NormalizeStoredExpression(raw).(parser.FunctionExpr)
			if !normalized.StoredProviderRoutine || normalized.StoredProviderSymbol != n.Kind.Name() {
				t.Fatal("missing normalized provider recipe", normalized)
			}
			definition := stored.NewView(stored.ViewParts{Literals: b.Capture.Literals, CastUsages: b.Capture.CastUsages, RoutineRefs: b.Capture.RoutineRefs, Dependencies: b.Dependencies})
			for _, deparse := range []bool{false, true} {
				renderer := stored.QueryRenderer{View: definition, Frame: b.FrameID(), Presentation: stored.Presentation{Deparse: deparse, Pretty: true}}
				renderedNodes := 0
				text, err := renderer.ExpressionSQLAt(ctx, source.CatalogRead(), normalized, path, &renderedNodes, stored.DeparseParent{})
				if err != nil || renderedNodes != capturedNodes {
					t.Fatal("frozen query operand traversal", text, renderedNodes, capturedNodes, err)
				}
				replayRaw := enginetest.Statement(t, "SELECT "+text).(*parser.Select).Targets[0].(parser.FunctionExpr)
				for i, label := range tc.labels {
					got := ""
					if i < len(replayRaw.ArgNames) {
						got = replayRaw.ArgNames[i].Text
					}
					if got != label {
						t.Fatal("frozen query lost named destination", text, i, got, label)
					}
				}
				target := enginetest.ExpressionBinder(t, ctx, destination, sqltype.InputEnvironment{}, nil)
				rebound, err := target.Bind(replayRaw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
				if err != nil {
					t.Fatal("frozen query replay", text, err)
				}
				next := rebound.(functions.BackupControl)
				selected, err := exec.ExpressionRoutineDescriptor(next)
				if err != nil || selected == nil || selected.Address() != procedure.Address() || string(selected.Signature) != wantSignature || selected != destination.CatalogRead().MustProcedure(procedure.Address()) || selected == procedure || selected.Owner == procedure.Owner || next.Kind != n.Kind || next.Arity != n.Arity || next.Slots != n.Slots {
					t.Fatal("destination formal identity/defaults", text, selected, err)
				}
				// Evaluate only pure supplied literals/casts, never the backup call.
				// Opposite types and values distinguish lexical child association.
				for i := range int(n.Arity) {
					original, err := n.Args[i].Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: source.CatalogRead()}})
					if err != nil {
						t.Fatal(err)
					}
					replayed, err := next.Args[i].Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: destination.CatalogRead()}})
					if err != nil || original.Type().ID() != replayed.Type().ID() || original.IsNull() != replayed.IsNull() {
						t.Fatal("replayed lexical operand", text, i, original, replayed, err)
					}
					if !original.IsNull() {
						want, err := original.Type().Output(ctx, original)
						if err != nil {
							t.Fatal(err)
						}
						got, err := replayed.Type().Output(ctx, replayed)
						if err != nil || got != want {
							t.Fatal("replayed literal value", text, i, got, want, err)
						}
					}
				}
			}
			if !reflect.DeepEqual(n, before) {
				t.Fatal("capture/replay mutated original recipe")
			}
		})
	}
}
