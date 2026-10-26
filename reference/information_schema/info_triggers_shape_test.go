package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoTriggersShape(t *testing.T) {
	itrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		itBuiltinDefinition(t, p, b, c, r)
		empty := itProjection(b.PG, "trigger_schema='it_scope'", nil)
		run(empty)
		run(fixture.Prepared(empty))
		var attributes [][]any
		for i, name := range itrColumns {
			domain, base, collation := "sql_identifier", int64(19), int64(950)
			switch i {
			case 3, 8, 9, 10, 11:
				domain, base = "character_data", 1043
			case 7:
				domain, base, collation = "cardinal_number", 23, 0
			case 16:
				domain, base, collation = "time_stamp", 1184, 0
			}
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", domain, "d", base, int64(-1), false, collation, int64(0), false})
		}
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.triggers'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.triggers'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		itSeed(run)
		all := itSeedRows(database, true)
		for _, s := range []fixture.Step{
			itProjection(b.PG, "trigger_schema='it_scope'", all),
			itProjection(b.PG, "trigger_schema='it_scope' AND FALSE", nil),
			itProjection(b.PG, "trigger_schema='it_scope' AND trigger_name='absent'", nil),
			itProjection(b.PG, "trigger_schema='it_scope' AND created IS NOT NULL", nil),
			itProjection(b.PG, "trigger_schema='it_scope' AND action_condition IS NOT NULL", all[2:4]),
			itProjection(b.PG, "trigger_schema='it_scope' AND action_condition IS NULL", [][]any{all[0], all[1], all[4]}),
		} {
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				run(s)
				named := s
				named.Query = strings.Replace(s.Query, "SELECT *", "SELECT "+strings.Join(itrColumns, ","), 1)
				run(named)
				rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, c, s.Query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				itrMetadata(t, columns, b.PG)
				count := 0
				for rows.Next() {
					cells := make([]any, 17)
					destinations := make([]any, 17)
					for i := range cells {
						destinations[i] = &cells[i]
					}
					if err := rows.Scan(destinations...); err != nil {
						t.Fatal(err)
					}
					for i, cell := range cells {
						if cell == nil {
							continue
						}
						switch itrTypes[i] {
						case "NAME":
							if b.PG {
								if _, ok := cell.([]byte); !ok {
									t.Fatalf("NAME raw %T", cell)
								}
							} else if _, ok := cell.(string); !ok {
								t.Fatalf("NAME raw %T", cell)
							}
						case "VARCHAR":
							if _, ok := cell.(string); !ok {
								t.Fatalf("VARCHAR raw %T", cell)
							}
						case "INT4":
							if _, ok := cell.(int64); !ok {
								t.Fatalf("INT4 raw %T", cell)
							}
						}
					}
					count++
				}
				if err := rows.Err(); err != nil || count != len(s.Rows) {
					t.Fatal(count, err)
				}
				if err := closeQuery(); err != nil {
					t.Fatal(err)
				}
				itrMetadata(t, columns, b.PG)
			}
		}
		nilStep := itProjection(b.PG, "trigger_schema='it_scope' AND trigger_name=$1", nil)
		nilStep.Args = []any{nil}
		run(nilStep)
		run(fixture.Prepared(nilStep))
	})
}
