package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"stuntgres/internal/sqltype"
)

func queryLimitBound(t *testing.T,e *Engine,value int64) BoundExpr {
	t.Helper()
	typ,_ := e.CatalogRead().LookupType(sqltype.Int8)
	v,err := sqltype.Integer(typ,value)
	if err != nil { t.Fatal(err) }
	return queryProbe(typ.Usage(),func(*EvalContext)(sqltype.Value,error){ return v,nil })
}

func TestQueryLimitDemand(t *testing.T) {
	for _,count := range []int64{0,1} {
		t.Run(string(rune('0'+count)),func(t *testing.T) {
			e,query,source := queryCoreFixture(t,"SELECT id FROM t WHERE f")
			project := query.Root.(*projectNode)
			filter := project.child.(*filterNode)
			boolType,_ := e.CatalogRead().LookupType(sqltype.Bool)
			calls := 0
			sentinel := errors.New("unneeded predicate")
			filter.predicate = queryProbe(boolType.Usage(),func(c *EvalContext)(sqltype.Value,error){ calls++;if calls>1{return sqltype.Value{},sentinel};return sqltype.Boolean(boolType,true),nil })
			project.child = &limitNode{querySchema:filter.Schema(),child:filter,count:queryLimitBound(t,e,count)}
			rows,n,err := finalizeQuery(context.Background(),project,source,nil)
			if err != nil || n!=int(count) || calls!=int(count) || rows==nil { t.Fatal(rows,n,err,calls) }
			defer rows.Close()
			if !reflect.DeepEqual(rows.Schema(),project.Schema()) { t.Fatal("limit changed schema") }
		})
	}
	t.Run("nested-loop stops after first accepted pair",func(t *testing.T) {
		e,query,source := queryCoreFixture(t,"SELECT id FROM t")
		project := query.Root.(*projectNode)
		left := project.child
		right := &sourceNode{occurrence:1}
		inputs := &querySource{sources:[]*querySource{source,{anonymous:[][]sqltype.Value{{}}}}}
		boolType,_ := e.CatalogRead().LookupType(sqltype.Bool)
		calls := 0
		join := &joinNode{querySchema:left.Schema(),left:left,right:right,leftWidth:len(left.Schema()),predicate:queryProbe(boolType.Usage(),func(*EvalContext)(sqltype.Value,error){calls++;if calls>1{return sqltype.Value{},errors.New("tail pair evaluated")};return sqltype.Boolean(boolType,true),nil})}
		project.child = &limitNode{querySchema:join.Schema(),child:join,count:queryLimitBound(t,e,1)}
		rows,n,err := finalizeQuery(context.Background(),project,inputs,nil)
		if err != nil || n!=1 || calls!=1 { t.Fatal(n,err,calls) }
		rows.Close()
	})
}

func TestQueryLimitBoundsOnce(t *testing.T) {
	e,query,source := queryCoreFixture(t,"SELECT id FROM t")
	project := query.Root.(*projectNode)
	typ,_ := e.CatalogRead().LookupType(sqltype.Int8)
	var calls []string
	probe := func(name string,n int64) BoundExpr {
		v,_ := sqltype.Integer(typ,n)
		return queryProbe(typ.Usage(),func(*EvalContext)(sqltype.Value,error){calls=append(calls,name);return v,nil})
	}
	project.child = &limitNode{querySchema:project.child.Schema(),child:project.child,offset:probe("offset",1),count:probe("limit",2)}
	rows,n,err := finalizeQuery(context.Background(),project,source,nil)
	if err != nil || n!=2 || !reflect.DeepEqual(calls,[]string{"offset","limit"}) { t.Fatal(n,err,calls) }
	defer rows.Close()
	values := make([]sqltype.Value,1)
	for _,want := range []int64{2,3} {
		present,err := rows.Next(context.Background(),values)
		got,_ := values[0].Integer()
		if !present || err!=nil || got!=want { t.Fatal(present,err,got) }
	}
	if len(calls)!=2 { t.Fatal("public Next evaluated bounds") }
}

