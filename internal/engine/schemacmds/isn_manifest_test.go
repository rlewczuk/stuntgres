package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestIsnManifestRoles(t *testing.T) {
	e, ext, _ := isnUnitInstalled(t)
	before := e.CatalogRead()
	for _, slots := range [][2]int{{0, 3}, {1, 4}, {2, 5}, {1, 2}, {4, 5}, {0, 6}, {3, 6}, {1, 7}, {2, 8}, {7, 8}, {0, 9}, {3, 9}, {6, 9}, {1, 10}, {7, 10}, {2, 11}, {8, 11}, {10, 11}, {0, 12}, {9, 12}, {13, 14}, {0, 15}, {3, 15}, {6, 15}, {9, 15}, {12, 15}, {1, 16}, {4, 16}, {7, 16}, {10, 16}, {13, 16}, {2, 17}, {5, 17}, {8, 17}, {11, 17}, {14, 17}, {16, 17}, {0, 18}, {3, 18}, {6, 18}, {9, 18}, {12, 18}, {15, 18}, {1, 19}, {4, 19}, {7, 19}, {10, 19}, {13, 19}, {16, 19}, {2, 20}, {5, 20}, {8, 20}, {11, 20}, {14, 20}, {17, 20}, {19, 20}, {0, 21}, {3, 21}, {6, 21}, {9, 21}, {12, 21}, {15, 21}, {18, 21}, {1, 22}, {4, 22}, {7, 22}, {10, 22}, {13, 22}, {16, 22}, {19, 22}, {2, 23}, {5, 23}, {8, 23}, {11, 23}, {14, 23}, {17, 23}, {20, 23}, {22, 23}} {
		candidate := catalog.CloneState(before.State())
		changed := *ext
		changed.Members = append([]ids.ObjectAddress(nil), ext.Members...)
		changed.Members[slots[0]], changed.Members[slots[1]] = changed.Members[slots[1]], changed.Members[slots[0]]
		if err := extension.StockIsn.ValidateExtension(e.Catalog.Read(candidate), &changed); err != nil {
			t.Fatal("mutable membership order became ISN role authority", slots, err)
		}
		forged := *ext.Installation
		forged.Members = append([]ids.ObjectAddress(nil), forged.Members...)
		forged.Members[slots[0]], forged.Members[slots[1]] = forged.Members[slots[1]], forged.Members[slots[0]]
		changed.Installation = &forged
		candidate.SetDescriptor(changed.Address(), &changed)
		if err := extension.StockIsn.ValidateExtension(e.Catalog.Read(candidate), &changed); err == nil {
			t.Fatal("swapped manifest roles accepted", slots)
		}
	}
	for slot := range len(ext.Members) {
		for _, missing := range []bool{false, true} {
			candidate := catalog.CloneState(before.State())
			changed := *ext
			changed.Members = append([]ids.ObjectAddress(nil), ext.Members...)
			if missing {
				changed.Members = append(changed.Members[:slot], changed.Members[slot+1:]...)
			} else {
				changed.Members[slot] = changed.Members[(slot+1)%len(changed.Members)]
			}
			candidate.SetDescriptor(changed.Address(), &changed)
			err := extension.StockIsn.ValidateExtension(e.Catalog.Read(candidate), &changed)
			if missing {
				err = catalog.ValidateExtensionMembership(e.Catalog.Read(candidate), before.Get(ext.Members[slot]))
			}
			if err == nil {
				t.Fatal("missing or duplicate manifest role accepted", slot, missing)
			}
		}
	}
	for _, slot := range []int{0, 3, 6, 9, 12, 15, 18, 21} {
		base := before.MustType(ext.Installation.Members[slot])
		other := before.MustType(ext.Installation.Members[(slot+3)%len(ext.Installation.Members)])
		input := "9780123456780!"
		if slot == 0 {
			input = "1234567890120!"
		}
		if slot == 9 || slot == 12 {
			input = "M123456780!"
		}
		if slot == 15 || slot == 18 {
			input = "9771234567898!"
		}
		if slot == 21 {
			input = "123456789010!"
		}
		value, err := base.SQLType.Input(context.Background(), input, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []sqltype.Value{value, sqltype.Null(base.SQLType)} {
			if _, err := typedef.ReownDomainDatum(source, other.SQLType, nil, map[*sqltype.Descriptor]*sqltype.Descriptor{base.SQLType: other.SQLType}, nil); err == nil {
				t.Fatal("source authorization crossed ISN family")
			}
		}
		candidate := catalog.CloneState(before.State())
		array := *candidate.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Array)})
		array.SQLType = sqltype.LookupArray(array.SQLType.ID(), array.Name(), other.SQLType)
		candidate.SetDescriptor(array.Address(), &array)
		if err := extension.StockIsn.ValidateExtension(e.Catalog.Read(candidate), ext); err == nil {
			t.Fatal("cross-family companion accepted")
		}
		for _, offset := range []int{1, 2} {
			codec := before.MustProcedure(ext.Members[slot+offset])
			source := [...]string{"", "ean13_in", "ean13_out"}[offset]
			if slot == 3 {
				source = [...]string{"", "isbn_in", "isn_out"}[offset]
			}
			if slot == 6 {
				source = [...]string{"", "isbn_in", "ean13_out"}[offset]
			}
			if slot == 9 {
				source = [...]string{"", "ismn_in", "isn_out"}[offset]
			}
			if slot == 12 {
				source = [...]string{"", "ismn_in", "ean13_out"}[offset]
			}
			if slot == 15 {
				source = [...]string{"", "issn_in", "isn_out"}[offset]
			}
			if slot == 18 {
				source = [...]string{"", "issn_in", "ean13_out"}[offset]
			}
			if slot == 21 {
				source = [...]string{"", "upc_in", "isn_out"}[offset]
			}
			if codec.Display.Source != source {
				t.Fatal("wrong immutable codec source", slot, offset, codec.Display.Source)
			}
			candidate := catalog.CloneState(before.State())
			proc := *candidate.MustProcedure(ext.Members[slot+offset])
			proc.Display.Source = "foreign_symbol"
			candidate.SetDescriptor(proc.Address(), &proc)
			if err := extension.StockIsn.ValidateExtension(e.Catalog.Read(candidate), ext); err == nil {
				t.Fatal("forged codec declaration accepted")
			}
		}
	}
	if e.CatalogRead().State() != before.State() {
		t.Fatal("rejected role mutations changed catalog")
	}
}

