package reference_test

import (
 "fmt"
 "stuntgres/reference/fixture"
 "testing"
 "time"
)

func TestReferenceTimestampRangeEnvironments(t *testing.T) {
 p := fixture.NewScenario(t)
 for _, b := range p.Backends() {
  conn,err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
  run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,conn,b.Recorder,s) }
  run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v tsrange)",0))
  run(fixture.Exec("SET DateStyle = 'ISO, MDY'",0))
  run(fixture.Exec("SET TIME ZONE 'UTC'",0))
  run(fixture.Exec("INSERT INTO r VALUES (1,'[01/02/2003,01/03/2003)'),(2,'[2004-10-19T10:23:54+02,2004-10-19T11:23:54-08)')",2))
  q := "INSERT INTO r VALUES ($1,'[01/02/2003,01/03/2003)')"
  stmt,err := conn.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
  run(fixture.Exec("SET DateStyle = 'ISO, DMY'",0))
  run(fixture.Exec("SET TIME ZONE '+12:00'",0))
  fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:q},b.Recorder,fixture.Exec(q,1,3))
  run(fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)",1,4,"[01/02/2003,01/03/2003)")))
  // Pinned PG18.0 confirms Postgres/DMY day names and two-digit day spelling.
  for _, tc := range []struct{ style string; bounds [][2]string }{
   {"ISO, DMY",[][2]string{{"2003-01-02 00:00:00","2003-01-03 00:00:00"},{"2004-10-19 10:23:54","2004-10-19 11:23:54"},{"2003-02-01 00:00:00","2003-03-01 00:00:00"}}},
   {"SQL, DMY",[][2]string{{"02/01/2003 00:00:00","03/01/2003 00:00:00"},{"19/10/2004 10:23:54","19/10/2004 11:23:54"},{"01/02/2003 00:00:00","01/03/2003 00:00:00"}}},
   {"German, DMY",[][2]string{{"02.01.2003 00:00:00","03.01.2003 00:00:00"},{"19.10.2004 10:23:54","19.10.2004 11:23:54"},{"01.02.2003 00:00:00","01.03.2003 00:00:00"}}},
   {"Postgres, DMY",[][2]string{{"Thu 02 Jan 00:00:00 2003","Fri 03 Jan 00:00:00 2003"},{"Tue 19 Oct 10:23:54 2004","Tue 19 Oct 11:23:54 2004"},{"Sat 01 Feb 00:00:00 2003","Sat 01 Mar 00:00:00 2003"}}},
  } {
   t.Run(tc.style,func(t *testing.T) {
    run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,conn,b.Recorder,s) }
    run(fixture.Exec("SET DateStyle = '"+tc.style+"'",0))
    format := func(i int) string { return fmt.Sprintf(`["%s","%s")`,tc.bounds[i][0],tc.bounds[i][1]) }
    check := rangeSelect(b,"SELECT k,v FROM r ORDER BY k",[]string{"k","v"},[]string{"INT4","TSRANGE"},[][]any{{int64(1),format(0)},{int64(2),format(1)},{int64(3),format(0)},{int64(4),format(2)}},[]bool{false,true})
    for _, prepared := range []bool{false,true} { s := check; s.Prepared = prepared; run(s) }
    rows,err := conn.QueryContext(p.Ctx,check.Query); if err != nil { t.Fatal(err) }
    defer rows.Close()
    // Session changes on another connection are not a retained-environment
    // witness. Native permits its captured cursor on this same session; pq
    // cannot issue another command while its cursor is outstanding.
    if !b.PG { run(fixture.Exec("SET DateStyle = 'ISO, MDY'",0)) }
    fixture.RangeConsumeHeld(t,rows,check)
   })
  }
 }
}

func timestampRangeScalarText(t *testing.T, value any) string {
 t.Helper()
 switch v := value.(type) {
 case time.Time:
  return v.Format("2006-01-02 15:04:05.999999")
 case string: return v
 case []byte: return string(v)
 default: t.Fatalf("timestamp scalar baseline transport %T",value); return ""
 }
}

func TestReferenceTimestampRangeRelativeCapture(t *testing.T) {
 for _, zone := range []string{"UTC","+12:00"} {
  t.Run(zone,func(t *testing.T) {
   p := fixture.NewScenario(t)
   for _, b := range p.Backends() {
    conn,err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
    run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,conn,b.Recorder,s) }
    run(fixture.Exec("SET DateStyle = 'ISO, MDY'",0)); run(fixture.Exec("SET TIME ZONE '"+zone+"'",0))
    run(fixture.Exec("CREATE TABLE baseline(k integer NOT NULL,v timestamp)",0))
    run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v tsrange)",0))
    tx,err := conn.BeginTx(p.Ctx,nil); if err != nil { t.Fatal(err) }; defer tx.Rollback()
    fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Exec("INSERT INTO baseline VALUES (1,'now'),(2,'today'),(3,'tomorrow'),(4,'yesterday')",4))
    rows,err := tx.QueryContext(p.Ctx,"SELECT v FROM baseline ORDER BY k"); if err != nil { t.Fatal(err) }
    var scalar []string
    for rows.Next() { var v any; if err := rows.Scan(&v); err != nil { rows.Close(); t.Fatal(err) }; scalar = append(scalar,timestampRangeScalarText(t,v)) }
    err = rows.Err(); rows.Close(); if err != nil { t.Fatal(err) }; if len(scalar) != 4 { t.Fatal("scalar baseline",scalar) }
    q := "INSERT INTO r VALUES ($1,'[now,now]')"
    stmt,err := tx.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
    held := fixture.HeldStatement{Executor:tx,Statement:stmt,SQL:q}
    for _, k := range []int{1,2} { fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,1,k)) }
    fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Exec("INSERT INTO r VALUES (3,'[today,tomorrow)'),(4,'[yesterday,today]')",2))
    fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)",1,5,"[today,tomorrow)")))
    singleton := fmt.Sprintf(`["%s","%s"]`,scalar[0],scalar[0])
    day := fmt.Sprintf(`["%s","%s")`,scalar[1],scalar[2])
    yesterday := fmt.Sprintf(`["%s","%s"]`,scalar[3],scalar[1])
    nextZone := "UTC"; if zone == "UTC" { nextZone = "+12:00" }
    fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Exec("SET TIME ZONE '"+nextZone+"'",0))
    fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Exec("INSERT INTO baseline VALUES (5,'now')",1))
    var freshScalar any
    if err := tx.QueryRowContext(p.Ctx,"SELECT v FROM baseline WHERE k=5").Scan(&freshScalar); err != nil { t.Fatal(err) }
    fresh := timestampRangeScalarText(t,freshScalar)
    if fresh == scalar[0] { t.Fatal("timezone change did not distinguish scalar environments",fresh) }
    fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,1,6))
    fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2)",1,7,"[now,now]")))
    freshRange := fmt.Sprintf(`["%s","%s"]`,fresh,fresh)
    fixture.RunStep(t,p.Ctx,tx,b.Recorder,rangeSelect(b,"SELECT k,v FROM r ORDER BY k",[]string{"k","v"},[]string{"INT4","TSRANGE"},[][]any{{int64(1),singleton},{int64(2),singleton},{int64(3),day},{int64(4),yesterday},{int64(5),day},{int64(6),singleton},{int64(7),freshRange}},[]bool{false,true}))
    if err := stmt.Close(); err != nil { t.Fatal(err) }; if err := tx.Commit(); err != nil { t.Fatal(err) }
   }
  })
 }
}
