package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.sql.SQLException;
import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class EscapeFunctionsTest {
    @Test
    void recordedFunctions() {
        JdbcEscapesTest.check("sql-functions.tsv", -1, 1);
    }

    @Test
    void translations() throws SQLException {
        assertEquals("SELECT (a|| b|| c)", JdbcEscapes.replace("SELECT {fn concat(a, b, c)}", true));
        assertEquals("SELECT substring(a for  2)", JdbcEscapes.replace("SELECT {fn LEFT(a, 2)}", true));
        assertEquals("SELECT ( 3*sign(position(a in substring( b from  3)))+position(a in"
                + " substring( b from  3)))", JdbcEscapes.replace("SELECT {fn locate(a, b, 3)}", true));
        assertEquals("SELECT (CAST(( n::int * 3) || ' month' as interval)+ ts)",
                JdbcEscapes.replace("SELECT {fn timestampadd(SQL_TSI_QUARTER, n, ts)}", true));
        assertEquals("SELECT extract( second from ( b- a))",
                JdbcEscapes.replace("SELECT {fn timestampdiff(sql_tsi_second, a, b)}", true));
        assertEquals("SELECT upper((a|| lower(b)))",
                JdbcEscapes.replace("SELECT {fn ucase({fn concat(a,{fn lcase(b)})})}", true)
                    .replace("||lower", "|| lower"));
    }

    @Test
    void errors() {
        SQLException e = assertThrows(SQLException.class,
                () -> JdbcEscapes.replace("SELECT {fn lcase(a, b)}", true));
        assertEquals("42601", e.getSQLState());
        assertEquals("The function escape lcase takes exactly one argument.", e.getMessage());
        assertEquals("The function escape curdate takes no arguments.", assertThrows(
                SQLException.class, () -> JdbcEscapes.replace("{fn curdate(1)}", true)).getMessage());
        assertEquals("The function escape substring takes two or three arguments.", assertThrows(
                SQLException.class, () -> JdbcEscapes.replace("{fn substring(a)}", true)).getMessage());
        e = assertThrows(SQLException.class,
                () -> JdbcEscapes.replace("{fn timestampadd(SQL_TSI_FRAC_SECOND, 1, t)}", true));
        assertEquals("0A000", e.getSQLState());
        assertEquals("The interval unit SQL_TSI_FRAC_SECOND is not supported by the function"
                + " escape timestampadd.", e.getMessage());
        assertEquals("42601", assertThrows(SQLException.class, () -> JdbcEscapes.replace(
                "{fn timestampdiff(SQL_TSI_WEEK, a, b)}", true)).getSQLState());
        assertEquals("42601", assertThrows(SQLException.class, () -> JdbcEscapes.replace(
                "{fn timestampadd(DAY, 1, t)}", true)).getSQLState());
    }
}
