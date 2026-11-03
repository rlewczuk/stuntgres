package core_test

import (
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPGStatBGWriterConstantRejection(t *testing.T) {
	t.Run("aggregate consumers", testBGWriterAggregateRejection)
	e := engine.New()
	view := e.CatalogRead()
	d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatBGWriterOID})
	ref := view.Owner().Domain.Ref(view.Epoch(), d.Address())
	boolean, _ := view.LookupType(sqltype.Bool)
	root := &plan.FilterNode{Child: &plan.SourceNode{Ref: ref}, Predicate: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}}
	leaf := exec.Leaf{Ref: ref, Relation: d, Table: &parser.TableReference{}}
	query := catalogrt.KeepQuery{Root: root, Leaves: []exec.Leaf{leaf}}
	if !query.BGWriterReadSuppressed([]exec.Leaf{leaf}, 0, plan.RowMark{}) {
		t.Fatal("matching query ownership lost direct false proof")
	}
	wrong := leaf
	wrong.Table = &parser.TableReference{}
	if query.BGWriterReadSuppressed([]exec.Leaf{wrong}, 0, plan.RowMark{}) {
		t.Fatal("proof crossed nested table occurrence ownership")
	}
	wrong = leaf
	wrong.Ref.Epoch++
	if query.BGWriterReadSuppressed([]exec.Leaf{wrong}, 0, plan.RowMark{}) || (*catalogrt.KeepQuery)(nil).BGWriterReadSuppressed([]exec.Leaf{leaf}, 0, plan.RowMark{}) {
		t.Fatal("proof crossed query/epoch ownership")
	}
	for _, retained := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "retained"}[retained], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "SET stats_fetch_consistency=snapshot", "BEGIN")
			defer statsFunctionExec(t, s, "ROLLBACK")
			var writer statistics.Writer
			writer.Shared(&e.Statistics, statistics.BGWriter, statistics.SharedFact{Counters: [6]int64{3, 5, 7}})
			writer.Flush(&e.Statistics, time.Unix(100, 0), true)
			owned := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
			if retained {
				statsFunctionExec(t, s, "SELECT buffers_clean FROM pg_stat_bgwriter")
			}
			full, kinds := owned.FieldByName("full").Bool(), owned.FieldByName("kinds").Uint()
			stamp := owned.FieldByName("stamp")
			wall, ext, loc := stamp.FieldByName("wall").Uint(), stamp.FieldByName("ext").Int(), stamp.FieldByName("loc").Pointer()
			for _, query := range []string{"SELECT buffers_clean FROM pg_stat_bgwriter WHERE FALSE", "SELECT stats_reset FROM pg_stat_bgwriter WHERE FALSE AND buffers_alloc=0"} {
				ast, description := enginetest.PreparedDescription(t, e, s, query)
				result, err := enginetest.PreparedExecute(e, s, ast, &description)
				if err != nil || result.Rows == nil || len(result.Rows.Schema()) != 1 {
					t.Fatal("typed empty false query", query, err)
				}
				var cell [1]sqltype.Value
				if present, err := result.Rows.Next(t.Context(), cell[:]); err != nil || present {
					t.Fatal("false filter published a row", present, err)
				}
				result.Rows.Close()
				if owned.FieldByName("full").Bool() != full || owned.FieldByName("kinds").Uint() != kinds || stamp.FieldByName("wall").Uint() != wall || stamp.FieldByName("ext").Int() != ext || stamp.FieldByName("loc").Pointer() != loc {
					t.Fatal("false projection changed actual Reader ownership")
				}
				if retained && owned.FieldByName("shared").Index(int(statistics.BGWriter)).FieldByName("Counters").Index(0).Int() != 3 {
					t.Fatal("false projection replaced positive retained family")
				}
			}
			admin := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, admin, "CREATE ROLE bgwriter_false_reader", "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() FROM PUBLIC")
			statsFunctionExec(t, s, "SET ROLE bgwriter_false_reader", "SAVEPOINT false_denied")
			ast, description := enginetest.PreparedDescription(t, e, s, "SELECT buffers_clean FROM pg_stat_bgwriter WHERE FALSE")
			result, err := enginetest.PreparedExecute(e, s, ast, &description)
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("revoked false target published rows")
			}
			enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function pg_stat_get_bgwriter_buf_written_clean"})
			if owned.FieldByName("full").Bool() != full || owned.FieldByName("kinds").Uint() != kinds || stamp.FieldByName("wall").Uint() != wall || stamp.FieldByName("ext").Int() != ext || stamp.FieldByName("loc").Pointer() != loc {
				t.Fatal("denied false target initialized or cleared Reader")
			}
			statsFunctionExec(t, s, "ROLLBACK TO SAVEPOINT false_denied")
			resetAST, resetDescription := enginetest.PreparedDescription(t, e, s, "SELECT stats_reset FROM pg_stat_bgwriter WHERE FALSE")
			resetResult, err := enginetest.PreparedExecute(e, s, resetAST, &resetDescription)
			if err != nil || resetResult.Rows == nil || len(resetResult.Rows.Schema()) != 1 || resetResult.Rows.Schema()[0].Usage.Type.ID() != sqltype.Timestamptz {
				t.Fatal("unrelated false reset projection schema", err)
			}
			defer resetResult.Rows.Close()
			var cell [1]sqltype.Value
			if present, err := resetResult.Rows.Next(t.Context(), cell[:]); err != nil || present {
				t.Fatal("unrelated false reset projection published a row", present, err)
			}
			resetResult.Rows.Close()
			if owned.FieldByName("full").Bool() != full || owned.FieldByName("kinds").Uint() != kinds || stamp.FieldByName("wall").Uint() != wall || stamp.FieldByName("ext").Int() != ext || stamp.FieldByName("loc").Pointer() != loc {
				t.Fatal("unrelated revoked getter changed false-query Reader")
			}
		})
	}
}
