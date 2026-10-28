package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func storageMetadata(relation string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT attname,attstorage::text,attcompression::text FROM pg_attribute WHERE attrelid='"+relation+"'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attstorage", "attcompression"}, []string{"NAME", "TEXT", "TEXT"}, rows)
}

func storageRows() fixture.Step {
	return fixture.Select("SELECT i,t,n::text FROM storage_case ORDER BY i", []string{"i", "t", "n"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(7), "payload", "12"}})
}

func storageInitial() fixture.Step {
	return storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "x", ""}, []any{"n", "m", ""})
}

func storageError(query, code, message string) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message})
}

func TestReferenceAlterStorageMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE storage_case(i integer NOT NULL,t text,n numeric)", 0))
	p.Step(t, fixture.Exec("INSERT INTO storage_case VALUES(7,'payload',12)", 1))
	p.Step(t, storageInitial())
	for i, mode := range []string{"PLAIN", "EXTERNAL", "EXTENDED", "MAIN", "DEFAULT", "DEFAULT", `"eXtErNaL"`, `"DEFAULT"`} {
		want := []string{"p", "e", "x", "m", "x", "x", "e", "x"}[i]
		s := fixture.Exec("ALTER TABLE public.storage_case ALTER COLUMN t SET STORAGE "+mode, 0)
		s.Prepared = i%2 == 1
		p.Step(t, s)
		p.Step(t, storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", want, ""}, []any{"n", "m", ""}))
		p.Step(t, storageRows())
	}
	for _, action := range []string{"n SET STORAGE EXTERNAL", "n SET STORAGE DEFAULT", "i SET STORAGE PLAIN", "i SET STORAGE DEFAULT"} {
		p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE storage_case ALTER "+action, 0)))
	}
	p.Step(t, storageInitial())
	s := fixture.Select("SELECT i,t,n FROM storage_case", []string{"i", "t", "n"}, []string{"INT4", "TEXT", "NUMERIC"}, [][]any{{int64(7), "payload", "12"}})
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		ct, err := rows.ColumnTypes()
		if err != nil { t.Fatal(err) }
		for i, nullable := range []bool{false, true, true} {
			if got, known := ct[i].Nullable(); known && got != nullable { t.Fatalf("column %d nullability = %v", i, got) }
		}
	}
	p.Step(t, s)
	p.Step(t, fixture.Exec(`CREATE TABLE "Storage Quoted"("t""\name" text)`, 0))
	p.Step(t, fixture.Prepared(fixture.Exec(`ALTER TABLE "public"."Storage Quoted" ALTER "t""\name" SET STORAGE "eXtErNaL"`, 0)))
	p.Step(t, storageMetadata(`"Storage Quoted"`, []any{"t\"\\name", "e", ""}))
}

