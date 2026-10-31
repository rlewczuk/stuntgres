package enginetests_test

import (
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
)

// Independently transcribed PG18.6 atomic bodies, including analyzed RTE aliases
// and outer-formal qualification. No native renderer supplies these expectations.
const attrsDeclarationThreeGolden = `BEGIN ATOMIC
 SELECT heap_page_items.lp,
     heap_page_items.lp_off,
     heap_page_items.lp_flags,
     heap_page_items.lp_len,
     heap_page_items.t_xmin,
     heap_page_items.t_xmax,
     heap_page_items.t_field3,
     heap_page_items.t_ctid,
     heap_page_items.t_infomask2,
     heap_page_items.t_infomask,
     heap_page_items.t_hoff,
     heap_page_items.t_bits,
     heap_page_items.t_oid,
     heap_part.tuple_data_split((heap_page_item_attrs.rel_oid)::oid, heap_page_items.t_data, heap_page_items.t_infomask, heap_page_items.t_infomask2, heap_page_items.t_bits, heap_page_item_attrs.do_detoast) AS t_attrs
    FROM heap_part.heap_page_items(heap_page_item_attrs.page) heap_page_items(lp, lp_off, lp_flags, lp_len, t_xmin, t_xmax, t_field3, t_ctid, t_infomask2, t_infomask, t_hoff, t_bits, t_oid, t_data);
END
`
const attrsDeclarationTwoGolden = `BEGIN ATOMIC
 SELECT heap_page_item_attrs.lp,
     heap_page_item_attrs.lp_off,
     heap_page_item_attrs.lp_flags,
     heap_page_item_attrs.lp_len,
     heap_page_item_attrs.t_xmin,
     heap_page_item_attrs.t_xmax,
     heap_page_item_attrs.t_field3,
     heap_page_item_attrs.t_ctid,
     heap_page_item_attrs.t_infomask2,
     heap_page_item_attrs.t_infomask,
     heap_page_item_attrs.t_hoff,
     heap_page_item_attrs.t_bits,
     heap_page_item_attrs.t_oid,
     heap_page_item_attrs.t_attrs
    FROM heap_part.heap_page_item_attrs(heap_page_item_attrs.page, heap_page_item_attrs.rel_oid, false) heap_page_item_attrs(lp, lp_off, lp_flags, lp_len, t_xmin, t_xmax, t_field3, t_ctid, t_infomask2, t_infomask, t_hoff, t_bits, t_oid, t_attrs);
END
`

