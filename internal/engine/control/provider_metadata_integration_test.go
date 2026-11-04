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
		"CREATE ROLE replay_constructor_owner",
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
		case p.GinSupport != 0:
			family = "gin"
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
	if len(selected) != 16 {
		t.Fatalf("expected 5 constructor, 3 handler and 8 other families, got %d", len(selected))
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
		parallel, rows := p.Parallel, p.Rows
		alter := "ALTER FUNCTION " + identity + " PARALLEL RESTRICTED COST 9.25"
		if p.ReturnsSet() {
			alter += " ROWS 23"
		}
		enginetest.RoleExecute(t, e, op, alter)
		if p.Parallel != parallel || p.Rows != rows {
			t.Fatalf("%s original descriptor changed during COW publication", family)
		}
		if p.RangeConstructor != nil {
			enginetest.RoleExecute(t, e, op, "ALTER FUNCTION "+identity+" OWNER TO replay_constructor_owner")
		}
	}
	enginetest.RoleExecute(t, e, op, "CREATE VIEW replay_span_consumer AS SELECT public.replay_span(1,2) AS span")
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
		owned := strings.Index(text, `OWNER TO "replay_constructor_owner"`)
		consumer := strings.Index(text, `CREATE VIEW "public"."replay_span_consumer"`)
		finalized := strings.Index(text, `DROP SCHEMA "__stuntgres_range_work_`)
		if owned < 0 || finalized <= owned || consumer <= finalized {
			t.Fatalf("constructor current owner/final identity must finish before consumers: %s", text)
		}
		target := engine.New()
		targetOp, targetDone := enginetest.RoleOperation(t, target, "postgres")
		enginetest.RoleExecute(t, target, targetOp, "CREATE ROLE replay_constructor_owner")
		targetDone()
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
			if found.RangeConstructor != nil {
				owner, ok := target.Roles().Lookup("replay_constructor_owner")
				if !ok || found.Owner != owner.Ref {
					t.Fatal("generated constructor current owner lost", family)
				}
			}
		}
		before := target.CatalogRead().State()
		if err := engine.Restore(context.Background(), target, strings.NewReader(text+"ALTER FUNCTION pg_catalog.abs(integer) COST -1;")); err == nil || target.CatalogRead().State() != before {
			t.Fatal("failed metadata replay published a prefix", err)
		}
		e = target
	}
}

// Installed providers bypass collectProvider. Exercise their current estimates
// and owner through real installation, lawful membership edits and native replay.
func TestInstallationReplayActualSurvivorMetadata(t *testing.T) {
	for _, membership := range []string{"attached", "detached", "foreign", "absent"} {
		t.Run(membership, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			for _, q := range []string{
				`CREATE ROLE replay_recipient`,
				`CREATE SCHEMA replay_final`,
				`CREATE EXTENSION pgrowlocks`,
				`ALTER FUNCTION public.pgrowlocks(text) PARALLEL RESTRICTED COST 7.25 ROWS 13`,
			} {
				enginetest.RoleExecute(t, e, op, q)
			}
			if membership != "attached" {
				enginetest.RoleExecute(t, e, op, `ALTER EXTENSION pgrowlocks DROP FUNCTION public.pgrowlocks(text)`)
			}
			if membership == "foreign" {
				enginetest.RoleExecute(t, e, op, `CREATE EXTENSION "uuid-ossp"`)
				enginetest.RoleExecute(t, e, op, `ALTER EXTENSION "uuid-ossp" ADD FUNCTION public.pgrowlocks(text)`)
			}
			if membership == "absent" {
				enginetest.RoleExecute(t, e, op, `DROP EXTENSION pgrowlocks`)
			}
			for _, q := range []string{
				`ALTER FUNCTION public.pgrowlocks(text) RENAME TO retained_locks`,
				`ALTER FUNCTION public.retained_locks(text) SET SCHEMA replay_final`,
				`ALTER FUNCTION replay_final.retained_locks(text) OWNER TO replay_recipient`,
			} {
				enginetest.RoleExecute(t, e, op, q)
			}
			done()
			for round := range 2 {
				var out bytes.Buffer
				if err := engine.Dump(context.Background(), e, &out); err != nil {
					t.Fatal(err)
				}
				target := engine.New()
				targetOp, targetDone := enginetest.RoleOperation(t, target, "postgres")
				enginetest.RoleExecute(t, target, targetOp, "CREATE ROLE replay_recipient")
				targetDone()
				if err := engine.Restore(context.Background(), target, bytes.NewReader(out.Bytes())); err != nil {
					t.Fatal(err)
				}
				view := target.CatalogRead()
				ns := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "replay_final"})
				address := view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ns, Name: "retained_locks", Signature: "25"})
				p, ok := view.Procedure(address)
				if !ok || p.Parallel != 'r' || p.Cost != 7.25 || p.Rows != 13 || !p.ReturnsSet() || !p.Origin.Valid(address) {
					t.Fatalf("round %d lost genuine current metadata: %+v", round, p)
				}
				owner, ok := target.Roles().Lookup("replay_recipient")
				if !ok || p.Owner != owner.Ref {
					t.Fatal("current procedure owner lost")
				}
				wantExtension := ""
				if membership == "attached" {
					wantExtension = "pgrowlocks"
				} else if membership == "foreign" {
					wantExtension = "uuid-ossp"
				}
				if wantExtension == "" {
					if p.Extension != (ids.ObjectAddress{}) {
						t.Fatal("detached survivor acquired membership")
					}
				} else if ext, ok := view.Extension(p.Extension); !ok || ext.Name() != wantExtension {
					t.Fatal("survivor current membership lost")
				}
				origin := view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pgrowlocks"})
				if (origin == (ids.ObjectAddress{})) != (membership == "absent") {
					t.Fatal("replay changed original extension presence")
				}
				e = target
			}
		})
	}
}

