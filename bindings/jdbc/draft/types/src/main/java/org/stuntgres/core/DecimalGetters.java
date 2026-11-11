package org.stuntgres.core;

import java.math.BigDecimal;
import java.math.RoundingMode;
import java.sql.SQLException;

/**
 * The conversions behind ResultSet.getFloat, getDouble and getBigDecimal (design section
 * 7.3). The value is the column text and is never null: for SQL NULL the caller returns 0
 * or null without calling these methods.
 */
public final class DecimalGetters {
    private DecimalGetters() {
    }

    /**
     * Reads a column as float.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @param context the conversion context
     * @return the number; NaN and the infinities are read as such
     * @throws SQLException with SQLState 22003 when the text is not a number
     */
    public static float getFloat(String value, int oid, ConversionContext context)
            throws SQLException {
        String number = NumberText.withoutCurrency(value);
        int bool = IntegerGetters.booleanAsNumber(number, oid, context);
        if (bool >= 0) {
            return bool;
        }
        return NumberText.real(number);
    }

    /**
     * Reads a column as double.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @param context the conversion context
     * @return the number; NaN and the infinities are read as such
     * @throws SQLException with SQLState 22003 when the text is not a number
     */
    public static double getDouble(String value, int oid, ConversionContext context)
            throws SQLException {
        String number = NumberText.withoutCurrency(value);
        int bool = IntegerGetters.booleanAsNumber(number, oid, context);
        if (bool >= 0) {
            return bool;
        }
        return NumberText.doublePrecision(number);
    }

    /**
     * Reads a column as BigDecimal with the scale of the text.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @param context the conversion context
     * @return the number
     * @throws SQLException with SQLState 22003 when the text is not a decimal number; NaN
     *         and the infinities are such texts
     */
    public static BigDecimal getBigDecimal(String value, int oid, ConversionContext context)
            throws SQLException {
        String number = NumberText.withoutCurrency(value);
        int bool = IntegerGetters.booleanAsNumber(number, oid, context);
        return bool >= 0 ? BigDecimal.valueOf(bool) : NumberText.decimal(number);
    }

    /**
     * Reads a column as BigDecimal rounded half-even to a scale.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @param scale the number of digits after the decimal point
     * @param context the conversion context
     * @return the number
     * @throws SQLException with SQLState 22003 when the text is not a decimal number
     */
    public static BigDecimal getBigDecimal(String value, int oid, int scale,
            ConversionContext context) throws SQLException {
        BigDecimal number = getBigDecimal(value, oid, context);
        try {
            return number.setScale(scale, RoundingMode.HALF_EVEN);
        } catch (ArithmeticException e) {
            throw ConversionErrors.badValue("BigDecimal", number.toString());
        }
    }
}
