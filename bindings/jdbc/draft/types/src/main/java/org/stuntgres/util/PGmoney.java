package org.stuntgres.util;

import java.sql.SQLException;

/** A value of the PostgreSQL type money as a double. */
public class PGmoney extends PGobject {
    private static final long serialVersionUID = 1L;

    /** The amount. */
    public double val;
    /** Whether the object stands for SQL NULL. */
    public boolean isNull;

    /**
     * Creates an amount.
     *
     * @param value the amount
     */
    public PGmoney(double value) {
        this();
        val = value;
    }

    /**
     * Creates an amount from text.
     *
     * @param value text as {@link #setValue(String)} takes it
     * @throws SQLException with SQLState 42820 when the text is not an amount
     */
    public PGmoney(String value) throws SQLException {
        this();
        read(value);
    }

    /** Creates the amount zero. */
    public PGmoney() {
        type = "money";
    }

    /**
     * Sets the amount from text: a currency sign, then digits with optional commas and a
     * decimal point; parentheses around the text make the amount negative. The first
     * character after an opening parenthesis is taken for the currency sign and dropped.
     *
     * @param s the text, or null for SQL NULL
     * @throws SQLException with SQLState 42820 when the rest is not a number
     */
    @Override
    public void setValue(String s) throws SQLException {
        read(s);
    }

    private void read(String s) throws SQLException {
        isNull = s == null;
        if (s == null) {
            return;
        }
        boolean negative = s.startsWith("(");
        String digits = negative ? s.substring(1) : s;
        if (digits.endsWith(")")) {
            digits = digits.substring(0, digits.length() - 1);
        }
        try {
            if (digits.isEmpty()) {
                throw new NumberFormatException(s);
            }
            double amount = Double.parseDouble(digits.substring(1).replace(",", ""));
            val = negative ? -amount : amount;
        } catch (NumberFormatException e) {
            throw new PSQLException("Cannot read \"" + s + "\" as a money value.",
                    PSQLState.NUMERIC_CONSTANT_OUT_OF_RANGE, e);
        }
    }

    @Override
    public int hashCode() {
        if (isNull) {
            return 0;
        }
        long bits = Double.doubleToLongBits(val);
        return 31 * super.hashCode() + (int) (bits ^ (bits >>> 32));
    }

    @Override
    public boolean equals(Object obj) {
        if (!(obj instanceof PGmoney)) {
            return false;
        }
        PGmoney other = (PGmoney) obj;
        if (isNull || other.isNull) {
            return isNull && other.isNull;
        }
        return val == other.val;
    }

    /** Returns a dollar sign and the amount, with a leading hyphen when negative. */
    @Override
    public String getValue() {
        if (isNull) {
            return null;
        }
        return val < 0 ? "-$" + (-val) : "$" + val;
    }

    @Override
    public Object clone() throws CloneNotSupportedException {
        return super.clone();
    }
}
