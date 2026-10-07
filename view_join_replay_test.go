package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestViewJoinedDefinitionAndNativeReplay(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, query := range []string{
		"CREATE TABLE a(unused INT,id INT,x TEXT,d DATE)",
		"CREATE TABLE b(spare INT,id BIGINT,y TEXT)",
		"CREATE TABLE c(id NUMERIC(5,2))",
		"INSERT INTO a VALUES(0,1,'a','2000-02-01'),(0,2,'b','2000-01-02'),(0,3,'c','2000-02-01')",
		"INSERT INTO b VALUES(0,1,'u'),(0,1,'v'),(0,3,NULL)",
		"INSERT INTO c VALUES(1.00),(3.00)",
		"CREATE VIEW joined AS SELECT l.id,l.x,r.y FROM a AS l JOIN b AS r ON l.id=r.id AND l.d='2000-02-01'",
		"CREATE VIEW merged AS SELECT id,x,y FROM a JOIN b USING(id)",
		"CREATE VIEW natural_view AS SELECT id,x,y FROM a NATURAL JOIN b",
		"CREATE VIEW repeated(left_id,right_id) AS SELECT l.id,r.id FROM a AS l JOIN a AS r ON l.id<r.id",
		"CREATE VIEW mixed(discard,left_id,label,day,right_id) AS SELECT l.*,r.id FROM a AS l JOIN a AS r ON l.id<r.id",
		"CREATE VIEW nested AS SELECT id,x,y FROM joined",
		"CREATE VIEW chain AS SELECT l.id,l.x,r.y FROM (a AS l JOIN b AS r ON l.id=r.id AND l.d='2000-02-01') JOIN b AS z ON l.id=z.id AND z.y='u'",
		"CREATE VIEW alias_columns AS SELECT q.key AS id,q.label AS x,q.value AS y FROM (a AS l(discard,key,label,day) JOIN b AS r(spare,other,value) ON l.key=r.other) AS q(discard,key,label,day,spare,other,value)",
		"CREATE VIEW converted AS SELECT id FROM a JOIN c USING(id)",
		"ALTER TABLE a RENAME COLUMN id TO aid",
		"ALTER TABLE a RENAME TO renamed_a",
		"ALTER TABLE b RENAME TO renamed_b",
		"CREATE TABLE a(id INT)",
		"INSERT INTO a VALUES(99)",
		"ALTER TABLE renamed_b DROP COLUMN spare",
	} { execSQL(t, db, query) }
	check := func(name, suffix string) {
		var names, types []string
		var want [][]any
		projection, order := "id,x,y", "id"
		switch name {
		case "converted":
			projection = "id"
			names, types = []string{"id"}, []string{"NUMERIC"}
			want = [][]any{{"1"},{"3"}}
		case "repeated":
			projection, order = "left_id,right_id", "left_id"
			names, types = []string{"left_id", "right_id"}, []string{"INT4", "INT4"}
			want = [][]any{{int64(1),int64(2)},{int64(1),int64(3)},{int64(2),int64(3)}}
		case "mixed":
			projection, order = "left_id,label,right_id", "left_id"
			names, types = []string{"left_id", "label", "right_id"}, []string{"INT4", "TEXT", "INT4"}
			want = [][]any{{int64(1),"a",int64(2)},{int64(1),"a",int64(3)},{int64(2),"b",int64(3)}}
		default:
			names, types = []string{"id","x","y"}, []string{"INT4","TEXT","TEXT"}
			if name == "merged" || name == "natural_view" { types[0] = "INT8" }
			want = [][]any{{int64(1),"a","u"},{int64(1),"a","v"},{int64(3),"c",nil}}
			if name == "chain" { want = want[:2] }
		}
		query := "SELECT "+projection+" FROM "+name+suffix+" ORDER BY "+order
		rows, err := db.Query(query)
		queryCoreRows(t, rows, err, names, types, want, true)
		stmt, err := db.Prepare(query)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		rows, err = stmt.Query()
		queryCoreRows(t, rows, err, names, types, want, true)
	}
	for _, name := range []string{"joined","merged","natural_view","repeated","mixed","nested","chain","alias_columns","converted"} {
		check(name, "")
		var definition string
		if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1", name).Scan(&definition); err != nil { t.Fatal(err) }
		execSQL(t, db, "CREATE VIEW "+name+"_copy AS "+definition)
		check(name, "_copy")
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil { t.Fatal(err) }
	destination := openLocal(t, stuntgres.NewDriver(), t.Name()+"_destination")
	execSQL(t, destination, "CREATE TEMPORARY SEQUENCE retained")
	execSQL(t, destination, "CREATE TABLE sentinel(id INT)")
	execSQL(t, destination, "INSERT INTO sentinel VALUES(17)")
	invalid := append(append([]byte(nil), dump.Bytes()...), []byte("CREATE VIEW invalid AS SELECT missing FROM renamed_a;\n")...)
	if err := stuntgres.Restore(context.Background(), destination, bytes.NewReader(invalid)); err == nil { t.Fatal("invalid suffix restored") }
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	if err := stuntgres.Restore(ctx, destination, bytes.NewReader(dump.Bytes())); err == nil { t.Fatal("canceled restore succeeded") }
	rows, err := destination.Query("SELECT id FROM sentinel")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
	if err := stuntgres.Restore(context.Background(), destination, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	db = destination
	for _, name := range []string{"joined","merged","natural_view","repeated","mixed","nested","chain","alias_columns","converted"} { check(name, ""); check(name, "_copy") }
	execSQL(t, db, "INSERT INTO renamed_a VALUES(0,4,'d','2000-02-01')")
	execSQL(t, db, "INSERT INTO renamed_b VALUES(4,'w')")
	rows, err = db.Query("SELECT id,x,y FROM nested WHERE id=4")
	queryCoreRows(t, rows, err, []string{"id","x","y"}, []string{"INT4","TEXT","TEXT"}, [][]any{{int64(4),"d","w"}}, false)
}

// The ORDER belongs to the stored input namespace, not the outer read. Both
// occurrences of the self-join must retain the old key after the base rename.
func TestViewJoinedNamespaceReplay(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, query := range []string{
		"CREATE TABLE a(unused INT,id INT)",
		"CREATE TABLE c(id NUMERIC(5,2))",
		"INSERT INTO a VALUES(0,3),(0,1),(0,2)",
		"INSERT INTO c VALUES(3.00),(1.00)",
		"CREATE VIEW self_sorted AS SELECT id FROM a AS l JOIN a AS r USING(id) ORDER BY id",
		"CREATE VIEW converted_sorted AS SELECT id FROM a JOIN c USING(id) ORDER BY id",
		"CREATE VIEW alias_sorted AS SELECT q.key AS id FROM (a AS l(discard,key) JOIN a AS r(discard,key) USING(key)) AS q(key,left_discard,right_discard) ORDER BY key",
		"ALTER TABLE a RENAME COLUMN id TO aid",
		"ALTER TABLE a DROP COLUMN unused",
	} { execSQL(t, db, query) }
	check := func(name, suffix string) {
		typ, want := "INT4", [][]any{{int64(1)},{int64(2)},{int64(3)}}
		if name == "converted_sorted" { typ, want = "NUMERIC", [][]any{{"1"},{"3"}} }
		query := "SELECT id FROM "+name+suffix
		rows, err := db.Query(query)
		queryCoreRows(t, rows, err, []string{"id"}, []string{typ}, want, false)
		stmt, err := db.Prepare(query)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		rows, err = stmt.Query()
		queryCoreRows(t, rows, err, []string{"id"}, []string{typ}, want, false)
	}
	for _, name := range []string{"self_sorted","converted_sorted","alias_sorted"} {
		check(name, "")
		var definition string
		if err := db.QueryRow("SELECT definition FROM pg_catalog.pg_views WHERE viewname=$1",name).Scan(&definition); err != nil { t.Fatal(err) }
		execSQL(t, db, "CREATE VIEW "+name+"_copy AS "+definition)
		check(name, "_copy")
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil { t.Fatal(err) }
	destination := openLocal(t, stuntgres.NewDriver(), t.Name()+"_destination")
	if err := stuntgres.Restore(context.Background(), destination, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	db = destination
	for _, name := range []string{"self_sorted","converted_sorted","alias_sorted"} { check(name,""); check(name,"_copy") }
}
