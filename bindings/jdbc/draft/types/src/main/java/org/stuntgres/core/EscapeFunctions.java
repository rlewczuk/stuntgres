package org.stuntgres.core;

import java.sql.SQLException;
import java.util.List;
import java.util.Locale;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/**
 * The scalar functions of the JDBC function escape that are not passed through under their
 * own name (design section 4.3). The arguments are used as written, including the white
 * space after a comma.
 */
final class EscapeFunctions {
    private static final String UNIT_PREFIX = "SQL_TSI_";

    private EscapeFunctions() {
    }

    /**
     * Appends the PostgreSQL text of a function call.
     *
     * @param out the text to append to
     * @param name the function name as written in the escape
     * @param args the translated arguments
     * @return false when the function is not in the table; nothing is appended then
     * @throws SQLException with SQLState 42601 for a wrong number of arguments or an
     *         unknown interval unit, 0A000 for an interval unit that is not supported
     */
    static boolean append(StringBuilder out, String name, List<? extends CharSequence> args)
            throws SQLException {
        String key = name.toLowerCase(Locale.ROOT);
        switch (key) {
            case "ceiling": return call(out, key, args, 1, "ceil(", ",", ")");
            case "log": return call(out, key, args, 1, "ln(", ",", ")");
            case "log10": return call(out, key, args, 1, "log(", ",", ")");
            case "power": return call(out, key, args, 2, "pow(", ",", ")");
            case "truncate": return call(out, key, args, 2, "trunc(", ",", ")");
            case "char": return call(out, key, args, 1, "chr(", ",", ")");
            case "concat": return call(out, key, args, -1, "(", "||", ")");
            case "lcase": return call(out, key, args, 1, "lower(", ",", ")");
            case "ucase": return call(out, key, args, 1, "upper(", ",", ")");
            case "left": return call(out, key, args, 2, "substring(", " for ", ")");
            case "length": return call(out, key, args, 1, "length(trim(trailing from ", ",", "))");
            case "ltrim": return call(out, key, args, 1, "trim(leading from ", ",", ")");
            case "rtrim": return call(out, key, args, 1, "trim(trailing from ", ",", ")");
            case "space": return call(out, key, args, 1, "repeat(' ',", ",", ")");
            case "curdate": return call(out, key, args, 0, "current_date", ",", "");
            case "curtime": return call(out, key, args, 0, "current_time", ",", "");
            case "database": return call(out, key, args, 0, "current_database()", ",", "");
            case "user": return call(out, key, args, 0, "user", ",", "");
            case "ifnull": return call(out, key, args, 2, "coalesce(", ",", ")");
            case "dayname": return call(out, key, args, 1, "to_char(", ",", ",'Day')");
            case "monthname": return call(out, key, args, 1, "to_char(", ",", ",'Month')");
            case "dayofmonth": return call(out, key, args, 1, "extract(day from ", ",", ")");
            case "dayofweek": return call(out, key, args, 1, "extract(dow from ", ",", ")+1");
            case "dayofyear": return call(out, key, args, 1, "extract(doy from ", ",", ")");
            case "hour": return call(out, key, args, 1, "extract(hour from ", ",", ")");
            case "minute": return call(out, key, args, 1, "extract(minute from ", ",", ")");
            case "month": return call(out, key, args, 1, "extract(month from ", ",", ")");
            case "quarter": return call(out, key, args, 1, "extract(quarter from ", ",", ")");
            case "second": return call(out, key, args, 1, "extract(second from ", ",", ")");
            case "week": return call(out, key, args, 1, "extract(week from ", ",", ")");
            case "year": return call(out, key, args, 1, "extract(year from ", ",", ")");
            case "substring":
                if (args.size() != 2 && args.size() != 3) {
                    throw arity(key, "two or three arguments");
                }
                return call(out, key, args, -1, "substr(", ",", ")");
            case "insert":
                check(key, args, 4);
                out.append("overlay(").append(args.get(0)).append(" placing ").append(args.get(3))
                        .append(" from ").append(args.get(1)).append(" for ").append(args.get(2))
                        .append(')');
                return true;
            case "right":
                check(key, args, 2);
                out.append("substring(").append(args.get(0)).append(" from (length(")
                        .append(args.get(0)).append(")+1-").append(args.get(1)).append("))");
                return true;
            case "locate":
                return locate(out, args);
            case "timestampadd":
                check(key, args, 3);
                out.append('(');
                interval(out, args.get(0).toString(), args.get(1).toString());
                out.append('+').append(args.get(2)).append(')');
                return true;
            case "timestampdiff":
                check(key, args, 3);
                out.append("extract( ").append(datePart(args.get(0).toString())).append(" from (")
                        .append(args.get(2)).append('-').append(args.get(1)).append("))");
                return true;
            default:
                return false;
        }
    }

