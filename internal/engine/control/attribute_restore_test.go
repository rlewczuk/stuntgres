package control

import (
	"reflect"
	"stuntgres/internal/parser"
	"testing"
)

func TestAccessRelationsAttributeDropPairs(t *testing.T) {
	const name = "__stuntgres_retired_abc_2"
	for _, tc := range []struct {
		sql      string
		pairs    int
		rejected bool
	}{
		{`CREATE TABLE public.t(a INT,` + name + ` pg_catalog.int4); ALTER TABLE ONLY public.t DROP COLUMN ` + name, 1, false},
		{`CREATE TABLE public.t(a INT,` + name + ` pg_catalog.text); ALTER TABLE ONLY public.t ALTER COLUMN ` + name + ` SET STORAGE EXTERNAL; ALTER TABLE ONLY public.t ALTER COLUMN ` + name + ` SET COMPRESSION pglz; ALTER TABLE ONLY public.t DROP COLUMN ` + name, 1, false},
		{`CREATE TABLE public.p(` + name + ` pg_catalog.text); CREATE TABLE public.c() INHERITS(public.p); ALTER TABLE ONLY public.p ALTER COLUMN ` + name + ` SET STORAGE EXTERNAL; ALTER TABLE ONLY public.p ALTER COLUMN ` + name + ` SET COMPRESSION pglz; ALTER TABLE ONLY public.c ALTER COLUMN ` + name + ` SET STORAGE MAIN; ALTER TABLE ONLY public.c ALTER COLUMN ` + name + ` SET COMPRESSION lz4; ALTER TABLE public.p DROP COLUMN ` + name, 1, false},
		{`CREATE TABLE public.p(a INT,` + name + ` pg_catalog.int4); CREATE TABLE public.c(b INT) INHERITS(public.p); ALTER TABLE public.p DROP COLUMN ` + name, 1, false},
		{`CREATE TABLE public.p(a INT,b INT) PARTITION BY RANGE(a); CREATE TABLE public.c(a INT,` + name + ` pg_catalog.int4,b INT); ALTER TABLE ONLY public.c DROP COLUMN ` + name + `; ALTER TABLE public.p ATTACH PARTITION public.c FOR VALUES FROM(0) TO(10)`, 1, false},
		{`CREATE TYPE public.row_type AS (a INT,b INT); CREATE TABLE public.c(a INT,` + name + ` pg_catalog.int4,b INT); ALTER TABLE ONLY public.c DROP COLUMN ` + name + `; ALTER TABLE public.c OF public.row_type`, 1, false},
		{`CREATE FOREIGN TABLE public.t(a INT,` + name + ` pg_catalog.int4) SERVER files; ALTER TABLE ONLY public.t DROP COLUMN ` + name, 1, false},
		{`CREATE FOREIGN TABLE public.t(a INT,` + name + ` pg_catalog.text) SERVER files; ALTER TABLE ONLY public.t ALTER COLUMN ` + name + ` SET STORAGE EXTERNAL; ALTER TABLE ONLY public.t DROP COLUMN ` + name, 1, false},
		{`CREATE TABLE public.p(a INT,` + name + ` pg_catalog.int4); CREATE FOREIGN TABLE public.c(b INT) INHERITS(public.p) SERVER files; ALTER TABLE public.p DROP COLUMN ` + name, 1, false},
		{`CREATE FOREIGN TABLE public.t(` + name + ` pg_catalog.int4 OPTIONS(force_null 'true')) SERVER files; ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE FOREIGN TABLE public.t(` + name + ` pg_catalog.int4 DEFAULT 1) SERVER files; ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE FOREIGN TABLE public.t(` + name + ` pg_catalog.int4 NOT NULL) SERVER files; ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE FOREIGN TABLE IF NOT EXISTS public.t(` + name + ` pg_catalog.int4) SERVER files; ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` INT); ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.int4 DEFAULT 1); ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.int4 NOT NULL); ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.text DEFAULT NULL); ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.text NOT NULL); ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.text COLLATE "C"); ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.varchar); ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.int4); ALTER TABLE ONLY public.other DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.int4); ALTER TABLE ONLY public.t DROP COLUMN ` + name + ` CASCADE`, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.int4); ALTER TABLE ONLY public.t DROP COLUMN IF EXISTS ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.int4); ALTER TABLE ONLY public.t DROP COLUMN ` + name + `,ADD COLUMN b INT`, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.int4); INSERT INTO public.t VALUES(1); ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.text); ALTER TABLE ONLY public.t ALTER COLUMN ` + name + ` SET STORAGE EXTERNAL; INSERT INTO public.t DEFAULT VALUES; ALTER TABLE ONLY public.t DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.t(` + name + ` pg_catalog.int4); ALTER TABLE public.t DROP COLUMN ` + name, 0, false},
		{`CREATE TABLE public.p(` + name + ` pg_catalog.int4); CREATE TABLE public.c(` + name + ` pg_catalog.int4) INHERITS(public.p); ALTER TABLE public.p DROP COLUMN ` + name, 0, true},
		{`CREATE TABLE public.p(` + name + ` pg_catalog.int4); CREATE TABLE public.c() INHERITS(public.p); INSERT INTO public.c DEFAULT VALUES; ALTER TABLE public.p DROP COLUMN ` + name, 0, true},
	} {
		statements, err := parser.Parse(tc.sql)
		if err != nil {
			t.Fatal("unit syntax", tc.sql, err)
		}
		original, err := parser.Parse(tc.sql)
		if err != nil {
			t.Fatal(err)
		}
		pairs, err := PairAttributeConstructors(statements)
		if (err != nil) != tc.rejected || !tc.rejected && len(pairs.Drops) != tc.pairs {
			t.Fatal("pairing", tc.sql, len(pairs.Drops), err)
		}
		if !reflect.DeepEqual(statements, original) {
			t.Fatal("pairing mutated source AST")
		}
		for _, pair := range pairs.Drops {
			if pair.Column != name || pair.Target.Schema != "public" {
				t.Fatal("paired identity", pair)
			}
			if pair.Recursive && len(pair.Participants) != 2 {
				t.Fatal("recursive closure", pair)
			}
		}
	}
	for _, name := range []string{"__stuntgres_retired_abc_0", "__stuntgres_retired_ABC_2", "__stuntgres_retired_abc_02", "__stuntgres_retired_abc_2_", "__stuntgres_retired_abc_2_0", "ordinary"} {
		if AttributePlaceholderName(name) {
			t.Fatal("noncanonical placeholder", name)
		}
	}
}

func TestAccessRelationsAttributeCheckConstructorPairs(t *testing.T) {
	const create = `CREATE TABLE public.t(a INT); `
	const add = `ALTER TABLE public.t ADD COLUMN b INT; `
	const check = `ALTER TABLE public.t ADD CONSTRAINT positive CHECK(b>0)`
	for _, tc := range []struct {
		name, sql string
		paired    bool
	}{
		{"fresh", create + add + check, true},
		{"existing_target", check, false},
		{"unstaged_target", create + check, false},
		{"other_target", create + add + `ALTER TABLE public.other ADD CONSTRAINT positive CHECK(b>0)`, false},
		{"after_rows", create + add + `INSERT INTO public.t VALUES(1,2); ` + check, false},
		{"after_acl", create + add + `UPDATE pg_catalog.pg_class SET relacl=NULL::pg_catalog.aclitem[] WHERE oid='public.t'::pg_catalog.regclass; ` + check, false},
		{"after_consumer", create + add + `SELECT 1; ` + check, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statements, err := parser.Parse(tc.sql)
			if err != nil {
				t.Fatal(err)
			}
			original, err := parser.Parse(tc.sql)
			if err != nil {
				t.Fatal(err)
			}
			pairs, err := PairAttributeConstructors(statements)
			if err != nil {
				t.Fatal(err)
			}
			last := statements[len(statements)-1].(*parser.AlterTable)
			_, paired := pairs.Constraints[last]
			if paired != tc.paired {
				t.Fatalf("paired=%v want %v", paired, tc.paired)
			}
			if !reflect.DeepEqual(statements, original) {
				t.Fatal("pairing mutated source AST")
			}
			if paired {
				if err := CheckAttributeDefinition(last); err != nil {
					t.Fatal(err)
				}
				last.IfExists = true
				if err := CheckAttributeDefinition(last); err == nil {
					t.Fatal("conditional staged CHECK admitted")
				}
			}
		})
	}
}

func TestAccessRelationsForeignOptionPairs(t *testing.T) {
	const ordinary = `CREATE TABLE public.parent(id INT,txt TEXT); CREATE FOREIGN TABLE public.child() INHERITS(public.parent) SERVER files; `
	const partition = `CREATE TABLE public.parent(id INT,txt TEXT) PARTITION BY RANGE(id); `
	const completion = `ALTER FOREIGN TABLE public.child ALTER COLUMN txt OPTIONS(force_null 'true',force_not_null 'off');`
	const hole = "__stuntgres_retired_abc_2"
	for _, tc := range []struct { name, sql string; number int32; noHistory bool }{
		{"inherited", ordinary + completion, 2, false},
		{"compact partition", partition + `CREATE FOREIGN TABLE public.child PARTITION OF public.parent FOR VALUES FROM(0) TO(10) SERVER files; ` + completion, 2, true},
		{"dense parent live shape", `CREATE TABLE public.parent(id INT,` + hole + ` pg_catalog.int4,txt TEXT) PARTITION BY RANGE(id); ALTER TABLE ONLY public.parent DROP COLUMN ` + hole + `; CREATE FOREIGN TABLE public.child PARTITION OF public.parent FOR VALUES FROM(0) TO(10) SERVER files; ` + completion, 2, true},
		{"separate attachment", partition + `CREATE FOREIGN TABLE public.child(id INT,` + hole + ` pg_catalog.int4,txt TEXT) SERVER files; ALTER TABLE ONLY public.child DROP COLUMN ` + hole + `; ALTER TABLE public.parent ATTACH PARTITION public.child FOR VALUES FROM(0) TO(10); ` + completion, 3, false},
		{"provider parses Boolean", ordinary + `ALTER FOREIGN TABLE public.child ALTER COLUMN txt OPTIONS(force_null 'not-a-Boolean');`, 2, false},
		{"one completion only", ordinary + completion + completion, 2, false},
		{"no constructor", completion, 0, false},
		{"ordinary target", `CREATE TABLE public.child(id INT,txt TEXT); ` + completion, 0, false},
		{"missing parent shape", `CREATE FOREIGN TABLE public.child PARTITION OF public.parent FOR VALUES FROM(0) TO(10) SERVER files; ` + completion, 0, false},
		{"unqualified target", ordinary + `ALTER FOREIGN TABLE child ALTER COLUMN txt OPTIONS(force_null 'true');`, 0, false},
		{"wrong column", ordinary + `ALTER FOREIGN TABLE public.child ALTER COLUMN absent OPTIONS(force_null 'true');`, 0, false},
		{"SET is not generated ADD", ordinary + `ALTER FOREIGN TABLE public.child ALTER COLUMN txt OPTIONS(SET force_null 'true');`, 0, false},
		{"DROP is not generated ADD", ordinary + `ALTER FOREIGN TABLE public.child ALTER COLUMN txt OPTIONS(DROP force_null);`, 0, false},
		{"explicit ADD is not canonical", ordinary + `ALTER FOREIGN TABLE public.child ALTER COLUMN txt OPTIONS(ADD force_null 'true');`, 0, false},
		{"unselected option", ordinary + `ALTER FOREIGN TABLE public.child ALTER COLUMN txt OPTIONS(column_name 'remote');`, 0, false},
		{"duplicate key", ordinary + `ALTER FOREIGN TABLE public.child ALTER COLUMN txt OPTIONS(force_null 'true',force_null 'false');`, 0, false},
		{"multiple actions", ordinary + `ALTER FOREIGN TABLE public.child ALTER COLUMN txt OPTIONS(force_null 'true'),ALTER COLUMN id OPTIONS(force_null 'true');`, 0, false},
		{"IF EXISTS", ordinary + `ALTER FOREIGN TABLE IF EXISTS public.child ALTER COLUMN txt OPTIONS(force_null 'true');`, 0, false},
		{"late after consumer", ordinary + `SELECT 1; ` + completion, 0, false},
		{"already configured local", `CREATE FOREIGN TABLE public.child(id INT,txt TEXT OPTIONS(force_null 'true')) SERVER files; ` + completion, 0, false},
		{"inert placeholder", `CREATE FOREIGN TABLE public.child(id INT,` + hole + ` pg_catalog.int4) SERVER files; ALTER FOREIGN TABLE public.child ALTER COLUMN ` + hole + ` OPTIONS(force_null 'true');`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statements, err := parser.Parse(tc.sql)
			if err != nil { t.Fatal("unit syntax", err) }
			original, err := parser.Parse(tc.sql)
			if err != nil { t.Fatal(err) }
			pairs, err := PairAttributeConstructors(statements)
			if err != nil { t.Fatal(err) }
			want := 0
			if tc.number != 0 { want = 1 }
			if len(pairs.ForeignOptions) != want { t.Fatal("foreign option pairing", len(pairs.ForeignOptions), want) }
			if tc.noHistory && (len(pairs.Adds) != 0 || len(pairs.Drops) != 0 || len(pairs.Detaches) != 0 || len(pairs.Teardowns) != 0) { t.Fatal("completion-only partition metadata gained history authority") }
			for statement, pair := range pairs.ForeignOptions {
				if ForeignAttributeOptions(statement) != &statement.TableActions[0] || pair.Target != (AttributeDropTarget{"public", "child"}) || pair.Number != tc.number || pair.Column != "txt" { t.Fatal("completion lost original action or stable slot", pair) }
			}
			if !reflect.DeepEqual(statements, original) { t.Fatal("option pairing mutated source AST") }
		})
	}
}
