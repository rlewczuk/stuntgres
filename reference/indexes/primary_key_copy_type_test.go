package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePrimaryKeyLike(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE source(a INT,b TEXT,CONSTRAINT source_pk PRIMARY KEY(a,b))", 0))
		// SQL index comments are an external capability; native descriptor
		// comment/copy preservation has a package-function counterpart.
		if b.PG {
			run(fixture.Exec("COMMENT ON INDEX source_pk IS 'source primary key'", 0))
		}
		run(fixture.Exec("INSERT INTO source VALUES(1,'x')", 1))
		source := fixture.PrimaryKeyReferenceIDs(t, p, b, "source_pk")
		run(fixture.Exec("CREATE TABLE plain(LIKE source)", 0))
		run(fixture.Exec("CREATE TABLE copied(LIKE source INCLUDING INDEXES)", 0))
		run(fixture.Exec("CREATE TABLE allcopy(LIKE source INCLUDING ALL)", 0))
		for _, name := range []string{"copied", "allcopy"} {
			ids := fixture.PrimaryKeyReferenceIDs(t, p, b, name+"_pkey")
			for _, id := range ids {
				for _, old := range source {
					if id == old {
						t.Fatal("LIKE reused source identity", source, ids)
					}
				}
			}
			run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname=$1", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"PRIMARY KEY (a, b)"}}, name+"_pkey"))
			run(fixture.Exec("INSERT INTO "+name+" VALUES(1,'x')", 1))
			run(fixture.ErrorDiagnostic("INSERT INTO "+name+" VALUES(1,'x')", fixture.UniqueReferenceDuplicate(name, name+"_pkey", "Key (a, b)=(1, x) already exists.")))
			run(fixture.ErrorDiagnostic("INSERT INTO "+name+" VALUES(NULL,'x')", fixture.PrimaryKeyReferenceNull(name, "a", "null, x")))
			fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, "SELECT * FROM "+name, []string{"a", "b"}, []string{"INT4", "TEXT"}, []bool{false, false}, 1)
		}
		run(fixture.Select("SELECT obj_description('copied_pkey'::regclass,'pg_class') AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{nil}}))
		var copiedComment any
		if b.PG {
			copiedComment = "source primary key"
		}
		run(fixture.Select("SELECT obj_description('allcopy_pkey'::regclass,'pg_class') AS comment", []string{"comment"}, []string{"TEXT"}, [][]any{{copiedComment}}))
		for _, c := range []struct{ table, ddl, unique string }{
			{"equivalent", "CREATE TABLE equivalent(LIKE source INCLUDING INDEXES,CONSTRAINT chosen UNIQUE(a,b))", "chosen"},
			{"reversecopy", "CREATE TABLE reversecopy(CONSTRAINT chosen_reverse UNIQUE(a,b),LIKE source INCLUDING INDEXES)", "chosen_reverse"},
		} {
			run(fixture.Exec(c.ddl, 0))
			copied := fixture.PrimaryKeyReferenceIDs(t, p, b, c.table+"_pkey")
			run(fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1 AND (contype='p' OR contype='u') ORDER BY conname", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, [][]any{{c.unique, "u", "UNIQUE (a, b)"}, {c.table + "_pkey", "p", "PRIMARY KEY (a, b)"}}, copied[1]))
			var separate [3]int64
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid,conrelid FROM pg_constraint WHERE conname=$1 AND contype='u'", c.unique).Scan(&separate[0], &separate[1]); err != nil {
				t.Fatal(err)
			}
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1 AND relkind='i'", c.unique).Scan(&separate[2]); err != nil {
				t.Fatal(err)
			}
			if separate[0] <= 0 || separate[2] <= 0 || copied[0] == separate[0] || copied[2] == separate[2] || copied[1] != separate[1] {
				t.Fatal("copied PK and explicit UNIQUE must remain independent", copied, separate)
			}
			run(fixture.Exec("INSERT INTO "+c.table+" VALUES(1,'x')", 1))
			run(fixture.Exec("ALTER TABLE "+c.table+" DROP CONSTRAINT "+c.table+"_pkey", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO "+c.table+" VALUES(1,'x')", fixture.UniqueReferenceDuplicate(c.table, c.unique, "Key (a, b)=(1, x) already exists.")))
		}
		run(fixture.Exec("INSERT INTO plain VALUES(1,'x'),(1,'x')", 2))
		run(fixture.ErrorDiagnostic("INSERT INTO plain VALUES(1,NULL)", fixture.PrimaryKeyReferenceNull("plain", "b", "1, null")))
		run(fixture.Select("SELECT contype FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relname='plain' ORDER BY c.conname", []string{"contype"}, []string{"CHAR"}, [][]any{{"n"}, {"n"}}))
		run(fixture.Exec("DROP TABLE source", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO copied VALUES(1,'x')", fixture.UniqueReferenceDuplicate("copied", "copied_pkey", "Key (a, b)=(1, x) already exists.")))
		run(fixture.Select("SELECT * FROM plain", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(1), "x"}}))
	})
}