    /** Appends begin, the arguments joined by the separator, and end. */
    private static boolean call(StringBuilder out, String name,
            List<? extends CharSequence> args, int count, String begin, String separator,
            String end) throws SQLException {
        if (count >= 0) {
            check(name, args, count);
        }
        out.append(begin);
        for (int i = 0; i < args.size(); i++) {
            out.append(i > 0 ? separator : "").append(args.get(i));
        }
        out.append(end);
        return true;
    }

    private static void check(String name, List<? extends CharSequence> args, int count)
            throws SQLException {
        if (args.size() != count) {
            String[] words = {"no arguments", "exactly one argument", "exactly two arguments",
                "exactly three arguments", "exactly four arguments"};
            throw arity(name, words[count]);
        }
    }

    private static PSQLException arity(String name, String expected) {
        return new PSQLException("The function escape " + name + " takes " + expected + ".",
                PSQLState.SYNTAX_ERROR);
    }

    private static boolean locate(StringBuilder out, List<? extends CharSequence> args)
            throws SQLException {
        if (args.size() == 2) {
            out.append("position(").append(args.get(0)).append(" in ").append(args.get(1))
                    .append(')');
        } else if (args.size() == 3) {
            String position = "position(" + args.get(0) + " in substring(" + args.get(1)
                    + " from " + args.get(2) + "))";
            out.append('(').append(args.get(2)).append("*sign(").append(position).append(")+")
                    .append(position).append(')');
        } else {
            throw arity("locate", "two or three arguments");
        }
        return true;
    }

    /** Tells whether {@code unit} is SQL_TSI_ followed by {@code suffix}, in any case. */
    private static boolean isUnit(String unit, String suffix) {
        return unit.length() == UNIT_PREFIX.length() + suffix.length()
                && unit.regionMatches(true, UNIT_PREFIX.length(), suffix, 0, suffix.length());
    }

    private static PSQLException badUnit(String unit, String function, PSQLState state) {
        return new PSQLException("The interval unit " + unit
                + " is not supported by the function escape " + function + ".", state);
    }

    private static void interval(StringBuilder out, String unit, String value)
            throws SQLException {
        if (!unit.regionMatches(true, 0, UNIT_PREFIX, 0, UNIT_PREFIX.length())) {
            throw badUnit(unit, "timestampadd", PSQLState.SYNTAX_ERROR);
        }
        for (String name : new String[] {"day", "second", "hour", "minute", "month", "week",
            "year"}) {
            if (isUnit(unit, name)) {
                out.append("CAST(").append(value).append("||' ").append(name)
                        .append("' as interval)");
                return;
            }
        }
        if (isUnit(unit, "quarter")) {
            out.append("CAST((").append(value).append("::int * 3) || ' month' as interval)");
            return;
        }
        throw badUnit(unit, "timestampadd", PSQLState.NOT_IMPLEMENTED);
    }

    private static String datePart(String unit) throws SQLException {
        if (unit.regionMatches(true, 0, UNIT_PREFIX, 0, UNIT_PREFIX.length())) {
            for (String name : new String[] {"day", "second", "hour", "minute"}) {
                if (isUnit(unit, name)) {
                    return name;
                }
            }
            if (isUnit(unit, "frac_second")) {
                throw badUnit(unit, "timestampdiff", PSQLState.NOT_IMPLEMENTED);
            }
        }
        throw badUnit(unit, "timestampdiff", PSQLState.SYNTAX_ERROR);
    }
}
