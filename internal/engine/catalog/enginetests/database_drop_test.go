package enginetests_test

import (
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestDatabaseDropSharedCatalogState(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE drop_catalog_local(v integer)")
	if _, err := enginetest.Execute(e, t.Context(), &parser.CreateDatabaseStmt{Name: "drop_catalog_peer"}, nil); err != nil {
		t.Fatal(err)
	}
	full := e.CatalogRead()
	local := enginetest.RoleTableRef(t, e, "drop_catalog_local")
	peer := full.NameAddress(ids.NameKey{ClassID: ids.DatabaseClassID, Name: "drop_catalog_peer"})
	next := catalog.CloneState(full.State())
	localKey := catalog.SecurityLabelKey{Object: local.Address, Provider: "selinux"}
	sharedKey := catalog.SecurityLabelKey{Object: peer, Provider: "selinux"}
	localLabel, sharedLabel := &catalog.SecurityLabelRecord{Label: "local"}, &catalog.SecurityLabelRecord{Label: "shared"}
	next.SetSecurityLabel(localKey, localLabel)
	next.SetSecurityLabel(sharedKey, sharedLabel)
	own := e.Catalog.DatabaseAddress()
	descriptor := next.Get(own)
	next.UnbindName(catalog.DescriptorNameKey(descriptor))
	next.RemoveDescriptor(own)
	retired := catalog.SharedOnlyState(next, full.Epoch()+1)
	if retired.Epoch() != full.Epoch()+1 || retired.Get(own) != nil || retired.Get(local.Address) != nil || retired.SecurityLabel(localKey) != nil {
		t.Fatal("shared-only state retained local incarnation")
	}
	if retired.Get(peer) != next.Get(peer) || retired.SecurityLabel(sharedKey) != sharedLabel || retired.ParameterIncarnation() != next.ParameterIncarnation() {
		t.Fatal("shared-only state replaced immutable authority")
	}
	for address, descriptor := range next.Descriptors() {
		shared := address.ClassID == ids.DatabaseClassID || address.ClassID == ids.SubscriptionClassID || address.ClassID == ids.ReplicationOriginClassID || address.ClassID == ids.ParameterClassID || address.ClassID == ids.TablespaceClassID
		if shared && retired.Get(address) != descriptor {
			t.Fatal("shared descriptor was recopied or omitted", address)
		}
		if !shared && retired.Get(address) != nil {
			t.Fatal("local descriptor survived tombstone", address)
		}
	}
	if next.Get(local.Address) != full.Get(local.Address) || next.SecurityLabel(localKey) != localLabel || full.Get(own) != descriptor {
		t.Fatal("retirement mutated source/retained backing")
	}
}
