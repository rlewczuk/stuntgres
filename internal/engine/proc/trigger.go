package proc

import (
	"fmt"
	"stuntgres/internal/parser"
)

const TriggerSlotCount = 12

var triggerNames = [TriggerSlotCount]string{"old", "new", "tg_name", "tg_when", "tg_level", "tg_op", "tg_relid", "tg_table_name", "tg_table_schema", "tg_relname", "tg_nargs", "tg_argv"}
var triggerTypes = [TriggerSlotCount]uint32{0, 0, 19, 25, 25, 25, 26, 19, 19, 19, 23, 1009}

// TriggerVariables resolves engine-created slots in pg_catalog, independently
// of caller search_path. User-written declarations keep their ordinary resolver.
func TriggerVariables(resolve PLTypeResolver) ([]PLVariable, error) {
	variables := make([]PLVariable, TriggerSlotCount)
	pgCatalog := parser.Identifier{Text: "pg_catalog"}
	for i := range variables {
		v := PLVariable{Name: triggerNames[i]}
		if i < 2 {
			v.Record = true
		} else {
			name := "text"
			switch i {
			case 2, 7, 8, 9:
				name = "name"
			case 6:
				name = "oid"
			case 10:
				name = "int4"
			}
			raw := parser.PLType{Type: parser.TypeName{Name: name, Schema: &pgCatalog}}
			if i == 11 {
				raw.Type.ArrayBounds = []int32{-1}
			}
			usage, record, err := resolve(raw)
			if err != nil {
				return nil, err
			}
			if record {
				return nil, fmt.Errorf("stuntgres: trigger scalar resolved as record")
			}
			v.Usage = usage
		}
		variables[i] = v
	}
	return variables, nil
}
