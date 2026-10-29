package reference_test

import "stuntgres/reference/fixture"

func brinControlViewSteps()[]fixture.Step{
 return []fixture.Step{
  fixture.Select("SELECT * FROM pi_types",[]string{"kind"},[]string{"TEXT"},[][]any{{"meta"}}),
  fixture.Select("SELECT * FROM pi_meta_view",brinMetaNames,brinMetaTypes,[][]any{{"0xA8109CFA",int64(1),int64(128),int64(1)}}),
  fixture.Select("SELECT * FROM pi_items_view ORDER BY itemoffset,attnum",brinItemNames,brinItemTypes,[][]any{{int64(1),int64(2147549186),int64(1),false,false,false,false,"{-7 .. 42}"},{int64(2),nil,nil,nil,nil,nil,nil,nil}}),
  fixture.Select("SELECT * FROM pi_rev_view",[]string{"pages"},[]string{"TID"},[][]any{{nil}}),
 }
}

func brinControlCatalogSteps(moved,pg bool)[]fixture.Step{
 // The public fixture uses postgres natively and its configured stuntgres
 // superuser on the pinned oracle (fixture/lifecycle.go), not a learned owner.
 principal:="postgres";if pg{principal="stuntgres"}
 namespace,classifier,owner:="brin_part","brin_page_type",principal
 if moved{namespace,classifier,owner="brin_moved","page_kind","pi_owner"}
 extension:=fixture.Select("SELECT e.extname::text AS extension_name,e.extversion AS version,n.nspname::text AS target_schema,r.rolname::text AS owner_name FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='pageinspect'",[]string{"extension_name","version","target_schema","owner_name"},[]string{"TEXT","TEXT","TEXT","TEXT"},[][]any{{"pageinspect","1.13","brin_part",principal}})
 members:=fixture.Select("SELECT p.proname::text AS member_name,n.nspname::text AS member_schema,r.rolname::text AS owner_name,p.proargtypes::text AS args FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='pageinspect' AND p.proname IN ('brin_page_type','page_kind','brin_metapage_info','brin_revmap_data','brin_page_items')",[]string{"member_name","member_schema","owner_name","args"},[]string{"TEXT","TEXT","TEXT","TEXT"},[][]any{
  {"brin_metapage_info",namespace,principal,"17"},
  {"brin_page_items",namespace,principal,"17 2205"},
  {"brin_revmap_data",namespace,principal,"17"},
  {classifier,namespace,owner,"17"},
 })
 members.Bag=true
 return []fixture.Step{extension,members}
}

// Every relocated function receives independently supplied bytes, including
// values different from its stored view. NULL/empty cursors retain declarations.
func brinMovedSteps()[]fixture.Step{
 classifier:="SELECT brin_moved.page_kind($1)"
 meta:="SELECT * FROM brin_moved.brin_metapage_info($1)"
 revmap:="SELECT * FROM brin_moved.brin_revmap_data($1)"
 items:="SELECT * FROM brin_moved.brin_page_items($1,'pi_idx'::regclass) ORDER BY itemoffset,attnum"
 metadata:=fixture.BRINMeta();metadata[28],metadata[32],metadata[36]=99,7,3
 reverse:=fixture.BRINImage(0xf092);copy(reverse[24:],[]byte{1,0,2,0,1,2})
 rows:=make([][]any,1360);for i:=range rows{rows[i]=[]any{"(0,0)"}};rows[0]=[]any{"(65538,513)"}
 rev:=fixture.Select(revmap,[]string{"pages"},[]string{"TID"},rows,reverse);rev.Bag=true
 return []fixture.Step{
  fixture.Select(classifier,[]string{"page_kind"},[]string{"TEXT"},[][]any{{"regular"}},fixture.BRINImage(0xf093)),
  fixture.Select(classifier,[]string{"page_kind"},[]string{"TEXT"},[][]any{{nil}},make([]byte,8192)),
  fixture.Select(classifier,[]string{"page_kind"},[]string{"TEXT"},[][]any{{nil}},nil),
  fixture.Select(classifier+" WHERE false",[]string{"page_kind"},[]string{"TEXT"},nil,fixture.BRINMeta()),
  fixture.Select(meta,brinMetaNames,brinMetaTypes,[][]any{{"0xA8109CFA",int64(99),int64(7),int64(3)}},metadata),
  fixture.Select(meta,brinMetaNames,brinMetaTypes,[][]any{{nil,nil,nil,nil}},make([]byte,8192)),
  fixture.Select(meta,brinMetaNames,brinMetaTypes,[][]any{{nil,nil,nil,nil}},nil),
  fixture.Select(meta+" WHERE false",brinMetaNames,brinMetaTypes,nil,metadata),
  fixture.Select("SELECT brin_moved.brin_metapage_info($1)",[]string{"brin_metapage_info"},[]string{"RECORD"},[][]any{{"(0xA8109CFA,99,7,3)"}},metadata),
  fixture.Select("SELECT brin_moved.brin_metapage_info($1)",[]string{"brin_metapage_info"},[]string{"RECORD"},[][]any{{nil}},nil),
  rev,
  fixture.Select(revmap,[]string{"pages"},[]string{"TID"},[][]any{{nil}},make([]byte,8192)),
  fixture.Select(revmap,[]string{"pages"},[]string{"TID"},nil,nil),
  fixture.Select(items,brinItemNames,brinItemTypes,[][]any{{int64(1),int64(2147549186),int64(1),false,false,false,false,"{42 .. -7}"}},brinItems(brinInt4(42,-7))),
  fixture.Select(items,brinItemNames,brinItemTypes,nil,make([]byte,8192)),
  fixture.Select(items,brinItemNames,brinItemTypes,nil,nil),
 }
}
