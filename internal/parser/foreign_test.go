package parser

import "testing"

func TestFDWParser(t *testing.T) {
	for _, q := range []string{
		"CREATE FOREIGN DATA WRAPPER w NO HANDLER NO VALIDATOR OPTIONS(a '1',b '2')",
		"CREATE FOREIGN DATA WRAPPER w HANDLER public.h VALIDATOR public.v",
		"ALTER FOREIGN DATA WRAPPER w NO HANDLER VALIDATOR v OPTIONS(SET a '2',DROP b,ADD c '3')",
		"ALTER FOREIGN DATA WRAPPER w RENAME TO other",
		"ALTER FOREIGN DATA WRAPPER w OWNER TO CURRENT_USER",
		"CREATE SERVER IF NOT EXISTS s TYPE 'sql' VERSION '' FOREIGN DATA WRAPPER w OPTIONS(a '1')",
		"ALTER SERVER s VERSION NULL OPTIONS(SET a '2',DROP b)",
		"ALTER SERVER s VERSION 'next'",
		"ALTER SERVER s OWNER TO owner_role",
		"ALTER SERVER s RENAME TO other",
		"CREATE USER MAPPING IF NOT EXISTS FOR PUBLIC SERVER s OPTIONS(a '1')",
		"CREATE USER MAPPING FOR USER SERVER s",
		"CREATE USER MAPPING FOR CURRENT_USER SERVER s OPTIONS(user 'postgres')",
		"ALTER USER MAPPING FOR CURRENT_USER SERVER s OPTIONS(SET user 'reader')",
		"ALTER USER MAPPING FOR CURRENT_USER SERVER s OPTIONS(DROP user)",
		"ALTER USER MAPPING FOR SESSION_USER SERVER s OPTIONS(SET a '2')",
		"DROP USER MAPPING IF EXISTS FOR CURRENT_ROLE SERVER s",
		"CREATE FOREIGN TABLE IF NOT EXISTS public.f(id integer OPTIONS(remote 'key') NOT NULL,label text CHECK(label<>'bad')) SERVER s OPTIONS(remote 'data')",
		"ALTER FOREIGN TABLE f OPTIONS(SET a '2'),ALTER COLUMN id OPTIONS(DROP absent)",
		"ALTER FOREIGN TABLE f ADD COLUMN note text,ALTER COLUMN id SET NOT NULL",
		"ALTER FOREIGN TABLE f ADD COLUMN note text OPTIONS(column_name 'remote') NOT NULL",
		"ALTER FOREIGN TABLE f ADD COLUMN IF NOT EXISTS note text OPTIONS(column_name 'remote')",
		"ALTER FOREIGN TABLE f ALTER COLUMN id SET DEFAULT 1,ALTER COLUMN note DROP DEFAULT",
		"ALTER FOREIGN TABLE f RENAME COLUMN id TO key",
		"ALTER FOREIGN TABLE f RENAME TO other",
		"ALTER FOREIGN TABLE f SET SCHEMA dest",
		"ALTER FOREIGN TABLE f OWNER TO CURRENT_USER",
		"DROP FOREIGN TABLE IF EXISTS f,g CASCADE",
		"DROP FOREIGN DATA WRAPPER IF EXISTS w,x RESTRICT",
		"DROP SERVER IF EXISTS s,t CASCADE",
		"IMPORT FOREIGN SCHEMA remote LIMIT TO(a,c) FROM SERVER s INTO local_schema OPTIONS(a '1')",
		"IMPORT FOREIGN SCHEMA remote EXCEPT(b) FROM SERVER s INTO local_schema",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatalf("%s: statements=%#v err=%v", q, statements, err)
		}
		if _, ok := statements[0].(*ForeignDDL); !ok {
			t.Fatalf("%s parsed as %T", q, statements[0])
		}
	}
	statements, err := Parse("CREATE FOREIGN TABLE f(id integer OPTIONS(a 'column')) SERVER s OPTIONS(a 'table')")
	if err != nil {
		t.Fatal(err)
	}
	f := statements[0].(*ForeignDDL)
	if f.Server.Text != "s" || len(f.Table.Elements) != 1 || len(f.Options) != 1 || f.Options[0].Value != "table" || len(f.ColumnOptions["id"]) != 1 || f.ColumnOptions["id"][0].Value != "column" {
		t.Fatalf("lost foreign definition: %#v", f)
	}
	for _, q := range []string{
		"CREATE FOREIGN DATA WRAPPER w NO HANDLER trailing",
		"CREATE FOREIGN DATA WRAPPER w HANDLER h NO HANDLER",
		"CREATE FOREIGN TABLE f(id integer) SERVER s OPTIONS(a '1') trailing",
		"DROP USER MAPPING FOR PUBLIC SERVER s CASCADE",
		"CREATE USER MAPPING FOR SESSION_USER SERVER s",
		"ALTER FOREIGN TABLE f OPTIONS(DROP a 'value')",
		"ALTER TABLE child ADD COLUMN note text OPTIONS(column_name 'remote')",
		"ALTER TABLE child ADD COLUMN IF NOT EXISTS note text OPTIONS(column_name 'remote')",
		"ALTER FOREIGN TABLE f RENAME CONSTRAINT old TO renamed",
	} {
		if _, err := Parse(q); err == nil {
			t.Fatalf("invalid foreign input accepted: %s", q)
		}
	}
	// New unreserved tokens must remain ordinary identifiers, and FOREIGN KEY
	// must keep using the preexisting constraint production.
	for _, q := range []string{
		"SELECT server,mapping,handler,validator,import FROM source",
		"CREATE TABLE server(handler integer,validator text)",
		"CREATE TABLE child(id integer REFERENCES parent(id),FOREIGN KEY(id) REFERENCES parent(id))",
		"ALTER TABLE child ADD COLUMN note text",
		"ALTER TABLE child ADD COLUMN IF NOT EXISTS note text",
		"GRANT USAGE ON FOREIGN SERVER s TO reader",
		"GRANT USAGE ON FOREIGN DATA WRAPPER w TO reader",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatalf("existing/contextual form %s: %v", q, err)
		}
	}
}
