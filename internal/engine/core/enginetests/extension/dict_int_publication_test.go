package extension_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
)

func TestDictIntPublication(t *testing.T) {
	for _, fence := range []string{"init", "lexize", "template", "original", "namespace", "cancel"} {
		t.Run(fence, func(t *testing.T) {
			e, op, ext := dictIntUnitInstalled(t)
			original := e.CatalogRead().MustTSDictionary(ext.Members[3])
			stmt := enginetest.Statement(t, "ALTER TEXT SEARCH DICTIONARY intdict (maxlen=3,absval=true)")
			stage, preparation := dictionaryUnitPrepare(t, e, op, stmt)
			defer e.DiscardCatalogStage(stage)
			compiled := preparation.Runtime
			if compiled == original.Runtime || !compiled.(*functions.IntDictionary).Configured(3, false, true) || !preparation.Current(e.CatalogRead()) {
				t.Fatal("detached initializer did not own an independent immutable candidate")
			}
			switch fence {
			case "cancel":
				ctx, cancel := context.WithCancel(op.Ctx)
				cancel()
				op.Ctx = ctx
			case "original":
				enginetest.RoleExecute(t, e, op, "ALTER TEXT SEARCH DICTIONARY intdict (maxlen=4)")
			default:
				view := e.CatalogRead()
				other := e.BeginCatalogStage(view)
				defer e.DiscardCatalogStage(other)
				var candidate catalog.Descriptor
				switch fence {
				case "init", "lexize":
					address := preparation.Template.Init
					if fence == "lexize" {
						address = preparation.Template.Lexize
					}
					p := *view.MustProcedure(address)
					p.ObjectRevision = p.Revision() + 1
					candidate = &p
				case "template":
					template := *preparation.Template
					template.ObjectRevision = template.Revision() + 1
					candidate = &template
				case "namespace":
					namespace := *view.MustNamespace(preparation.Namespace.Address())
					namespace.ObjectRevision = namespace.Revision() + 1
					candidate = &namespace
				}
				if err := other.Replace(candidate); err != nil {
					t.Fatal(err)
				}
				enginetest.PublishOperationStage(t, e, op, other)
			}
			incumbent := e.CatalogRead()
			if fence != "cancel" && preparation.Current(incumbent) {
				t.Fatal("captured authority remained current after genuine replacement", fence)
			}
			e.Mu.Lock()
			completed, err := schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &preparation)
			e.Mu.Unlock()
			if completed != nil || err == nil || e.CatalogRead().State() != incumbent.State() || preparation.Runtime != compiled {
				t.Fatal("rejected completion published or replaced its retained candidate", completed, err)
			}
			if fence == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary cancellation boundary", err)
				}
			} else if !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("ordinary captured-predecessor boundary", err)
			}
			if original.Revision() == 0 || original.Options != nil || !original.Runtime.(*functions.IntDictionary).Configured(6, false, false) {
				t.Fatal("failed candidate mutated the retained source descriptor")
			}
		})
	}
	// Compiler failure after an earlier valid option must not publish even a
	// prefix, change the descriptor generation, or overwrite retained backing.
	e, op, ext := dictIntUnitInstalled(t)
	enginetest.RoleExecute(t, e, op, "ALTER TEXT SEARCH DICTIONARY intdict (maxlen=3,absval=true)")
	view := e.CatalogRead()
	original := view.MustTSDictionary(ext.Members[3])
	options := slices.Clone(original.Options)
	for _, sql := range []string{"ALTER TEXT SEARCH DICTIONARY intdict (maxlen=2,unknown=1)", "CREATE TEXT SEARCH DICTIONARY dict_int_rejected (TEMPLATE=intdict_template,maxlen=2,unknown=1)"} {
		stmt := enginetest.Statement(t, sql)
		stage, err := schemacmds.StageDictionaryDetached(e, op, stmt)
		if stage != nil {
			e.DiscardCatalogStage(stage)
			t.Fatal("invalid options produced a completed candidate")
		}
		enginetest.State(t, err, "22023")
		current := e.CatalogRead()
		if current.State() != view.State() || current.MustTSDictionary(original.Address()) != original || !slices.Equal(original.Options, options) || !original.Runtime.(*functions.IntDictionary).Configured(3, false, true) || &current.MustTSDictionary(original.Address()).Options[0] != &original.Options[0] {
			t.Fatal("failed compiler changed catalog generation, runtime or option backing")
		}
	}
}