func TestReferencePrimaryKeyLikeMultiple(t *testing.T) {
	for _, c := range []struct{ name, ddl string }{
		{"copied_then_explicit", "CREATE TABLE t(LIKE source INCLUDING INDEXES,PRIMARY KEY(a))"},
		{"explicit_then_copied", "CREATE TABLE t(PRIMARY KEY(a),LIKE source INCLUDING INDEXES)"},
		{"two_copied", "CREATE TABLE t(LIKE source INCLUDING INDEXES,LIKE other INCLUDING INDEXES)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE source(a INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE other(b INT PRIMARY KEY)", 0))
				run(fixture.ImplicitError(c.ddl, fixture.Diagnostic{Code: "42P16", Message: `multiple primary keys for table "t" are not allowed`}))
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='t' OR relname='t_pkey' OR relname='t_pkey1'", []string{"relname"}, []string{"NAME"}, nil))
				fixture.PrimaryKeyReferenceIDs(t, p, b, "source_pkey")
				fixture.PrimaryKeyReferenceIDs(t, p, b, "other_pkey")
			})
		})
	}
}

func TestReferencePrimaryKeyTypeBuild(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT PRIMARY KEY,v TEXT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'one'),(2,'two')", 2))
		before := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey")
		var beforeNotNull int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid=$1 AND contype='n' AND conname='t_k_not_null'", before[1]).Scan(&beforeNotNull); err != nil || beforeNotNull <= 0 {
			t.Fatal(beforeNotNull, err)
		}
		run(fixture.Prepared(fixture.Exec("ALTER TABLE t ALTER k TYPE BIGINT", 0)))
		after := fixture.PrimaryKeyReferenceIDs(t, p, b, "t_pkey")
		if before[0] == after[0] || before[2] == after[2] || before[1] != after[1] {
			t.Fatal("TYPE rebuild must remint key/index, retaining table", before, after)
		}
		var afterNotNull int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid=$1 AND contype='n' AND conname='t_k_not_null'", after[1]).Scan(&afterNotNull); err != nil || afterNotNull <= 0 || afterNotNull == beforeNotNull {
			t.Fatal("TYPE must remint affected NOT NULL identity", beforeNotNull, afterNotNull, err)
		}
		run(fixture.PrimaryKeyReferenceCatalog("t", [][]any{{"t_k_not_null", "n", "NOT NULL k", nil}, {"t_pkey", "p", "PRIMARY KEY (k)", nil}}))
		run(fixture.Select("SELECT * FROM t ORDER BY k", []string{"k", "v"}, []string{"INT8", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}}))
		fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, true, "SELECT * FROM t", []string{"k", "v"}, []string{"INT8", "TEXT"}, []bool{false, true}, 2)
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'bad')", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (k)=(1) already exists.")))
		run(fixture.Exec("CREATE TABLE collision(k TEXT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO collision VALUES('01'),('1')", 2))
		original := fixture.PrimaryKeyReferenceIDs(t, p, b, "collision_pkey")
		duplicate := fixture.UniqueReferenceDuplicate("collision", "collision_pkey", "Key (k)=(1) is duplicated.")
		duplicate.Message = `could not create unique index "collision_pkey"`
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("ALTER TABLE collision ALTER k TYPE INT USING k::INT", duplicate)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			if retained := fixture.PrimaryKeyReferenceIDs(t, p, b, "collision_pkey"); retained != original {
				t.Fatal("failed TYPE changed identities", original, retained)
			}
			run(fixture.Select("SELECT k FROM collision ORDER BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"01"}, {"1"}}))
		}
		null := fixture.ControlsReferenceDiagnostic("23502", `column "k" of relation "collision" contains null values`)
		null.Schema, null.Table, null.Column = "public", "collision", "k"
		run(fixture.ErrorDiagnostic("ALTER TABLE collision ALTER k TYPE INT USING NULL::INT", null))
		if retained := fixture.PrimaryKeyReferenceIDs(t, p, b, "collision_pkey"); retained != original {
			t.Fatal("NULL TYPE changed identities", original, retained)
		}
		run(fixture.ErrorDiagnostic("INSERT INTO collision VALUES('01')", fixture.UniqueReferenceDuplicate("collision", "collision_pkey", "Key (k)=(01) already exists.")))
		run(fixture.Select("SELECT k FROM collision ORDER BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"01"}, {"1"}}))
	})
}

