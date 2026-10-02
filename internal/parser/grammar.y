%{
package parser
%}
%union {
 tok lexeme
 stmt Statement
 col Column
 cols []Column
 typ Type
 nullable bool
 names []string
 rows [][]Value
 row []Value
 value Value
 selectStmt *Select
 pred *Predicate
 order *Order
 desc bool
}
%token <tok> IDENT NUMBER STRING PARAM
%token <tok> CREATE TABLE INSERT INTO VALUES SELECT FROM WHERE ORDER BY ASC DESC NOT NULL TRUE FALSE
%token <tok> INTEGER BIGINT TEXT BOOLEAN UNSUP_TYPE UNSUP_IDENT UNSUP
/* A following '(' denotes an unsupported function, not a completed name list. */
%nonassoc NAME_LIST
%nonassoc '('
%type <stmt> statement create insert select
%type <col> column
%type <cols> columns
%type <typ> type
%type <nullable> nullability
%type <tok> name
%type <names> names insert_columns
%type <rows> rows
%type <row> row values
%type <value> value
%type <selectStmt> projection
%type <pred> where
%type <order> order
%type <desc> direction
%%
script: separators optional_statements ;
separators: /* empty */ | separators ';' ;
optional_statements: /* empty */ | accepted tail ;
tail: /* empty */ | ';' separators optional_statements ;
accepted: statement { sqllex.(*lexer).statements = append(sqllex.(*lexer).statements, $1) } ;
statement: create | insert | select ;
create: CREATE TABLE name '(' columns ')' { $$ = &CreateTable{Name:$3.text, Columns:$5, Pos:$1.pos} }
 | CREATE TABLE name NOT { code:="42601"; if $3.text=="if" { code="0A000" }; sqllex.(*lexer).fail(code,"unsupported CREATE syntax"); return 1 } ;
columns: column { $$ = []Column{$1} } | columns ',' column { $$ = append($1,$3) } ;
column: name type nullability { $$ = Column{Name:$1.text, Type:$2, Nullable:$3, Pos:$1.pos} } ;
type: INTEGER { $$ = Int4 } | BIGINT { $$ = Int8 } | TEXT { $$ = Text } | BOOLEAN { $$ = Bool }
 | UNSUP_TYPE { sqllex.(*lexer).fail("0A000", "unsupported column type"); return 1 } ;
nullability: /* empty */ { $$ = true } | NULL { $$ = true } | NOT NULL { $$ = false } ;
insert: INSERT INTO name insert_columns VALUES rows { $$ = &Insert{Name:$3.text, Columns:$4, Rows:$6, Pos:$1.pos} } ;
insert_columns: /* empty */ { $$ = nil } | '(' names ')' { $$ = $2 } ;
names: name %prec NAME_LIST { $$ = []string{$1.text} } | names ',' name { $$ = append($1,$3.text) } ;
rows: row { $$ = [][]Value{$1} } | rows ',' row { $$ = append($1,$3) } ;
row: '(' values ')' { $$ = $2 } ;
values: value { $$ = []Value{$1} } | values ',' value { $$ = append($1,$3) } ;
value: NULL { $$ = Value{Kind:NullValue, Pos:$1.pos} }
 | TRUE { $$ = Value{Kind:BooleanValue, Boolean:true, Pos:$1.pos} }
 | FALSE { $$ = Value{Kind:BooleanValue, Pos:$1.pos} }
 | STRING { $$ = Value{Kind:StringValue, Text:$1.text, Pos:$1.pos} }
 | PARAM { $$ = Value{Kind:ParameterValue, Parameter:sqllex.(*lexer).parameter($1.text), Pos:$1.pos} }
 | NUMBER { var ok bool; $$, ok = integerValue(sqllex,$1,""); if !ok { return 1 } }
 | '+' NUMBER { var ok bool; $$, ok = integerValue(sqllex,$2,"+"); if !ok { return 1 } }
 | '-' NUMBER { var ok bool; $$, ok = integerValue(sqllex,$2,"-"); if !ok { return 1 } }
 | value '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '*' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value '/' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | name '(' { sqllex.(*lexer).fail("0A000","functions are unsupported"); return 1 } ;
select: SELECT projection FROM name where order { $2.Name=$4.text; $2.Where=$5; $2.Order=$6; $2.Pos=$1.pos; $$=$2 }
 | SELECT projection FROM name name { sqllex.(*lexer).fail("0A000","table aliases are unsupported"); return 1 } ;
projection: '*' { $$ = &Select{Star:true} } | names { $$ = &Select{Columns:$1} }
 | names name { sqllex.(*lexer).fail("0A000","projection aliases are unsupported"); return 1 }
 | names '(' { sqllex.(*lexer).fail("0A000","functions are unsupported"); return 1 }
 | names '+' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | names '-' { sqllex.(*lexer).fail("0A000","expressions are unsupported"); return 1 }
 | value { sqllex.(*lexer).fail("0A000","literal projections are unsupported"); return 1 } ;
where: /* empty */ { $$ = nil } | WHERE name '=' value { $$ = &Predicate{Column:$2.text, Value:$4} } ;
order: /* empty */ { $$ = nil } | ORDER BY name direction { $$ = &Order{Column:$3.text, Desc:$4} }
 | ORDER BY name direction ',' { sqllex.(*lexer).fail("0A000","multiple sort keys are unsupported"); return 1 } ;
direction: /* empty */ { $$ = false } | ASC { $$ = false } | DESC { $$ = true } ;
name: IDENT | INTEGER | BIGINT | TEXT | BOOLEAN | VALUES | UNSUP_TYPE | UNSUP_IDENT ;
%%