func TestQueryLimitCancellation(t *testing.T) {
	for _,phase := range []string{"bound","rejected filter","offset","projection","prepublication"} {
		t.Run(phase,func(t *testing.T) {
			e,query,source := queryCoreFixture(t,"SELECT id FROM t WHERE f")
			ctx,cancel := context.WithCancel(context.Background())
			defer cancel()
			project := query.Root.(*projectNode)
			filter := project.child.(*filterNode)
			limit := &limitNode{querySchema:filter.Schema(),child:filter,count:queryLimitBound(t,e,1)}
			project.child = limit
			calls := 0
			if phase=="bound" {
				limit.count = queryProbe(limit.count.ResultUsage(),func(*EvalContext)(sqltype.Value,error){cancel();return sqltype.Value{},context.Canceled})
			} else if phase=="rejected filter" {
				bt,_ := e.CatalogRead().LookupType(sqltype.Bool)
				filter.predicate = queryProbe(bt.Usage(),func(*EvalContext)(sqltype.Value,error){calls++;cancel();return sqltype.Boolean(bt,false),nil})
			} else {
				original := project.expressions[0]
				expr := queryProbe(original.ResultUsage(),func(c *EvalContext)(sqltype.Value,error){calls++;if phase!="prepublication" || calls==2 { cancel() };return c.Row[0],nil})
				if phase=="offset" {
					limit.offset = queryLimitBound(t,e,1)
					limit.child = &evaluationNode{querySchema:filter.Schema(),child:filter,expressions:[]BoundExpr{expr}}
				} else {
					if phase=="prepublication" { limit.count=queryLimitBound(t,e,2) }
					project.expressions[0] = expr
				}
			}
			rows,n,err := finalizeQuery(ctx,project,source,nil)
			if rows!=nil || n!=0 || err!=context.Canceled || source.view!=nil { t.Fatal(rows,n,err,source.view,calls) }
		})
	}
}

func TestQueryLimitMetadata(t *testing.T) {
	for _,suffix := range []string{"LIMIT 0","OFFSET 99","OFFSET 1 LIMIT 2"} {
		_,query,source := queryCoreFixture(t,"SELECT id,v FROM t "+suffix)
		want := append([]ColumnDescriptor(nil),query.Root.Schema()...)
		rows,_,err := finalizeQuery(context.Background(),query.Root,source,nil)
		if err!=nil || !reflect.DeepEqual(rows.Schema(),want) { t.Fatal(rows,err,want) }
		rows.Close()
		if !reflect.DeepEqual(rows.Schema(),want) { t.Fatal("close changed schema") }
	}
	e := New()
	run(t,e,"CREATE TABLE t(id INT)")
	run(t,e,"INSERT INTO t VALUES(1),(2),(3)")
	r := run(t,e,"SELECT FROM t LIMIT 2")
	if r.Count!=2 || len(r.Rows.Schema())!=0 { t.Fatal(r.Count,r.Rows.Schema()) }
	defer r.Rows.Close()
	for range 2 { present,err := r.Rows.Next(context.Background(),nil);if !present || err!=nil { t.Fatal(present,err) } }
	present,err := r.Rows.Next(context.Background(),nil)
	if present || err!=nil { t.Fatal(present,err) }
}

func TestQueryLimitACL(t *testing.T) {
	e := New()
	op,done := roleOperation(t,e,"postgres")
	defer done()
	roleExecute(t,e,op,"CREATE USER limit_reader")
	roleExecute(t,e,op,"CREATE TABLE acl_t(id INT,secret INT)")
	roleExecute(t,e,op,"INSERT INTO acl_t VALUES(1,9),(2,8)")
	column := roleTableRef(t,e,"acl_t")
	column.Address.SubID = 1
	reader,_ := e.Roles().Lookup("limit_reader")
	entry := ACLEntry{Grantor:op.RoleState().CurrentUser,Grantee:ACLGrantee{Role:reader.Ref},Privileges:SelectPrivilege}
	if err:=e.SetObjectACL(context.Background(),column,ACLValue{Explicit:true,Entries:[]ACLEntry{entry}});err!=nil { t.Fatal(err) }
	roleExecute(t,e,op,"SET SESSION AUTHORIZATION limit_reader")
	for _,q := range []string{"SELECT secret FROM acl_t LIMIT 0","SELECT id FROM acl_t ORDER BY secret LIMIT 0","SELECT secret FROM acl_t OFFSET 99","SELECT secret FROM acl_t WHERE FALSE LIMIT 1"} {
		roleError(t,e,op,q,"42501","permission denied for table acl_t","")
	}
	r := roleExecute(t,e,op,"SELECT id FROM acl_t LIMIT 0")
	if r.Rows==nil || r.Count!=0 { t.Fatal(r) }
	r.Rows.Close()
}