// Aggregate estimates are recipe state, not ALTER AGGREGATE attributes. Its
// genuine support procedures retain independent current estimates and owners.
func TestInstallationReplayIntaggMetadata(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	for _, q := range []string{
		`CREATE ROLE replay_state_owner`,
		`CREATE ROLE replay_final_owner`,
		`CREATE SCHEMA replay_helpers`,
		`CREATE SCHEMA replay_aggregate`,
		`CREATE EXTENSION intagg`,
		`ALTER FUNCTION public.int_agg_state(internal,integer) PARALLEL RESTRICTED COST 7.25`,
		`ALTER FUNCTION public.int_agg_final_array(internal) PARALLEL UNSAFE COST 9.5`,
		`ALTER FUNCTION public.int_array_enum(integer[]) PARALLEL RESTRICTED COST 11 ROWS 13`,
		`ALTER FUNCTION public.int_agg_state(internal,integer) SET SCHEMA replay_helpers`,
		`ALTER FUNCTION public.int_agg_final_array(internal) SET SCHEMA replay_helpers`,
		`ALTER FUNCTION replay_helpers.int_agg_state(internal,integer) OWNER TO replay_state_owner`,
		`ALTER FUNCTION replay_helpers.int_agg_final_array(internal) OWNER TO replay_final_owner`,
		`ALTER AGGREGATE public.int_array_aggregate(integer) SET SCHEMA replay_aggregate`,
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	done()
	for round := range 2 {
		var out bytes.Buffer
		if err := engine.Dump(context.Background(), e, &out); err != nil {
			t.Fatal(err)
		}
		for _, statement := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(statement, "ALTER AGGREGATE ") && (strings.Contains(statement, " COST ") || strings.Contains(statement, " PARALLEL ") || strings.Contains(statement, " ROWS ")) {
				t.Fatal("aggregate acquired unsupported estimate syntax", statement)
			}
		}
		target := engine.New()
		targetOp, targetDone := enginetest.RoleOperation(t, target, "postgres")
		enginetest.RoleExecute(t, target, targetOp, `CREATE ROLE replay_state_owner`)
		enginetest.RoleExecute(t, target, targetOp, `CREATE ROLE replay_final_owner`)
		targetDone()
		if err := engine.Restore(context.Background(), target, bytes.NewReader(out.Bytes())); err != nil {
			t.Fatal(err)
		}
		view := target.CatalogRead()
		ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intagg"}))
		if len(ext.Members) != 4 {
			t.Fatal("canonical intagg declaration inventory", ext.Members)
		}
		for _, want := range []struct {
			name, schema, owner string
			parallel            byte
			cost, rows          float32
		}{
			{"int_agg_state", "replay_helpers", "replay_state_owner", 'r', 7.25, 0},
			{"int_agg_final_array", "replay_helpers", "replay_final_owner", 'u', 9.5, 0},
			{"int_array_aggregate", "replay_aggregate", "postgres", 0, 1, 0},
			{"int_array_enum", "public", "postgres", 'r', 11, 13},
		} {
			var found *catalog.Procedure
			for _, member := range ext.Members {
				p := view.MustProcedure(member)
				if p.Name() == want.name {
					found = p
					break
				}
			}
			owner, ok := target.Roles().Lookup(want.owner)
			if !ok || found == nil || found.Owner != owner.Ref || view.MustNamespace(found.Namespace()).Name() != want.schema || found.Parallel != want.parallel || found.Cost != want.cost || found.Rows != want.rows || !found.Origin.Valid(found.Address()) {
				t.Fatalf("round %d lost independent intagg declaration %s: %+v", round, want.name, found)
			}
			if found.Aggregate != nil {
				for _, helper := range found.Aggregate.Helpers {
					current := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID})
					if current.Origin.Installation != found.Origin.Installation || view.MustNamespace(current.Namespace()).Name() != "replay_helpers" {
						t.Fatal("aggregate lost genuine independently placed support", helper)
					}
				}
			}
		}
		e = target
	}
}

func TestInstallationReplayIntaggUnsupportedEstimates(t *testing.T) {
	for _, field := range []string{"parallel", "cost", "rows"} {
		t.Run(field, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			enginetest.RoleExecute(t, e, op, `CREATE EXTENSION intagg`)
			done()
			view := e.CatalogRead()
			ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "intagg"}))
			original := view.MustProcedure(ext.Members[2])
			bad := *original
			switch field {
			case "parallel":
				bad.Parallel = 'r'
			case "cost":
				bad.Cost = 9
			case "rows":
				bad.Rows = 23
			}
			// Corrupt only this genuine declaration's unsupported override.
			// No SQL route is invented to create the impossible header.
			e.Catalog.State.SetDescriptor(original.Address(), &bad)
			var out bytes.Buffer
			if err := engine.Dump(context.Background(), e, &out); err == nil || out.Len() != 0 {
				t.Fatal("unsupported aggregate estimate was dropped or emitted", field, err, out.String())
			}
		})
	}
}
