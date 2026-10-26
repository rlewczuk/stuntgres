package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePseudoTypesInvocation(t *testing.T) {
	routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		routineExec(t,p,b,c,
			"CREATE FUNCTION pseudo_id(anyelement) RETURNS anyelement LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION pl_id(x anyelement) RETURNS anyelement LANGUAGE plpgsql AS $$DECLARE y x%TYPE; BEGIN y := x; RETURN y; END$$",
			"CREATE FUNCTION pl_zero(x anyelement) RETURNS anyelement LANGUAGE plpgsql AS $$DECLARE r ALIAS FOR $0; y r%TYPE; BEGIN IF r IS NOT NULL THEN RAISE EXCEPTION 'result not initialized'; END IF; y := x; r := y; RETURN r; END$$",
			"CREATE FUNCTION pl_out(IN x anyelement,OUT y anyelement) LANGUAGE plpgsql AS $$BEGIN y := x; END$$",
			"CREATE FUNCTION pl_sum(a anycompatible,b anycompatible,c anycompatible) RETURNS anycompatible LANGUAGE plpgsql AS $$BEGIN RETURN a+b+c; END$$",
			"CREATE FUNCTION pseudo_strict(anyelement) RETURNS anyelement LANGUAGE plpgsql STRICT AS $$BEGIN RAISE EXCEPTION 'body executed'; END$$")
		for _, name := range []string{"pseudo_id","pl_id","pl_zero","pl_out"} {
			for _, tc := range []struct{expression,typ string; value any}{{"7","INT4",int64(7)},{"'x'::text","TEXT","x"},{"9","INT4",int64(9)}} {
				routineValue(t,p,b,c,"SELECT "+name+"("+tc.expression+") AS v","v",tc.typ,tc.value)
			}
			q := "SELECT "+name+"($1::integer) AS v"
			stmt,err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }
			for _, n := range []int64{8,9} {
				step := fixture.Select(q,[]string{"v"},[]string{"INT4"},[][]any{{n}},n)
				step.CheckMetadata = routineScalarMetadata(b,"v","INT4",false,false)
				fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},b.Recorder,step)
				routineValue(t,p,b,c,"SELECT "+name+"('x'::text) AS v","v","TEXT","x")
			}
			if err := stmt.Close(); err != nil { t.Fatal(err) }
		}
		routineValue(t,p,b,c,"SELECT pseudo_id(pseudo_id(7)) AS v","v","INT4",int64(7))
		routineValue(t,p,b,c,"SELECT pl_sum(1,2,4.7::numeric) AS v","v","NUMERIC","7.7")
		routineValue(t,p,b,c,"SELECT pseudo_strict(NULL::integer) AS v","v","INT4",nil)
		// Native results are eager and detached; pq rows stream on their owner.
		// Use a second connection for the intervening specialization on pq.
		other := c
		if b.PG {
			var err error
			other,err = b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer other.Close()
		}
		held,err := c.QueryContext(p.Ctx,"SELECT pseudo_id(7) AS v"); if err != nil { t.Fatal(err) }; defer held.Close()
		routineValue(t,p,b,other,"SELECT pseudo_id('x'::text) AS v","v","TEXT","x")
		var value int64
		if !held.Next() { t.Fatal(held.Err()) }; if err := held.Scan(&value); err != nil || value != 7 { t.Fatal(value,err) }
		if held.Next() || held.Err() != nil { t.Fatal(held.Err()) }
	})
}

