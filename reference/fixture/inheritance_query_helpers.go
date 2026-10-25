package fixture

func InheritanceQueryDiamond(run func(Step)) {
	run(Exec("CREATE TABLE p(a INT,x TEXT)", 0))
	run(Exec("CREATE TABLE q(y BOOL,a INT)", 0))
	run(Exec("CREATE TABLE l() INHERITS(p)", 0))
	run(Exec("CREATE TABLE r() INHERITS(p)", 0))
	run(Exec("CREATE TABLE c(z BIGINT) INHERITS(q,l,r)", 0))
	run(Exec("INSERT INTO p VALUES(7,'same'),(7,'same')", 2))
	run(Exec("INSERT INTO l VALUES(7,'same')", 1))
	run(Exec("INSERT INTO c VALUES(true,7,'same',9),(true,7,'same',10)", 2))
}

func InheritanceQueryFive() [][]any {
	return [][]any{{int64(7), "same"}, {int64(7), "same"}, {int64(7), "same"}, {int64(7), "same"}, {int64(7), "same"}}
}
