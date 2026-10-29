package pgcatalog

import (
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

const PreparedStatementsOID ids.OID = 16250

// Session admission owns the registry during this synchronous capture. Rows
// retain only immutable datums, never borrowed signature slices or authority.
func capturePGPreparedStatements(in Input) ([][]sqltype.Value, error) {
 ctx, d := in.Context, in.Source
 if err := ctx.Err(); err != nil { return nil, err }
 rows := make([][]sqltype.Value, 0, in.Runtime.PreparedStatementCount())
 err := in.Runtime.VisitPreparedStatements(ctx, func(f PreparedStatementFact) error {
  if err := ctx.Err(); err != nil { return err }
  if f.Name == "" { return nil }
  created, err := sqltype.TimestampFromTime(d.Columns[2].Usage.Type, f.Created)
  if err != nil { return err }
  parameters, err := preparedRegtypes(d.Columns[3].Usage.Type, f.ParameterCount, func(i int) uint32 {
   if i < len(f.ParameterTypes) && f.ParameterTypes[i].Type != nil { return uint32(f.ParameterTypes[i].Type.ID()) }
   if i < len(f.ParameterOIDs) { return f.ParameterOIDs[i] }
   return 0
  })
  if err != nil { return err }
  results := sqltype.Null(d.Columns[4].Usage.Type)
  if f.HasResults {
   results, err = preparedRegtypes(d.Columns[4].Usage.Type, len(f.Results), func(i int) uint32 { return uint32(f.Results[i].Usage.Type.ID()) })
   if err != nil { return err }
  }
  rows = append(rows, []sqltype.Value{
   sqltype.String(d.Columns[0].Usage.Type, f.Name),
   sqltype.String(d.Columns[1].Usage.Type, f.Statement), created, parameters, results,
   sqltype.Boolean(d.Columns[5].Usage.Type, f.FromSQL),
   sqltype.Null(d.Columns[6].Usage.Type), sqltype.Null(d.Columns[7].Usage.Type),
  })
  return nil
 })
 if err != nil { return nil, err }
 if err := ctx.Err(); err != nil { return nil, err }
 return rows, nil
}

func preparedRegtypes(d *sqltype.Descriptor, count int, oid func(int) uint32) (sqltype.Value, error) {
 if count == 0 { return sqltype.OwnedArrayValue(d, nil, nil) }
 elements := make([]sqltype.Value, count)
 for i := range elements {
  value, err := sqltype.RegtypeValue(d.Element(), oid(i))
  if err != nil { return sqltype.Value{}, err }
  elements[i] = value
 }
 return sqltype.OwnedArrayValue(d, []sqltype.ArrayDimension{{Lower: 1, Length: int32(count)}}, elements)
}
