package typedef

import (
 "testing"
 "stuntgres/internal/sqltype"
)

func TestTIDArrayStorageAdmission(t *testing.T){
 types:=map[sqltype.ID]*sqltype.Descriptor{};for _,d:=range sqltype.Builtins(){types[d.ID()]=d}
 if !types[1010].Concrete() || types[1010].Element()!=types[sqltype.Tid] || !StoredArrayElement(types[sqltype.Tid]) || !StoredArrayElement(types[1010]){t.Fatal("TID array lost generic storage admission")}
 domain,err:=sqltype.Domain(60001,"stored_tid","public.stored_tid",types[sqltype.Tid].Usage());if err!=nil{t.Fatal(err)}
 if !StoredArrayElement(domain){t.Fatal("TID domain lost generic element admission")}
 for _,id:=range []sqltype.ID{sqltype.Lseg,sqltype.Circle,sqltype.Macaddr,sqltype.Macaddr8,sqltype.Inet,sqltype.Cidr,sqltype.Xid,sqltype.Cid,sqltype.Xid8,sqltype.PgSnapshot}{if StoredArrayElement(types[id]){t.Fatal("unrelated storage exclusion changed",id)}}
}