func TestReferencePrimaryKeyCompositeTypeIdentities(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE multi(a INT CONSTRAINT a_nn NOT NULL,b TEXT CONSTRAINT b_nn NOT NULL,payload INT CONSTRAINT payload_nn NOT NULL,PRIMARY KEY(a,b))", 0))
				run(fixture.Exec("INSERT INTO multi VALUES(1,'x',9),(1,'y',10)", 2))
				before := fixture.PrimaryKeyReferenceIDs(t, p, b, "multi_pkey")
				capture := func() map[string]int64 {
					rows, err := b.DB.QueryContext(p.Ctx, "SELECT conname,oid::bigint FROM pg_constraint WHERE conrelid=$1 AND contype='n'", before[1])
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					ids := make(map[string]int64)
					for rows.Next() {
						var name string
						var id int64
						if err := rows.Scan(&name, &id); err != nil || id <= 0 {
							t.Fatal(name, id, err)
						}
						ids[name] = id
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					if len(ids) != 3 || ids["a_nn"] <= 0 || ids["b_nn"] <= 0 || ids["payload_nn"] <= 0 {
						t.Fatal("actual independently named NN identities", ids)
					}
					return ids
				}
				oldNN := capture()
				alter := fixture.Exec("ALTER TABLE multi ALTER a TYPE BIGINT", 0)
				alter.Prepared = prepared
				run(alter)
				after, nextNN := fixture.PrimaryKeyReferenceIDs(t, p, b, "multi_pkey"), capture()
				if after[0] == before[0] || after[2] == before[2] || after[1] != before[1] || nextNN["a_nn"] == oldNN["a_nn"] || nextNN["a_nn"] <= after[0] || nextNN["b_nn"] != oldNN["b_nn"] || nextNN["payload_nn"] != oldNN["payload_nn"] {
					t.Fatal("affected-only p/index/n remint and relative order", before, after, oldNN, nextNN)
				}
				run(fixture.PrimaryKeyReferenceCatalog("multi", [][]any{{"a_nn", "n", "NOT NULL a", nil}, {"b_nn", "n", "NOT NULL b", nil}, {"multi_pkey", "p", "PRIMARY KEY (a, b)", nil}, {"payload_nn", "n", "NOT NULL payload", nil}}))
				run(fixture.Select("SELECT * FROM multi ORDER BY b", []string{"a", "b", "payload"}, []string{"INT8", "TEXT", "INT4"}, [][]any{{int64(1), "x", int64(9)}, {int64(1), "y", int64(10)}}))
				fixture.TableDDLMetadata(t, p.Ctx, b.DB, b.PG, prepared, "SELECT * FROM multi", []string{"a", "b", "payload"}, []string{"INT8", "TEXT", "INT4"}, []bool{false, false, false}, 2)
				run(fixture.ErrorDiagnostic("INSERT INTO multi VALUES(1,'x',99)", fixture.UniqueReferenceDuplicate("multi", "multi_pkey", "Key (a, b)=(1, x) already exists.")))
				run(fixture.ErrorDiagnostic("INSERT INTO multi VALUES(2,NULL,11)", fixture.PrimaryKeyReferenceNull("multi", "b", "2, null, 11")))
				run(fixture.ErrorDiagnostic("INSERT INTO multi VALUES(2,'z',NULL)", fixture.PrimaryKeyReferenceNull("multi", "payload", "2, z, null")))
				if retained := fixture.PrimaryKeyReferenceIDs(t, p, b, "multi_pkey"); retained != after {
					t.Fatal("failed typed writes changed rebuilt group", after, retained)
				}
				run(fixture.Select("SELECT * FROM multi ORDER BY b", []string{"a", "b", "payload"}, []string{"INT8", "TEXT", "INT4"}, [][]any{{int64(1), "x", int64(9)}, {int64(1), "y", int64(10)}}))
			})
		})
	}
}
