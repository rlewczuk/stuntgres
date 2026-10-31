package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const attrsDefinitionSQL = "SELECT pg_get_functiondef($1::oid) AS definition"

// Independently specified REL_18_6 declarations, with full-text qualification
// grounded by the pinned oracle. Formal references follow the current outer
// routine name; stored function-RTE aliases retain their original spelling.
func attrsDefinitionText(arity int, moved bool) string {
	inputs := "page bytea, rel_oid regclass"
	if arity == 3 {
		inputs += ", do_detoast boolean"
	}
	outputs := "OUT lp smallint, OUT lp_off smallint, OUT lp_flags smallint, OUT lp_len smallint, OUT t_xmin xid, OUT t_xmax xid, OUT t_field3 integer, OUT t_ctid tid, OUT t_infomask2 integer, OUT t_infomask integer, OUT t_hoff smallint, OUT t_bits text, OUT t_oid oid, OUT t_attrs bytea[]"
	target := "heap_part.heap_page_item_attrs"
	if arity == 3 && moved {
		target = "heap_moved.attrs_inner"
	}
	self := "heap_page_item_attrs"
	if arity == 3 && moved {
		self = "attrs_inner"
	}
	alias := "heap_page_item_attrs"
	source := "heap_part.heap_page_item_attrs(" + self + ".page, " + self + ".rel_oid, false)"
	fields := []string{"lp", "lp_off", "lp_flags", "lp_len", "t_xmin", "t_xmax", "t_field3", "t_ctid", "t_infomask2", "t_infomask", "t_hoff", "t_bits", "t_oid", "t_attrs"}
	if moved {
		source = "heap_moved.attrs_inner(" + self + ".page, " + self + ".rel_oid, false)"
	}
	projection := make([]string, len(fields))
	if arity == 3 {
		alias = "heap_page_items"
		split, heap := "heap_part.tuple_data_split", "heap_part.heap_page_items"
		if moved {
			split, heap = "heap_moved.split_six", "heap_moved.heap_rows"
		}
		source = heap + "(" + self + ".page)"
		for i := range 13 {
			projection[i] = alias + "." + fields[i]
		}
		projection[13] = split + "((" + self + ".rel_oid)::oid, heap_page_items.t_data, heap_page_items.t_infomask, heap_page_items.t_infomask2, heap_page_items.t_bits, " + self + ".do_detoast) AS t_attrs"
		fields[13] = "t_data"
	} else {
		for i, field := range fields {
			projection[i] = alias + "." + field
		}
	}
	return "CREATE OR REPLACE FUNCTION " + target + "(" + inputs + ", " + outputs + ")\n RETURNS SETOF record\n LANGUAGE sql\n PARALLEL RESTRICTED\nBEGIN ATOMIC\n SELECT " + strings.Join(projection, ",\n     ") + "\n    FROM " + source + " " + alias + "(" + strings.Join(fields, ", ") + ");\nEND\n"
}

func attrsDefinitionIdentity(arity int, moved bool) string {
	if arity == 3 {
		if moved {
			return "heap_moved.attrs_inner(bytea,regclass,boolean)"
		}
		return "heap_part.heap_page_item_attrs(bytea,regclass,boolean)"
	}
	return "heap_part.heap_page_item_attrs(bytea,regclass)"
}

func attrsDefinitionOID(t *testing.T, p *fixture.Pair, c *sql.Conn, identity string) int64 {
	t.Helper()
	var oid int64
	if err := c.QueryRowContext(p.Ctx, "SELECT '"+identity+"'::regprocedure::bigint").Scan(&oid); err != nil || oid <= 0 {
		t.Fatal("wrapper identity", identity, oid, err)
	}
	return oid
}

func TestReferencePageinspectHeapAttrsFunctionDefinitions(t *testing.T) {
	for _, arity := range []int{2, 3} {
		for _, phase := range []string{"initial", "moved", "detached"} {
			name := "two-"
			if arity == 3 {
				name = "three-"
			}
			t.Run(name+phase, func(t *testing.T) {
				attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("SET search_path TO public,pg_catalog", 0))
					run(fixture.Exec("SET quote_all_identifiers TO off", 0))
					oid := attrsDefinitionOID(t, p, c, attrsDefinitionIdentity(arity, false))
					st, err := c.PrepareContext(p.Ctx, attrsDefinitionSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer st.Close()
					held := fixture.HeldStatement{Executor: c, Statement: st, SQL: attrsDefinitionSQL}
					moved := phase != "initial"
					if moved {
						for _, q := range []string{
							"CREATE SCHEMA heap_moved",
							"ALTER FUNCTION heap_part.heap_page_items(bytea) RENAME TO heap_rows",
							"ALTER FUNCTION heap_part.heap_rows(bytea) SET SCHEMA heap_moved",
							"ALTER FUNCTION heap_part.tuple_data_split(oid,bytea,integer,integer,text,boolean) RENAME TO split_six",
							"ALTER FUNCTION heap_part.split_six(oid,bytea,integer,integer,text,boolean) SET SCHEMA heap_moved",
							"ALTER FUNCTION heap_part.heap_page_item_attrs(bytea,regclass,boolean) RENAME TO attrs_inner",
							"ALTER FUNCTION heap_part.attrs_inner(bytea,regclass,boolean) SET SCHEMA heap_moved",
						} {
							run(fixture.Exec(q, 0))
						}
					}
					if phase == "detached" {
						for _, signature := range []string{"heap_moved.heap_rows(bytea)", "heap_moved.split_six(oid,bytea,integer,integer,text,boolean)", "heap_moved.attrs_inner(bytea,regclass,boolean)", "heap_part.heap_page_item_attrs(bytea,regclass)"} {
							run(fixture.Exec("ALTER EXTENSION pageinspect DROP FUNCTION "+signature, 0))
						}
					}
					want := attrsDefinitionText(arity, moved)
					step := fixture.Select(attrsDefinitionSQL, []string{"definition"}, []string{"TEXT"}, [][]any{{want}}, oid)
					heapBoth(t, p, b, c, step)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
					if b.PG || phase != "detached" {
						return
					}
					dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
					other, destination, recorder := pgcryptoControlDestination(t, p, "attrs_definition_restore")
					if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
					step.Args = []any{attrsDefinitionOID(t, p, destination, attrsDefinitionIdentity(arity, true))}
					fixture.RunStep(t, p.Ctx, destination, recorder, step)
					fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Prepared(step))
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
					step.Args = []any{attrsDefinitionOID(t, p, c, attrsDefinitionIdentity(arity, true))}
					heapBoth(t, p, b, c, step)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
				})
			})
		}
	}
}
