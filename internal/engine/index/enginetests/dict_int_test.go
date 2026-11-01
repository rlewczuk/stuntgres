package enginetests_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestDictIntIndexRoot(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE EXTENSION dict_int",
		"CREATE TEXT SEARCH DICTIONARY dict_int_index_dict (TEMPLATE=intdict_template,maxlen=3,absval=true)",
		"CREATE TEXT SEARCH CONFIGURATION dict_int_index_cfg (COPY=pg_catalog.simple)",
		"ALTER TEXT SEARCH CONFIGURATION dict_int_index_cfg ALTER MAPPING FOR int,uint WITH dict_int_index_dict",
		"CREATE TABLE dict_int_index_rows(id int,v tsvector)",
		"INSERT INTO dict_int_index_rows VALUES(1,to_tsvector('dict_int_index_cfg','-123456')),(2,to_tsvector('dict_int_index_cfg','42')),(3,NULL)",
		"CREATE INDEX dict_int_index_gist ON dict_int_index_rows USING gist(v)",
	} {
		enginetest.Run(t, e, q)
	}
	parent := gistCopyRelation(t, e, "dict_int_index_rows")
	index := gistCopyIndex(t, e, parent)
	integer, small, stopped := "'123':1", "'42':1", "'42':2"
	original := gistCopyMembership(t, e, parent, index, map[int64]*string{1: &integer, 2: &small, 3: nil})
	before := gistCopyEntries(original)
	enginetest.Run(t, e, "INSERT INTO dict_int_index_rows VALUES(4,to_tsvector('dict_int_index_cfg','+123999'))")
	inserted := gistCopyMembership(t, e, parent, index, map[int64]*string{1: &integer, 2: &small, 3: nil, 4: &integer})
	insertedEntries := gistCopyEntries(inserted)
	enginetest.Run(t, e, "UPDATE dict_int_index_rows SET v=to_tsvector('dict_int_index_cfg','42') WHERE id=1")
	updated := gistCopyMembership(t, e, parent, index, map[int64]*string{1: &small, 2: &small, 3: nil, 4: &integer})
	updatedEntries := gistCopyEntries(updated)
	enginetest.Run(t, e, "DELETE FROM dict_int_index_rows WHERE id=4")
	deleted := gistCopyMembership(t, e, parent, index, map[int64]*string{1: &small, 2: &small, 3: nil})
	enginetest.Run(t, e, "ALTER TEXT SEARCH DICTIONARY dict_int_index_dict (rejectlong=true)")
	enginetest.Run(t, e, "ALTER TEXT SEARCH CONFIGURATION dict_int_index_cfg ALTER MAPPING FOR int,uint WITH dict_int_index_dict,pg_catalog.simple")
	enginetest.Run(t, e, "INSERT INTO dict_int_index_rows VALUES(5,to_tsvector('dict_int_index_cfg','123456 42'))")
	final := gistCopyMembership(t, e, parent, index, map[int64]*string{1: &small, 2: &small, 3: nil, 5: &stopped})
	if original == inserted || inserted == updated || updated == deleted || deleted == final || !reflect.DeepEqual(original.Entries, before) || !reflect.DeepEqual(inserted.Entries, insertedEntries) || !reflect.DeepEqual(updated.Entries, updatedEntries) {
		t.Fatal("DML reused or mutated retained normalized typed roots")
	}
}