func TestReferencePseudoTypesRecord(t *testing.T) {
	routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		routineExec(t,p,b,c,
			"CREATE FUNCTION pseudo_rec() RETURNS record LANGUAGE SQL AS $$SELECT 1 AS a,'x'::text AS b$$",
			"CREATE FUNCTION pseudo_row() RETURNS record LANGUAGE SQL AS $$SELECT ROW(1,'x'::text)$$",
			"CREATE FUNCTION pseudo_empty() RETURNS record LANGUAGE SQL AS $$SELECT 1 AS a,'x'::text AS b WHERE false$$",
			"CREATE FUNCTION pl_rec(x record) RETURNS record LANGUAGE plpgsql AS $$BEGIN RETURN x; END$$",
			"CREATE FUNCTION pl_badrec() RETURNS record LANGUAGE plpgsql AS $$BEGIN RETURN 1; END$$",
			"CREATE FUNCTION pl_field(x record) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN x.a; END$$",
			"CREATE FUNCTION pl_unassigned() RETURNS integer LANGUAGE plpgsql AS $$DECLARE r record; BEGIN RETURN r.a; END$$",
			"CREATE TYPE pseudo_pair AS (a integer,b text)",
			"CREATE TYPE pseudo_other AS (a text,b integer,c boolean)")
		for _, q := range []string{"SELECT pseudo_rec() AS v","SELECT pseudo_row() AS v","SELECT pl_rec(ROW(1,'x'::text)) AS v","SELECT pl_rec(ROW(1,'x')::pseudo_pair) AS v"} {
			routineValue(t,p,b,c,q,"v","RECORD","(1,x)")
		}
		routineValue(t,p,b,c,"SELECT pl_rec(ROW('y',2,true)::pseudo_other) AS v","v","RECORD","(y,2,t)")
		routineValue(t,p,b,c,"SELECT pl_rec(NULL::pseudo_pair) AS v","v","RECORD",nil)
		routineValue(t,p,b,c,"SELECT pseudo_empty() AS v","v","RECORD",nil)
		routineValue(t,p,b,c,"SELECT pl_field(NULL::pseudo_pair) AS v","v","INT4",nil)
		routineValue(t,p,b,c,"SELECT pl_rec(pl_rec(ROW(1,'x'::text))) AS v","v","RECORD","(1,x)")
		for _, prepared := range []bool{false,true} {
			step := fixture.Select("SELECT * FROM pseudo_rec() AS t(a integer,b text)",[]string{"a","b"},[]string{"INT4","TEXT"},[][]any{{int64(1),"x"}})
			if prepared { step = fixture.Prepared(step) }; fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
		}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT * FROM pseudo_rec() AS t(a text,b text)",[]string{"a","b"},[]string{"TEXT","TEXT"},[][]any{{"1","x"}}))
		pseudoError(t,p,b,c,"SELECT * FROM pseudo_rec()",fixture.Diagnostic{Code:"42601",Message:`a column definition list is required for functions returning "record"`,Position:fixture.Position(15)},true)
		for _, tc := range []struct{definitions,detail string}{
			{"a integer","Final statement returns too many columns."},
			{"a integer,b text,c integer","Final statement returns too few columns."},
			{"a integer,b integer","Final statement returns text instead of integer at column 2."},
		} {
			pseudoError(t,p,b,c,"SELECT * FROM pseudo_rec() AS t("+tc.definitions+")",fixture.Diagnostic{Code:"42P13",Message:"return type mismatch in function declared to return record",Detail:tc.detail,Context:`SQL function "pseudo_rec" statement 1`},true)
		}
		pseudoError(t,p,b,c,"SELECT pl_badrec()",fixture.Diagnostic{Code:"42804",Message:"cannot return non-composite value from function returning composite type",Context:"PL/pgSQL function pl_badrec() line 1 at RETURN"},true)
		pseudoError(t,p,b,c,"SELECT pl_unassigned()",fixture.Diagnostic{Code:"55000",Message:`record "r" is not assigned yet`,Detail:"The tuple structure of a not-yet-assigned record is indeterminate.",Context:"PL/pgSQL expression \"r.a\"\nPL/pgSQL function pl_unassigned() line 1 at RETURN"},true)
		pseudoError(t,p,b,c,"CREATE TABLE pseudo_record_table AS SELECT pseudo_rec() AS v",fixture.Diagnostic{Code:"42P16",Message:`column "v" has pseudo-type record`},false)
	})
}

