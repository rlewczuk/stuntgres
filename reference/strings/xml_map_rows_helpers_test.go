package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow REL_18_6 xml.c's literal templates, not backend output.
const xmlMapXSI = ` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`
const xmlMapInteger = `<xsd:simpleType name="INTEGER">
  <xsd:restriction base="xsd:int">
    <xsd:maxInclusive value="2147483647"/>
    <xsd:minInclusive value="-2147483648"/>
  </xsd:restriction>
</xsd:simpleType>

`

func xmlMapLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func xmlMapDocument(root, namespace, rows string) string {
	ns := ""
	if namespace != "" { ns = ` xmlns="` + namespace + `"` }
	return "<" + root + xmlMapXSI + ns + ">\n\n" + rows + "</" + root + ">\n"
}

func xmlMapRow(fields string) string { return "<row>\n" + fields + "</row>\n\n" }

func xmlMapForest(root, namespace, fields string) string {
	ns := ""
	if namespace != "" { ns = ` xmlns="` + namespace + `"` }
	return "<" + root + xmlMapXSI + ns + ">\n" + fields + "</" + root + ">\n\n"
}

func xmlMapSchema(types, columns, root, suffix, namespace string, forest bool) string {
	header := "<xsd:schema\n    xmlns:xsd=\"http://www.w3.org/2001/XMLSchema\""
	if namespace != "" { header += "\n    targetNamespace=\"" + namespace + "\"\n    elementFormDefault=\"qualified\"" }
	row := "RowType" + suffix
	body := header + ">\n\n" + types + `<xsd:complexType name="` + row + `">` + "\n  <xsd:sequence>\n" + columns + "  </xsd:sequence>\n</xsd:complexType>\n\n"
	top := row
	if !forest {
		top = "TableType" + suffix
		body += `<xsd:complexType name="` + top + `">` + "\n  <xsd:sequence>\n" + `    <xsd:element name="row" type="` + row + `" minOccurs="0" maxOccurs="unbounded"/>` + "\n  </xsd:sequence>\n</xsd:complexType>\n\n"
	}
	return body + `<xsd:element name="` + root + `" type="` + top + `"/>` + "\n\n</xsd:schema>"
}

func xmlMapColumn(name, typ string, nulls bool) string {
	attr := `minOccurs="0"`
	if nulls { attr = `nillable="true"` }
	return `    <xsd:element name="` + name + `" type="` + typ + `" ` + attr + `></xsd:element>` + "\n"
}

func xmlMapIntSchema(name, root, suffix, ns string, nulls, forest bool) string {
	return xmlMapSchema(xmlMapInteger, xmlMapColumn(name, "INTEGER", nulls), root, suffix, ns, forest)
}

func xmlMapCombined(root, namespace, schema, rows string, forest bool) string {
	if forest { return schema + "\n\n" + rows }
	link := ` xsi:noNamespaceSchemaLocation="#"`
	ns := ""
	if namespace != "" { ns = ` xmlns="` + namespace + `"`; link = ` xsi:schemaLocation="` + namespace + ` #"` }
	return "<" + root + xmlMapXSI + ns + link + ">\n\n" + schema + "\n\n" + rows + "</" + root + ">\n"
}

func xmlMapExec(t *testing.T, p *fixture.Pair, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, q := range queries { if _, err := c.ExecContext(p.Ctx, q); err != nil { t.Fatal(q, err) } }
}

func xmlMapDatabase(t *testing.T, p *fixture.Pair, c *sql.Conn) string {
	t.Helper()
	var name string
	if err := c.QueryRowContext(p.Ctx, `SELECT current_database()`).Scan(&name); err != nil { t.Fatal(err) }
	// REL_18_6 xml.c: SQL/XML fully escaped, period-escaped multipart names.
	// Both fixtures use ASCII database names; this independently spells that
	// release subset rather than asking either mapper to manufacture a golden.
	var mapped strings.Builder
	for i,r:=range name{
		if r>127{t.Fatalf("fixture database needs an independent non-ASCII name expectation: %q",name)}
		letter:=r>='A'&&r<='Z'||r>='a'&&r<='z'
		escape:=r==':'||r=='.'||r=='_'&&i+1<len(name)&&name[i+1]=='x'||i==0&&len(name)>=3&&strings.EqualFold(name[:3],"xml")
		if !escape{escape=!(letter||r=='_'||i>0&&(r>='0'&&r<='9'||r=='-'))}
		if escape{fmt.Fprintf(&mapped,"_x%04X_",r)}else{mapped.WriteRune(r)}
	}
	return mapped.String()
}

func xmlMapSPI(code, message, query string, position int, context bool) fixture.Diagnostic {
	d := xmlError(code, message, "", 0)
	if position != 0 { d.InternalPosition, d.InternalQuery = fixture.Position(position), query }
	if context { d.Context = "SQL statement \"" + query + "\"" }
	return d
}
