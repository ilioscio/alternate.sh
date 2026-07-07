package shell

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ilioscio/alternate.sh/internal/db"
)

func cmdCalendar(s *Session, args []string) error {
	if len(args) > 0 && args[0] == "edit" {
		return calendarEdit(s)
	}
	return calendarShow(s)
}

func calendarShow(s *Session) error {
	if s.User.Calendar == "" {
		s.Println("No calendar entries. Use 'calendar edit' to add entries.")
		s.Println("Format: month/day description  (e.g.  7/15 Team meeting)")
		return nil
	}

	now := time.Now()
	window := now.AddDate(0, 0, 30)

	type entry struct {
		t    time.Time
		desc string
	}
	var upcoming []entry
	var past []entry

	for _, line := range strings.Split(s.User.Calendar, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		datePart := parts[0]
		desc := parts[1]

		// parse month/day
		slash := strings.Index(datePart, "/")
		if slash < 0 {
			continue
		}
		month, err1 := strconv.Atoi(datePart[:slash])
		day, err2 := strconv.Atoi(datePart[slash+1:])
		if err1 != nil || err2 != nil {
			continue
		}
		t := time.Date(now.Year(), time.Month(month), day, 0, 0, 0, 0, now.Location())
		if t.Before(now) {
			// Try next year
			t = t.AddDate(1, 0, 0)
		}
		e := entry{t: t, desc: desc}
		if !t.After(window) {
			upcoming = append(upcoming, e)
		} else {
			past = append(past, e)
		}
	}

	if len(upcoming) == 0 && len(past) == 0 {
		s.Println("No calendar entries. Use 'calendar edit' to add entries.")
		return nil
	}

	s.Printf("Upcoming events (next 30 days):\r\n\r\n")
	if len(upcoming) == 0 {
		s.Println("  (none in the next 30 days)")
	} else {
		for _, e := range upcoming {
			daysUntil := int(e.t.Sub(now).Hours() / 24)
			when := fmt.Sprintf("in %d day(s)", daysUntil)
			if daysUntil == 0 {
				when = "TODAY"
			}
			s.Printf("  %-12s  %s  — %s\r\n", e.t.Format("Jan 2"), when, e.desc)
		}
	}

	if len(past) > 0 {
		s.Printf("\r\nFurther ahead:\r\n")
		for _, e := range past {
			s.Printf("  %-12s  %s\r\n", e.t.Format("Jan 2"), e.desc)
		}
	}
	return nil
}

func calendarEdit(s *Session) error {
	s.Println("Current calendar:")
	if s.User.Calendar == "" {
		s.Println("  (empty)")
	} else {
		for _, line := range strings.Split(s.User.Calendar, "\n") {
			s.Printf("  %s\r\n", line)
		}
	}
	s.Println("")
	s.Println("Enter new calendar entries (replaces all above). End with '.' on its own line.")
	s.Println("Press '.' immediately to cancel without changes.")
	s.Println("Format: month/day description  (e.g. 7/15 Team meeting)")
	cal := readBody(s, "")
	if cal == "" {
		s.Println("No changes made.")
		return nil
	}
	if err := db.UpdateCalendar(s.ctx, s.db, s.User.ID, cal); err != nil {
		s.Println("calendar: error saving")
		return nil
	}
	s.User.Calendar = cal
	s.Println("Calendar saved.")
	return nil
}
