package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func TestUpcCoercion(t *testing.T) {
	t.Run("prepared_authority", func(t *testing.T) { testIsnPreparedAuthorityRebind(t, "upc", 21) })
	t.Run("prepared_literal", func(t *testing.T) { testIsnPreparedLiteralReown(t, "upc", 21) })
	t.Run("owner_replay", func(t *testing.T) { testIsnRestoreOwnerReplay(t, "upc", 21) })
	e, ext, ean := isnUnitInstalled(t)
	view := e.CatalogRead()
	upc := view.MustType(ext.Members[21])
	for _, pair := range [][2]*sqltype.Descriptor{{upc.SQLType,ean.SQLType},{ean.SQLType,upc.SQLType}} {
		if err := catalog.CanCoerce(view,pair[0].Usage(),pair[1].Usage(),sqltype.Explicit); err != nil { t.Fatal(err) }
	}
	foreign,err := sqltype.Upc(upc.SQLType.ID(),upc.Name(),upc.SQLType.SQLName())
	if err != nil { t.Fatal(err) }
	for _, pair := range [][2]*sqltype.Descriptor{{foreign,ean.SQLType},{ean.SQLType,foreign}} {
		if err := catalog.CanCoerce(view,pair[0].Usage(),pair[1].Usage(),sqltype.Explicit); err == nil { t.Fatal("equal OID foreign endpoint admitted") }
	}
	other,otherExt,_ := isnUnitInstalled(t)
	otherUpc := other.CatalogRead().MustType(otherExt.Members[21])
	if err := catalog.CanCoerce(view,otherUpc.SQLType.Usage(),ean.SQLType.Usage(),sqltype.Explicit);err == nil {t.Fatal("foreign engine endpoint admitted")}
}
