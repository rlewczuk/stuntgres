package core_test

import (
 "slices"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/pgcatalog"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/sqltype"
)

func TestAttributeStatisticsCatalogPseudoAdmission(t *testing.T){
 e:=engine.New();read:=e.CatalogRead()
 source:=read.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatsOID})
 if err:=core.RelationProvider.Validate(read,source);err!=nil{t.Fatal("released pg_stats descriptor rejected",err)}
 expected:=map[int]string{7:"most_common_vals",9:"histogram_bounds",11:"most_common_elems",14:"range_length_histogram",16:"range_bounds_histogram"}
 for i,column:=range source.Columns{
  name,pseudo:=expected[i]
  if pseudo&&(column.Name!=name||column.Usage.Type.ID()!=2277||!column.Usage.CatalogNullUsage()){t.Fatal("released pseudo result slot",i,column)}
  if !pseudo{
   bad:=*source;bad.Columns=slices.Clone(source.Columns);bad.Columns[i].Usage=source.Columns[7].Usage
   if err:=core.RelationProvider.Validate(read,&bad);err==nil{t.Fatal("ANYARRAY escaped released pg_stats attribute",i)}
  }
 }
 foreign:=engine.New().CatalogRead();anyarray,_:=foreign.LookupType(2277)
 bad:=*source;bad.Columns=slices.Clone(source.Columns);bad.Columns[7].Usage=anyarray.Usage()
 if err:=core.RelationProvider.Validate(read,&bad);err==nil{t.Fatal("foreign pseudo descriptor generation admitted")}
 bad=*source;bad.VirtualView=false
 if err:=core.RelationProvider.Validate(read,&bad);err==nil{t.Fatal("stored pg_stats pseudo columns admitted")}
 // Existing bounded carriers remain valid under their own independent gates.
 for _,oid:=range []ids.OID{pgcatalog.AttributeOID,pgcatalog.StatisticExtDataOID}{d:=read.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:oid});if err:=core.RelationProvider.Validate(read,d);err!=nil{t.Fatal("existing pseudo carrier rejected",oid,err)}}
}

func TestAttributeStatisticsPolymorphicOrderingAdmission(t *testing.T) {
	read := engine.New().CatalogRead()
	for _, tc := range []struct {
		name string
		typeID sqltype.ID
		want bool
	}{
		{"integer", 23, true},
		{"integer array", 1007, true},
		{"name array", 1003, true},
		{"json", 114, false},
		{"json array", 199, false},
		{"integer range", 3904, true},
		{"integer multirange", 4451, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typ, found := read.LookupType(tc.typeID)
			if !found {
				t.Fatal("missing released type", tc.typeID)
			}
			got, err := storage.AttributeStatisticsHasLessThan(read, typ.Usage())
			if err != nil || got != tc.want {
				t.Fatalf("less-than availability = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