// Every codec role is released origin authority, not its current spelling,
// namespace or e edge. Exercise both codecs of every represented nominal type.
func TestInstalledCodecMembershipAuthority(t *testing.T) {
	for _, name := range []string{"isn", "ltree", "seg"} {
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, name, "public"))
			extensionUnitPublish(t, e, op, cubeUnitStage(t, e, op, "uuid-ossp", "public"))
			owner := roleAttributeCreate(t, e, op, "codec_owner", false, parser.RoleOptions{})
			view := e.CatalogRead()
			parent := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name}))
			receiver := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "uuid-ossp"}))
			for _, address := range parent.Installation.Members {
				typ, ok := view.Type(address)
				if !ok || typ.Array == 0 {
					continue
				}
				if err := extension.ValidateTypeOrigin(view, typ); err != nil {
					t.Fatal("genuine nominal companion graph rejected", typ.Name(), err)
				}
				for _, array := range []sqltype.ID{0, typ.SQLType.ID()} {
					broken := catalog.CloneState(view.State())
					bad := *typ
					bad.Array = array
					broken.SetDescriptor(address, &bad)
					if err := extension.ValidateTypeOrigin(view.WithState(broken), &bad); err == nil {
						t.Fatal("missing or substituted canonical companion accepted", typ.Name(), array)
					}
				}
			}
			for _, address := range parent.Installation.Members {
				codec, ok := view.Procedure(address)
				if !ok {
					continue
				}
				symbol, err := extension.ProcedureSymbol(view, codec)
				if err != nil || symbol != codec.Display.Source {
					t.Fatal("released baseline symbol", codec.Name(), symbol, err)
				}
				for _, foreign := range []bool{false, true} {
					state := catalog.CloneState(view.State())
					moved := *codec
					moved.ObjectName = "renamed_" + codec.Name()
					moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
					moved.Extension = ids.ObjectAddress{}
					moved.Owner = owner.Ref
					moved.ACL = authorization.SchemaACLOwner(codec.ACL, codec.Owner, owner.Ref)
					source := *parent
					source.Members = nil
					for _, member := range parent.Members {
						if member != address {
							source.Members = append(source.Members, member)
						}
					}
					state.SetDescriptor(source.Address(), &source)
					if foreign {
						target := *receiver
						target.Members = append(append([]ids.ObjectAddress(nil), receiver.Members...), address)
						moved.Extension = target.Address()
						state.SetDescriptor(target.Address(), &target)
					}
					state.UnbindName(catalog.DescriptorNameKey(codec))
					state.SetDescriptor(address, &moved)
					state.BindName(catalog.DescriptorNameKey(&moved), address)
					current := view.WithState(state)
					if got, err := extension.ProcedureSymbol(current, &moved); err != nil || got != symbol {
						t.Fatal("lawful codec mutation lost released symbol", codec.Name(), foreign, got, err)
					}
					if err := codec.Implementation.ValidateExtension(current, &source); err != nil {
						t.Fatal("lawful current membership rejected", codec.Name(), foreign, err)
					}
					for _, corruption := range []string{"source", "principal", "seal", "reverse"} {
						broken := catalog.CloneState(state)
						bad := moved
						switch corruption {
						case "source":
							bad.Display.Source = "foreign_codec"
						case "principal":
							bad.Owner = (&authorization.Domain{}).Principal(moved.Owner.ID())
						case "seal":
							unsealed := *moved.Origin.Installation
							bad.Origin = &catalog.InstalledOrigin{Installation: &unsealed, Declaration: moved.Origin.Declaration}
						case "reverse":
							bad.Extension = source.Address()
						}
						broken.SetDescriptor(address, &bad)
						if _, err := extension.ProcedureSymbol(view.WithState(broken), &bad); err == nil {
							t.Fatal("codec corruption admitted", codec.Name(), foreign, corruption)
						}
						if err := codec.Implementation.ValidateExtension(view.WithState(broken), &source); err == nil {
							t.Fatal("current installation ignored codec corruption", codec.Name(), foreign, corruption)
						}
					}
				}
			}
			absent := catalog.CloneState(view.State())
			for _, address := range parent.Members {
				member := absent.Get(address)
				detached, err := catalog.WithExtensionMembership(member, ids.ObjectAddress{}, member.Revision())
				if err != nil {
					t.Fatal(err)
				}
				absent.SetDescriptor(address, detached)
			}
			absent.UnbindName(catalog.DescriptorNameKey(parent))
			absent.RemoveDescriptor(parent.Address())
			for _, address := range parent.Installation.Members {
				if typ, ok := absent.Get(address).(*catalog.Type); ok {
					if err := extension.ValidateTypeOrigin(view.WithState(absent), typ); err != nil {
						t.Fatal("absent origin lost genuine nominal companion graph", typ.Name(), err)
					}
					continue
				}
				codec, ok := absent.Get(address).(*catalog.Procedure)
				if !ok {
					continue
				}
				current := view.WithState(absent)
				if symbol, err := extension.ProcedureSymbol(current, codec); err != nil || symbol != codec.Display.Source {
					t.Fatal("absent origin lost released codec authority", codec.Name(), symbol, err)
				}
				if name, err := extension.ProcedureInstallationName(current, codec); err != nil || name != codec.Name() {
					t.Fatal("C symbol replaced released SQL identity", codec.Name(), name, err)
				}
			}
		})
	}
}
