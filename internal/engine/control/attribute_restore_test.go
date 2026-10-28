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
