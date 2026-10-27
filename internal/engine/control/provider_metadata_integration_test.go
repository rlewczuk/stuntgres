package control_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
)

// Reach collection through actual SQL publication and real provider validators,
// including every generated constructor form and all three bootstrap handlers.
func TestProviderReplayActualFamilies(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	for _, sql := range []string{
		"CREATE TYPE replay_span AS RANGE (subtype=integer, multirange_type_name=replay_many)",
		"CREATE EXTENSION unaccent",
		"CREATE EXTENSION pgrowlocks",
	} {
		enginetest.RoleExecute(t, e, op, sql)
	}
	view := e.CatalogRead()
	selected := make(map[string]*catalog.Procedure)
	for _, descriptor := range view.Descriptors() {
		p, ok := descriptor.(*catalog.Procedure)
		if !ok || p.SQL != nil || p.RoutineKind != catalog.FunctionRoutine {
			continue
		}
		family := ""
		switch {
		case p.RangeConstructor != nil:
			// Select the actual custom declaration whose readiness ordering is
			// asserted below, not an arbitrary seeded range of the same form.
			if p.Name() != "replay_span" && p.Name() != "replay_many" {
				continue
			}
			family = fmt.Sprintf("constructor-%d", p.RangeConstructor.Form)
		case p.PLHandler != catalog.NoPLHandler:
			family = fmt.Sprintf("handler-%d", p.PLHandler)
		case p.AccessMethod != catalog.NoAccessMethod:
			family = "access-method"
		case p.BrinSupport != 0:
			family = "brin"
		case p.Conversion.Name() != "":
			family = "conversion"
		case p.Implementation != nil:
			family = "installed-scalar"
			if p.ReturnsSet() {
				family = "installed-set"
			}
		case p.Builtin != nil && p.Name() == "abs" && p.Signature == "23":
			family = "builtin-scalar"
		case p.Builtin != nil && p.Name() == "generate_series" && p.Signature == "23,23":
			family = "builtin-set"
		}
		if family != "" && selected[family] == nil {
			selected[family] = p
		}
	}
	if len(selected) != 15 {
		t.Fatalf("expected 5 constructor, 3 handler and 7 other families, got %d", len(selected))
	}
	for family, p := range selected {
		ns := view.MustNamespace(p.Namespace())
		var args []string
		for raw := range strings.SplitSeq(string(p.Signature), ",") {
			if raw == "" {
				continue
			}
			var oid uint32
			if _, err := fmt.Sscan(raw, &oid); err != nil {
				t.Fatal(err)
			}
			args = append(args, regobj.TypeSpelling(view, nil, ids.OID(oid)))
		}
		identity := catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(p.Name()) + "(" + strings.Join(args, ", ") + ")"
		alter := "ALTER FUNCTION " + identity + " PARALLEL RESTRICTED COST 9.25"
		if p.ReturnsSet() {
			alter += " ROWS 23"
		}
		enginetest.RoleExecute(t, e, op, alter)
		if p.Parallel != 0 || p.Rows != 0 {
			t.Fatalf("%s original descriptor changed during COW publication", family)
		}
	}
	done()
	for round := range 2 {
		var out bytes.Buffer
		if err := engine.Dump(context.Background(), e, &out); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		if create, alter := strings.Index(text, "CREATE TYPE"), strings.Index(text, "ALTER FUNCTION \"public\".\"replay_span\""); create < 0 || alter < create {
			t.Fatalf("constructor metadata preceded type installation: %s", text)
		}
		target := engine.New()
		if err := engine.Restore(context.Background(), target, bytes.NewReader(out.Bytes())); err != nil {
			t.Fatal(err)
		}
		restored := target.CatalogRead()
		for family, old := range selected {
			ns := view.MustNamespace(old.Namespace())
			var found *catalog.Procedure
			for _, descriptor := range restored.Descriptors() {
				p, ok := descriptor.(*catalog.Procedure)
				if !ok || p.Name() != old.Name() || restored.MustNamespace(p.Namespace()).Name() != ns.Name() {
					continue
				}
				if old.RangeConstructor != nil {
					if p.RangeConstructor == nil || p.RangeConstructor.Form != old.RangeConstructor.Form {
						continue
					}
				} else if p.Signature != old.Signature {
					continue
				}
				found = p
				break
			}
			var wantRows float32
			if old.ReturnsSet() {
				wantRows = 23
			}
			if found == nil || found.Parallel != 'r' || found.Cost != 9.25 || found.ReturnsSet() != old.ReturnsSet() || found.Rows != wantRows {
				t.Fatalf("round %d %s metadata lost: %+v", round, family, found)
			}
			if found.RangeConstructor != nil && found.RangeConstructor.Type == old.RangeConstructor.Type {
				t.Fatal("constructor retained source type generation")
			}
		}
		before := target.CatalogRead().State()
		if err := engine.Restore(context.Background(), target, strings.NewReader(text+"ALTER FUNCTION pg_catalog.abs(integer) COST -1;")); err == nil || target.CatalogRead().State() != before {
			t.Fatal("failed metadata replay published a prefix", err)
		}
		e = target
	}
}
