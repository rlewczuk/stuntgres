package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class SqlScannerTest {
    private static final String[] KINDS = {"other", "word", "string", "quoted", "dollar", "line",
        "block"};

    /** Returns the tokens as "kind:text" joined by '|'; an unterminated token ends with '!'. */
    private static String tokens(String sql, boolean standardConformingStrings) {
        SqlScanner scanner = new SqlScanner(sql.toCharArray(), standardConformingStrings);
        StringBuilder out = new StringBuilder();
        while (!scanner.atEnd()) {
            int kind = scanner.next();
            out.append(out.length() > 0 ? "|" : "").append(KINDS[kind]).append(':')
                    .append(sql, scanner.start(), scanner.end())
                    .append(scanner.terminated() ? "" : "!");
        }
        return out.toString();
    }

    @Test
    void wordsAndCharacters() {
        assertEquals("word:SELECT|other: |other:1|other:+|word:a1|other:,|word:_b|other:;",
                tokens("SELECT 1+a1,_b;", true));
        assertEquals("word:a|other:$|word:b|other: |other:1|word:e5|other:?", tokens("a$b 1e5?", true));
        assertEquals("word:été|other:(|other:)", tokens("été()", true));
        assertEquals("", tokens("", true));
    }

    @Test
    void strings() {
        assertEquals("string:'a''b'|other:?", tokens("'a''b'?", true).replace("'|string:'", "''"));
        assertEquals("string:'a\\'|other:?|string:'!", tokens("'a\\'?'", true));
        assertEquals("string:'a\\'?'", tokens("'a\\'?'", false));
        assertEquals("other: |word:E|string:'a\\'?'", tokens(" E'a\\'?'", true));
        assertEquals("word:xE|string:'a\\'|other:?|string:'!", tokens("xE'a\\'?'", true));
        assertEquals("string:'abc!", tokens("'abc", true));
        assertEquals("quoted:\"a?\"|quoted:\"b!", tokens("\"a?\"\"b", true));
    }

    @Test
    void dollarQuotes() {
        assertEquals("dollar:$$?$$|other:,|dollar:$t$ $x$ $t$", tokens("$$?$$,$t$ $x$ $t$", true));
        assertEquals("other:$|other:1|other: |word:a|other:$|word:b|other:$", tokens("$1 a$b$", true));
        assertEquals("dollar:$a$x$a$|other:$|word:b|other:$", tokens("$a$x$a$$b$", true));
        assertEquals("dollar:$$abc!", tokens("$$abc", true));
        assertEquals("dollar:$t$abc$T$!", tokens("$t$abc$T$", true));
    }

    @Test
    void comments() {
        assertEquals("line:-- ?\n|other:?", tokens("-- ?\n?", true));
        assertEquals("line:-- ?\r|other:\n", tokens("-- ?\r\n", true));
        assertEquals("line:--", tokens("--", true));
        assertEquals("other:-|other: |other:-", tokens("- -", true));
        assertEquals("block:/* a /* ? */ ? */|other:?", tokens("/* a /* ? */ ? */?", true));
        assertEquals("block:/*/|other:?", tokens("/*/?", true));
        assertEquals("block:/* /* */!", tokens("/* /* */", true));
        assertEquals("other:/|other: |other:*", tokens("/ *", true));
    }

    @Test
    void helpers() {
        assertTrue(SqlScanner.isKeyword("xSeLeCt".toCharArray(), 1, 6, "select"));
        assertFalse(SqlScanner.isKeyword("selects".toCharArray(), 0, 7, "select"));
        assertFalse(SqlScanner.isKeyword("sel".toCharArray(), 0, 6, "select"));
        assertTrue(SqlScanner.isWordStart('_'));
        assertFalse(SqlScanner.isWordStart('$'));
        assertFalse(SqlScanner.isWordStart('1'));
        assertTrue(SqlScanner.isWordPart('1'));
        assertFalse(SqlScanner.isWordPart('$'));
        SqlScanner scanner = new SqlScanner("ab cd".toCharArray(), true);
        scanner.seek(3);
        assertEquals(SqlScanner.WORD, scanner.next());
        assertEquals(3, scanner.start());
        assertEquals(5, scanner.end());
        assertEquals(5, scanner.position());
        assertTrue(scanner.atEnd());
    }
}