func TestPageinspectHeapAttrsDeclarationProducer(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "CREATE SCHEMA heap_part")
	enginetest.SessionSQL(t, session, "CREATE EXTENSION pageinspect SCHEMA heap_part")
	base := e.CatalogRead()
	installed := base.MustExtension(base.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pageinspect"}))
	for _, slot := range []int{19, 20} {
		name, golden, source := "three", attrsDeclarationThreeGolden, "SELECT lp,lp_off,lp_flags,lp_len,t_xmin,t_xmax,t_field3,t_ctid,t_infomask2,t_infomask,t_hoff,t_bits,t_oid,tuple_data_split(rel_oid::oid,t_data,t_infomask,t_infomask2,t_bits,do_detoast) AS t_attrs FROM heap_page_items(page)"
		helpers := []int{15, 18}
		if slot == 20 {
			name, golden, source = "two", attrsDeclarationTwoGolden, "SELECT * FROM heap_page_item_attrs(page,rel_oid,false)"
			helpers = []int{19}
		}
		t.Run(name, func(t *testing.T) {
			target := base.MustProcedure(installed.Installation.Members[slot])
			assertBody := func(read catalog.Read) {
				t.Helper()
				got, err := stored.RoutineDeclarationSQL(t.Context(), read, read.MustProcedure(target.Address()), declarationRecipePolicy())
				if err != nil || got != golden {
					t.Fatalf("body=%q, %v; want %q", got, err, golden)
				}
			}
			reject := func(read catalog.Read, current *catalog.Procedure) {
				t.Helper()
				if _, err := stored.RoutineDeclarationSQL(t.Context(), read, current, declarationRecipePolicy()); !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatalf("forged declaration accepted: %v", err)
				}
			}
			assertBody(base)
			copied := *target
			reject(base, &copied)
			t.Run("detached", func(t *testing.T) {
				state := catalog.CloneState(base.State())
				header := *installed
				header.Members = slices.Clone(installed.Members)
				for _, memberSlot := range append(slices.Clone(helpers), slot) {
					member := *base.MustProcedure(installed.Installation.Members[memberSlot])
					member.Extension = ids.ObjectAddress{}
					state.SetDescriptor(member.Address(), &member)
					header.Members = slices.DeleteFunc(header.Members, func(a ids.ObjectAddress) bool { return a == member.Address() })
				}
				state.SetDescriptor(header.Address(), &header)
				assertBody(base.WithState(state))
			})
			for _, mode := range []string{"missing_origin", "copied_installation", "wrong_slot", "wrong_provider", "wrong_display", "foreign_body", "part_cross_provider", "part_wrong_slot", "part_wrong_class", "part_wrong_signature"} {
				t.Run("target_"+mode, func(t *testing.T) {
					state := catalog.CloneState(base.State())
					current := *target
					switch mode {
					case "missing_origin":
						current.Origin = nil
					case "copied_installation":
						copied := *current.Origin.Installation
						origin := *current.Origin
						origin.Installation = &copied
						current.Origin = &origin
					case "wrong_slot":
						origin := *current.Origin
						origin.Declaration = 15
						current.Origin = &origin
					case "wrong_provider":
						current.Implementation = extension.StockEarthdistance
					case "wrong_display":
						current.Display.Source = "SELECT 1"
					default:
						recipe, ok := proc.RoutineDeclarationRecipe(current.DeclarationBody)
						if !ok {
							t.Fatal("missing genuine recipe")
						}
						parts := make([]proc.DeclarationPart, recipe.Len())
						for i := range recipe.Len() {
							parts[i] = recipe.At(i)
						}
						for i := range parts {
							if parts[i].Kind != proc.DeclarationExtensionMember {
								continue
							}
							switch mode {
							case "part_cross_provider":
								parts[i].Extension = "cube"
							case "part_wrong_slot":
								parts[i].Slot = 0
							case "part_wrong_class":
								parts[i].Class = uint32(ids.TypeClassID)
							case "part_wrong_signature":
								parts[i].Arguments[0] = 23
							}
							break
						}
						var err error
						current.DeclarationBody, err = proc.NewRoutineDeclarationBody(proc.DeclarationRecipe{Parts: parts, Source: source})
						if err != nil {
							t.Fatal(err)
						}
					}
					state.SetDescriptor(current.Address(), &current)
					read := base.WithState(state)
					reject(read, read.MustProcedure(target.Address()))
				})
			}
			for _, helperSlot := range helpers {
				helper := base.MustProcedure(installed.Installation.Members[helperSlot])
				for _, mode := range []string{"missing", "missing_origin", "copied_installation", "wrong_slot", "wrong_provider", "wrong_signature", "wrong_body", "wrong_display", "missing_reciprocal_member"} {
					t.Run("helper_"+helper.Name()+"_"+mode, func(t *testing.T) {
						state := catalog.CloneState(base.State())
						current := *helper
						switch mode {
						case "missing":
							state.RemoveDescriptor(helper.Address())
						case "missing_origin":
							current.Origin = nil
						case "copied_installation":
							copied := *current.Origin.Installation
							origin := *current.Origin
							origin.Installation = &copied
							current.Origin = &origin
						case "wrong_slot":
							origin := *current.Origin
							origin.Declaration = 17
							current.Origin = &origin
						case "wrong_provider":
							current.Implementation = extension.StockEarthdistance
						case "wrong_signature":
							current.Signature = "17,23"
						case "wrong_body":
							current.DeclarationBody = target.DeclarationBody
						case "wrong_display":
							current.Display.Source = "foreign_helper"
						case "missing_reciprocal_member":
							header := *installed
							header.Members = slices.DeleteFunc(slices.Clone(installed.Members), func(a ids.ObjectAddress) bool { return a == helper.Address() })
							state.SetDescriptor(header.Address(), &header)
						}
						if mode != "missing" {
							state.SetDescriptor(current.Address(), &current)
						}
						read := base.WithState(state)
						reject(read, read.MustProcedure(target.Address()))
					})
				}
			}
		})
	}
}