func TestReferencePseudoTypesValues(t *testing.T) {
	routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		routineValue(t,p,b,c,"SELECT 'abc'::cstring AS v","v","CSTRING","abc")
		routineValue(t,p,b,c,"SELECT 'ignored'::void AS v","v","VOID","")
		routineValue(t,p,b,c,"SELECT NULL::void AS v","v","VOID",nil)
		routineValue(t,p,b,c,"SELECT 'x' AS v","v","TEXT","x")
		routineExec(t,p,b,c,"CREATE FUNCTION pseudo_void() RETURNS void LANGUAGE plpgsql AS $$BEGIN END$$")
		routineValue(t,p,b,c,"SELECT pseudo_void() AS v","v","VOID","")
		for _, name := range []string{"anyelement","anynonarray","anycompatible","anycompatiblenonarray",`"any"`} {
			routineValue(t,p,b,c,"SELECT 'x'::"+name+" AS v","v","TEXT","x")
			if b.PG { routineValue(t,p,b,c,"SELECT pg_typeof('x'::"+name+")::text AS v","v","TEXT","unknown") }
		}
		pseudoError(t,p,b,c,"SELECT 'x'::anyenum",fixture.Diagnostic{Code:"42846",Message:"cannot cast type unknown to anyenum",Position:fixture.Position(11)},true)
		for _, name := range []string{"anyarray","anyrange","anymultirange","anycompatiblearray","anycompatiblerange","anycompatiblemultirange","language_handler","fdw_handler","table_am_handler","index_am_handler","tsm_handler","trigger","event_trigger","pg_ddl_command"} {
			pseudoError(t,p,b,c,"SELECT 'x'::"+name,fixture.Diagnostic{Code:"0A000",Message:"cannot accept a value of type "+name,Position:fixture.Position(8)},true)
		}
		for _, name := range []string{"language_handler","fdw_handler","table_am_handler","index_am_handler","tsm_handler","trigger","event_trigger"} {
			pseudoError(t,p,b,c,"SELECT NULL::"+name,fixture.Diagnostic{Code:"0A000",Message:"cannot accept a value of type "+name,Position:fixture.Position(8)},true)
		}
		for _, tc := range []struct{name,typ string}{{"cstring","CSTRING"},{"record","RECORD"},{"pg_ddl_command","PG_DDL_COMMAND"},{"anyarray","ANYARRAY"},{"anyrange","ANYRANGE"},{"anymultirange","ANYMULTIRANGE"},{"anycompatiblearray","ANYCOMPATIBLEARRAY"},{"anycompatiblerange","ANYCOMPATIBLERANGE"},{"anycompatiblemultirange","ANYCOMPATIBLEMULTIRANGE"}} {
			routineValue(t,p,b,c,"SELECT NULL::"+tc.name+" AS v","v",tc.typ,nil)
			if b.PG { routineValue(t,p,b,c,"SELECT pg_typeof(NULL::"+tc.name+")::text AS v","v","TEXT",tc.name) }
		}
		for _, name := range []string{"anyelement","anynonarray","anycompatible","anycompatiblenonarray",`"any"`} {
			routineValue(t,p,b,c,"SELECT NULL::"+name+" AS v","v","TEXT",nil)
			if b.PG { routineValue(t,p,b,c,"SELECT pg_typeof(NULL::"+name+")::text AS v","v","TEXT","unknown") }
		}
		pseudoError(t,p,b,c,"SELECT NULL::anyenum",fixture.Diagnostic{Code:"42846",Message:"cannot cast type unknown to anyenum",Position:fixture.Position(12)},true)
	})
}

func TestReferencePseudoTypesInternal(t *testing.T) {
	routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		pseudoError(t,p,b,c,"SELECT NULL::internal",fixture.Diagnostic{Code:"42846",Message:"cannot cast type unknown to internal",Position:fixture.Position(12)},true)
		pseudoError(t,p,b,c,"SELECT pg_ddl_command_recv(NULL)",fixture.Diagnostic{Code:"42883",Message:"function pg_ddl_command_recv(unknown) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)},true)
		for _, name := range []string{"pg_ddl_command_recv","array_append_support"} {
			step := fixture.ImplicitError("PREPARE pseudo_internal(internal) AS SELECT "+name+"($1)",fixture.Diagnostic{Code:"0A000",Message:`functions accepting type "internal" cannot be called explicitly`,Position:fixture.Position(45)})
			step.ErrorStage = "execute"
			fixture.RunStep(t,p.Ctx,c,b.Recorder,step)
		}
		pseudoError(t,p,b,c,"SELECT internal_in('x')",fixture.Diagnostic{Code:"0A000",Message:`functions returning type "internal" cannot be called explicitly`,Position:fixture.Position(8)},true)
		pseudoError(t,p,b,c,"SELECT 1::internal",fixture.Diagnostic{Code:"42846",Message:"cannot cast type integer to internal",Position:fixture.Position(9)},true)
	})
}

func TestReferencePseudoTypesHandlers(t *testing.T) {
	routineScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		routineExec(t,p,b,c,
			"CREATE FUNCTION pseudo_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
			"CREATE FUNCTION pseudo_event() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$")
		for _, name := range []string{"pseudo_trigger","pseudo_event"} {
			pseudoError(t,p,b,c,"SELECT "+name+"()",fixture.Diagnostic{Code:"0A000",Message:"trigger functions can only be called as triggers",Context:`compilation of PL/pgSQL function "`+name+`" near line 1`},true)
		}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT l.lanname,p.proname FROM pg_language l JOIN pg_proc p ON p.oid=l.lanplcallfoid WHERE l.lanname='plpgsql'",[]string{"lanname","proname"},[]string{"NAME","NAME"},[][]any{{"plpgsql","plpgsql_call_handler"}}))
		routineExec(t,p,b,c,"CREATE TABLE pseudo_trigger_data(v integer)","CREATE FUNCTION pseudo_fire() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v+1; RETURN NEW; END$$","CREATE TRIGGER pseudo_fire BEFORE INSERT ON pseudo_trigger_data FOR EACH ROW EXECUTE FUNCTION pseudo_fire()")
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO pseudo_trigger_data VALUES(7)",1))
		routineValueWithNativeNullability(t,p,b,c,"SELECT v FROM pseudo_trigger_data","v","INT4",int64(8),true,true)
	})
}
