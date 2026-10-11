package fixture

func (s Step) WithIndexPropertyArgs(args ...any) Step { s.Args = args; return s }