func TestReferenceAlterStorageCompression(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE storage_case(i integer,t text,n numeric)", 0))
	for i, tc := range []struct{ action, storage, compression string }{
		{"COMPRESSION pglz", "x", "p"}, {"STORAGE PLAIN", "p", "p"},
		{"COMPRESSION default", "p", ""}, {"COMPRESSION pglz", "p", "p"},
		{"STORAGE EXTERNAL", "e", "p"}, {"COMPRESSION lz4", "e", "l"},
		{"STORAGE DEFAULT", "x", "l"}, {"COMPRESSION DEFAULT", "x", ""},
		{"COMPRESSION PGLZ", "x", "p"}, {`COMPRESSION "pglz"`, "x", "p"},
	} {
		s := fixture.Exec("ALTER TABLE storage_case ALTER t SET "+tc.action, 0)
		s.Prepared = i%2 != 0
		p.Step(t, s)
		p.Step(t, storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", tc.storage, tc.compression}, []any{"n", "m", ""}))
	}
	for _, mode := range []string{"default", `"default"`} {
		p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE storage_case ALTER i SET COMPRESSION "+mode, 0)))
	}
	p.Step(t, fixture.Exec("ALTER TABLE storage_case ALTER n SET COMPRESSION pglz", 0))
	p.Step(t, storageMetadata("storage_case", []any{"i", "p", ""}, []any{"t", "x", "p"}, []any{"n", "m", "p"}))
	p.Step(t, fixture.Exec("CREATE DOMAIN storage_text AS text", 0))
	p.Step(t, fixture.Exec("CREATE DOMAIN storage_int AS integer", 0))
	p.Step(t, fixture.Exec("CREATE TABLE storage_types(u uuid,q tsquery,dt storage_text,di storage_int)", 0))
	p.Step(t, storageMetadata("storage_types", []any{"u", "p", ""}, []any{"q", "p", ""}, []any{"dt", "x", ""}, []any{"di", "p", ""}))
	p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE storage_types ALTER dt SET STORAGE EXTERNAL, ALTER dt SET COMPRESSION pglz, ALTER di SET COMPRESSION default", 0)))
	p.Step(t, storageMetadata("storage_types", []any{"u", "p", ""}, []any{"q", "p", ""}, []any{"dt", "e", "p"}, []any{"di", "p", ""}))
	for _, action := range []string{"STORAGE EXTENDED", "COMPRESSION pglz"} {
		message := "column data type storage_int can only have storage PLAIN"
		if action == "COMPRESSION pglz" { message = "column data type storage_int does not support compression" }
		for _, prepared := range []bool{false, true} {
			s := storageError("ALTER TABLE storage_types ALTER di SET "+action, "0A000", message)
			s.Prepared = prepared
			if prepared { s.ErrorStage = "execute" }
			p.Step(t, s)
			p.Step(t, storageMetadata("storage_types", []any{"u", "p", ""}, []any{"q", "p", ""}, []any{"dt", "e", "p"}, []any{"di", "p", ""}))
		}
	}
}

func TestReferenceAlterStorageDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE storage_case(i integer,t text,n numeric)", 0))
	p.Step(t, fixture.Exec("INSERT INTO storage_case VALUES(7,'payload',12)", 1))
	type failure struct{ action, code, message string }
	cases := []failure{
		{"t SET STORAGE bogus", "22023", `invalid storage type "bogus"`},
		{"i SET STORAGE bogus", "22023", `invalid storage type "bogus"`},
		{"t SET COMPRESSION bogus", "22023", `invalid compression method "bogus"`},
		{`t SET COMPRESSION "PGLZ"`, "22023", `invalid compression method "PGLZ"`},
		{`t SET COMPRESSION "DEFAULT"`, "22023", `invalid compression method "DEFAULT"`},
		{`t SET STORAGE "a""\b"`, "22023", "invalid storage type \"a\"\\b\""},
		{`t SET COMPRESSION "a""\b"`, "22023", "invalid compression method \"a\"\\b\""},
	}
	for _, mode := range []string{"EXTENDED", "EXTERNAL", "MAIN"} {
		cases = append(cases, failure{"i SET STORAGE "+mode, "0A000", "column data type integer can only have storage PLAIN"})
	}
	for _, mode := range []string{"pglz", "lz4", "bogus"} {
		cases = append(cases, failure{"i SET COMPRESSION "+mode, "0A000", "column data type integer does not support compression"})
	}
	for _, property := range []string{"STORAGE PLAIN", "COMPRESSION default"} {
		cases = append(cases, failure{"absent SET "+property, "42703", `column "absent" of relation "storage_case" does not exist`}, failure{"ctid SET "+property, "0A000", `cannot alter system column "ctid"`})
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.action, prepared), func(t *testing.T) {
				s := storageError("ALTER TABLE storage_case ALTER "+tc.action, tc.code, tc.message)
				s.Prepared = prepared
				if prepared { s.ErrorStage = "execute" }
				p.Step(t, s)
				p.Step(t, storageInitial())
				p.Step(t, storageRows())
			})
		}
	}
	p.Step(t, fixture.Exec("CREATE TABLE storage_types(u uuid,q tsquery)", 0))
	for _, tc := range []struct{ column, typ, mode string }{{"u", "uuid", "EXTERNAL"}, {"q", "tsquery", "EXTENDED"}} {
		for _, prepared := range []bool{false, true} {
			for _, property := range []string{"STORAGE "+tc.mode, "COMPRESSION pglz"} {
				message := "column data type "+tc.typ+" can only have storage PLAIN"
				if property == "COMPRESSION pglz" { message = "column data type "+tc.typ+" does not support compression" }
				s := storageError("ALTER TABLE storage_types ALTER "+tc.column+" SET "+property, "0A000", message)
				s.Prepared = prepared
				if prepared { s.ErrorStage = "execute" }
				p.Step(t, s)
				p.Step(t, storageMetadata("storage_types", []any{"u", "p", ""}, []any{"q", "p", ""}))
			}
		}
	}
	for _, tc := range []struct{ property, literal, position string }{{"STORAGE", "'plain'", fixture.Position(46)}, {"COMPRESSION", "'pglz'", fixture.Position(50)}} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("ALTER TABLE storage_case ALTER t SET "+tc.property+" "+tc.literal, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "`+tc.literal+`"`, Position: tc.position})
			s.Prepared = prepared
			if prepared { s.ErrorStage = "prepare" }
			p.Step(t, s)
			p.Step(t, storageInitial())
			p.Step(t, storageRows())
		}
	}
}
