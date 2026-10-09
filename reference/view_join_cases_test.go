package reference_test

// Declarative seeds retain both physical attribute layouts and every view shape.
func referenceViewJoinSeed(leading bool) []referenceStep {
	a, b, insertA, insertB := "CREATE TABLE a(id INT,x TEXT,d DATE)", "CREATE TABLE b(id BIGINT,y TEXT)", "INSERT INTO a VALUES(1,'a','2000-02-01'),(2,'b','2000-01-02'),(3,'c','2000-02-01')", "INSERT INTO b VALUES(1,'u'),(1,'v'),(3,NULL)"
	repeated := "CREATE VIEW repeated(left_id,label,day,right_id) AS SELECT l.*,r.id FROM a AS l JOIN a AS r ON l.id<r.id"
	aliases := "CREATE VIEW alias_columns AS SELECT q.key AS id,q.label AS x,q.value AS y FROM (a AS l(key,label,day) JOIN b AS r(other,value) ON l.key=r.other) AS q(key,label,day,other,value)"
	if leading {
		a, b = "CREATE TABLE a(unused INT,id INT,x TEXT,d DATE)", "CREATE TABLE b(spare INT,id BIGINT,y TEXT)"
		insertA, insertB = "INSERT INTO a VALUES(0,1,'a','2000-02-01'),(0,2,'b','2000-01-02'),(0,3,'c','2000-02-01')", "INSERT INTO b VALUES(0,1,'u'),(0,1,'v'),(0,3,NULL)"
		repeated = "CREATE VIEW repeated(left_id,right_id) AS SELECT l.id,r.id FROM a AS l JOIN a AS r ON l.id<r.id"
		aliases = "CREATE VIEW alias_columns AS SELECT q.key AS id,q.label AS x,q.value AS y FROM (a AS l(discard,key,label,day) JOIN b AS r(spare,other,value) ON l.key=r.other) AS q(discard,key,label,day,spare,other,value)"
	}
	steps := []referenceStep{
		referenceExec(a, 0), referenceExec(b, 0), referenceExec("CREATE TABLE c(id NUMERIC(5,2))", 0), referenceExec(insertA, 3), referenceExec(insertB, 3), referenceExec("INSERT INTO c VALUES(1.00),(3.00)", 2),
		referenceExec("CREATE VIEW joined AS SELECT l.id,l.x,r.y FROM a AS l JOIN b AS r ON l.id=r.id AND l.d='2000-02-01'", 0),
		referenceExec("CREATE VIEW merged AS SELECT id,x,y FROM a JOIN b USING(id)", 0), referenceExec("CREATE VIEW natural_view AS SELECT id,x,y FROM a NATURAL JOIN b", 0), referenceExec(repeated, 0),
		referenceExec("CREATE VIEW chain AS SELECT l.id,l.x,r.y FROM (a AS l JOIN b AS r ON l.id=r.id AND l.d='2000-02-01') JOIN b AS z ON l.id=z.id AND z.y='u'", 0), referenceExec(aliases, 0), referenceExec("CREATE VIEW converted AS SELECT id FROM a JOIN c USING(id)", 0),
	}
	if leading {
		steps = append(steps, referenceExec("CREATE VIEW mixed(discard,left_id,label,day,right_id) AS SELECT l.*,r.id FROM a AS l JOIN a AS r ON l.id<r.id", 0), referenceExec("CREATE VIEW nested AS SELECT id,x,y FROM joined", 0))
	}
	steps = append(steps, referenceExec("ALTER TABLE a RENAME COLUMN id TO aid", 0), referenceExec("ALTER TABLE a RENAME TO renamed_a", 0), referenceExec("ALTER TABLE b RENAME TO renamed_b", 0), referenceExec("CREATE TABLE a(id INT)", 0), referenceExec("INSERT INTO a VALUES(99)", 1))
	if leading {
		steps = append(steps, referenceExec("ALTER TABLE renamed_b DROP COLUMN spare", 0))
	}
	return steps
}

func referenceViewJoinNames(leading bool) []string {
	names := []string{"joined", "merged", "natural_view", "repeated", "chain", "alias_columns", "converted"}
	if leading {
		names = append(names, "mixed", "nested")
	}
	return names
}

func referenceViewJoinRead(name, suffix string, leading bool) referenceStep {
	projection, order := "id,x,y", "id"
	columns, types := []string{"id", "x", "y"}, []string{"INT4", "TEXT", "TEXT"}
	want := [][]any{{int64(1), "a", "u"}, {int64(1), "a", "v"}, {int64(3), "c", nil}}
	switch name {
	case "merged", "natural_view":
		types = []string{"INT8", "TEXT", "TEXT"}
	case "chain":
		want = [][]any{{int64(1), "a", "u"}, {int64(1), "a", "v"}}
	case "converted":
		projection = "id"
		columns, types = []string{"id"}, []string{"NUMERIC"}
		want = [][]any{{"1"}, {"3"}}
	case "repeated", "mixed":
		order = "left_id"
		if name == "repeated" && leading {
			projection = "left_id,right_id"
			columns, types = []string{"left_id", "right_id"}, []string{"INT4", "INT4"}
			want = [][]any{{int64(1), int64(2)}, {int64(1), int64(3)}, {int64(2), int64(3)}}
		} else {
			projection = "left_id,label,right_id"
			columns, types = []string{"left_id", "label", "right_id"}, []string{"INT4", "TEXT", "INT4"}
			want = [][]any{{int64(1), "a", int64(2)}, {int64(1), "a", int64(3)}, {int64(2), "b", int64(3)}}
		}
	}
	s := referenceSelect("SELECT "+projection+" FROM "+name+suffix+" ORDER BY "+order, columns, types, want)
	s.bag = name != "converted"
	return s
}

func referenceViewNamespaceSeed() []referenceStep {
	return []referenceStep{
		referenceExec("CREATE TABLE a(unused INT,id INT)", 0), referenceExec("CREATE TABLE c(id NUMERIC(5,2))", 0), referenceExec("INSERT INTO a VALUES(0,3),(0,1),(0,2)", 3), referenceExec("INSERT INTO c VALUES(3.00),(1.00)", 2),
		referenceExec("CREATE VIEW self_sorted AS SELECT id FROM a AS l JOIN a AS r USING(id) ORDER BY id", 0), referenceExec("CREATE VIEW converted_sorted AS SELECT id FROM a JOIN c USING(id) ORDER BY id", 0), referenceExec("CREATE VIEW alias_sorted AS SELECT q.key AS id FROM (a AS l(discard,key) JOIN a AS r(discard,key) USING(key)) AS q(key,left_discard,right_discard) ORDER BY key", 0),
		referenceExec("ALTER TABLE a RENAME COLUMN id TO aid", 0), referenceExec("ALTER TABLE a DROP COLUMN unused", 0),
	}
}

func referenceViewNamespaceRead(name, suffix string) referenceStep {
	typ, want := "INT4", [][]any{{int64(1)}, {int64(2)}, {int64(3)}}
	if name == "converted_sorted" {
		typ, want = "NUMERIC", [][]any{{"1"}, {"3"}}
	}
	return referenceSelect("SELECT id FROM "+name+suffix, []string{"id"}, []string{typ}, want)
}
