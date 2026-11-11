package org.stuntgres.util;

import java.sql.Timestamp;
import java.util.Calendar;

/**
 * A java.sql.Timestamp that carries the calendar whose time zone the value belongs to. A
 * parameter of this class is sent with a time zone when it has a calendar and without one
 * when it has none.
 */
public class PGTimestamp extends Timestamp {
    private static final long serialVersionUID = 1L;

    private Calendar calendar;

    /**
     * Creates a value without a calendar.
     *
     * @param time milliseconds since 1970-01-01 00:00:00 GMT
     */
    public PGTimestamp(long time) {
        this(time, null);
    }

    /**
     * Creates a value with a calendar.
     *
     * @param time milliseconds since 1970-01-01 00:00:00 GMT
     * @param calendar the calendar, or null
     */
    public PGTimestamp(long time, Calendar calendar) {
        super(time);
        this.calendar = calendar;
    }

    /**
     * Sets the calendar.
     *
     * @param calendar the calendar, or null
     */
    public void setCalendar(Calendar calendar) {
        this.calendar = calendar;
    }

    /**
     * Returns the calendar.
     *
     * @return the calendar, or null
     */
    public Calendar getCalendar() {
        return calendar;
    }

    @Override
    public int hashCode() {
        return 31 * super.hashCode() + (calendar == null ? 0 : calendar.hashCode());
    }

    @Override
    public boolean equals(Object o) {
        if (this == o) {
            return true;
        }
        if (o == null || getClass() != o.getClass() || !super.equals(o)) {
            return false;
        }
        Calendar other = ((PGTimestamp) o).calendar;
        return calendar == null ? other == null : calendar.equals(other);
    }

    /** The copy has a copy of the calendar. */
    @Override
    public Object clone() {
        PGTimestamp copy = (PGTimestamp) super.clone();
        if (calendar != null) {
            copy.calendar = (Calendar) calendar.clone();
        }
        return copy;
    }
}
