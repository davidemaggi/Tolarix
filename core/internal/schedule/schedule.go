// Package schedule is the flow schedule model: what the schedule wizard
// edits, its translation to a cron expression, a plain-language sentence
// (English and Italian) and the next run times.
package schedule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Kind is the type of schedule.
type Kind string

// Schedule kinds, matching the wizard's choices.
const (
	Manual   Kind = "manual"   // only when run by hand
	Interval Kind = "interval" // every N minutes or hours
	Daily    Kind = "daily"    // every day at a time
	Weekly   Kind = "weekly"   // on some weekdays at a time
	Monthly  Kind = "monthly"  // every month on a day at a time
	Cron     Kind = "cron"     // a 5-field cron expression (advanced)
)

// Interval units.
const (
	Minutes = "minutes"
	Hours   = "hours"
)

// Allowed interval steps: they divide the hour or the day evenly, so the
// runs fall at the same clock times every hour or day.
var (
	MinuteSteps = []int{5, 10, 15, 20, 30}
	HourSteps   = []int{1, 2, 3, 4, 6, 8, 12}
)

// Weekdays in display order (Monday first), as stored in Days.
var Weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var cronDay = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// Schedule is the stored schedule of a flow (JSON in the database and API).
type Schedule struct {
	Kind  Kind     `json:"kind"`
	Every int      `json:"every,omitempty"` // interval
	Unit  string   `json:"unit,omitempty"`  // interval: minutes or hours
	Time  string   `json:"time,omitempty"`  // daily, weekly, monthly: "HH:MM"
	Days  []string `json:"days,omitempty"`  // weekly: mon..sun
	Day   int      `json:"day,omitempty"`   // monthly: 1..31
	Expr  string   `json:"expr,omitempty"`  // cron
}

// FieldErrors are validation messages keyed by field path
// ("schedule.time", ...), ready for the API's field errors.
type FieldErrors map[string]string

func (e FieldErrors) Error() string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+e[k])
	}
	return "invalid schedule: " + strings.Join(parts, "; ")
}

// Parse reads, validates and normalises a stored schedule. Empty input is
// the manual schedule.
func Parse(raw json.RawMessage) (Schedule, error) {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 || string(b) == "null" {
		return Schedule{Kind: Manual}, nil
	}
	var s Schedule
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&s); err != nil {
		return s, FieldErrors{"schedule": "The schedule is not valid."}
	}
	return s.Normalize()
}

// MustParse is Parse for values known to be valid (tests, defaults).
func MustParse(raw string) Schedule {
	s, err := Parse(json.RawMessage(raw))
	if err != nil {
		panic(err)
	}
	return s
}

// JSON is the canonical stored form: only the fields of the kind.
func (s Schedule) JSON() json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// IsManual reports whether the schedule never runs by itself.
func (s Schedule) IsManual() bool { return s.Kind == Manual || s.Kind == "" }

var reTime = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)

// Normalize validates the schedule and returns it with only the fields of
// its kind, the time as "HH:MM" and the days in week order.
func (s Schedule) Normalize() (Schedule, error) {
	errs := FieldErrors{}
	out := Schedule{Kind: s.Kind}
	clock := func() {
		h, m, ok := parseClock(s.Time)
		if !ok {
			errs["schedule.time"] = "Enter a time as HH:MM, for example 07:30."
			return
		}
		out.Time = fmt.Sprintf("%02d:%02d", h, m)
	}
	switch s.Kind {
	case Manual, "":
		out.Kind = Manual
	case Interval:
		out.Every, out.Unit = s.Every, s.Unit
		switch s.Unit {
		case Minutes:
			if !slices.Contains(MinuteSteps, s.Every) {
				errs["schedule.every"] = "Choose 5, 10, 15, 20 or 30 minutes."
			}
		case Hours:
			if !slices.Contains(HourSteps, s.Every) {
				errs["schedule.every"] = "Choose 1, 2, 3, 4, 6, 8 or 12 hours."
			}
		default:
			errs["schedule.unit"] = "Choose minutes or hours."
		}
	case Daily:
		clock()
	case Weekly:
		clock()
		seen := map[string]bool{}
		for _, d := range s.Days {
			d = strings.ToLower(strings.TrimSpace(d))
			if _, ok := cronDay[d]; !ok {
				errs["schedule.days"] = "Unknown weekday."
				break
			}
			seen[d] = true
		}
		for _, d := range Weekdays {
			if seen[d] {
				out.Days = append(out.Days, d)
			}
		}
		if len(out.Days) == 0 && errs["schedule.days"] == "" {
			errs["schedule.days"] = "Choose at least one day."
		}
	case Monthly:
		clock()
		out.Day = s.Day
		if s.Day < 1 || s.Day > 31 {
			errs["schedule.day"] = "Choose a day between 1 and 31."
		}
	case Cron:
		out.Expr = strings.Join(strings.Fields(s.Expr), " ")
		if msg := checkExpr(out.Expr); msg != "" {
			errs["schedule.expr"] = msg
		}
	default:
		errs["schedule.kind"] = "Unknown kind of schedule."
	}
	if len(errs) > 0 {
		return out, errs
	}
	return out, nil
}

func parseClock(v string) (h, m int, ok bool) {
	g := reTime.FindStringSubmatch(strings.TrimSpace(v))
	if g == nil {
		return 0, 0, false
	}
	h, _ = strconv.Atoi(g[1])
	m, _ = strconv.Atoi(g[2])
	return h, m, h < 24 && m < 60
}

// checkExpr validates an advanced expression; "" means valid.
func checkExpr(expr string) string {
	switch {
	case expr == "":
		return "Enter a cron expression."
	case strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ="):
		return "Choose the timezone in the timezone field, not in the expression."
	case strings.HasPrefix(expr, "@every"):
		return "@every is not supported; use the \"Every N minutes or hours\" option."
	case !strings.HasPrefix(expr, "@") && len(strings.Fields(expr)) != 5:
		return "Use 5 fields: minute, hour, day of month, month, day of week."
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return "This is not a valid cron expression (" + err.Error() + ")."
	}
	if sched.Next(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)).IsZero() {
		return "This expression never runs."
	}
	return ""
}

// Cron returns the 5-field cron expression, or "" for a manual schedule.
// The schedule must be normalised.
func (s Schedule) Cron() string {
	h, m, _ := parseClock(s.Time)
	switch s.Kind {
	case Interval:
		if s.Unit == Hours {
			return fmt.Sprintf("0 */%d * * *", s.Every)
		}
		return fmt.Sprintf("*/%d * * * *", s.Every)
	case Daily:
		return fmt.Sprintf("%d %d * * *", m, h)
	case Weekly:
		return fmt.Sprintf("%d %d * * %s", m, h, cronDays(s.Days))
	case Monthly:
		return fmt.Sprintf("%d %d %d * *", m, h, s.Day)
	case Cron:
		return s.Expr
	}
	return ""
}

// cronDays writes weekdays as cron numbers with ranges ("1-5", "0,6").
func cronDays(days []string) string {
	nums := make([]int, 0, len(days))
	for _, d := range days {
		nums = append(nums, cronDay[d])
	}
	sort.Ints(nums)
	if len(nums) == 7 {
		return "*"
	}
	var parts []string
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		switch {
		case j-i >= 2:
			parts = append(parts, fmt.Sprintf("%d-%d", nums[i], nums[j]))
		case j > i:
			parts = append(parts, strconv.Itoa(nums[i]), strconv.Itoa(nums[j]))
		default:
			parts = append(parts, strconv.Itoa(nums[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// Next returns up to n run times strictly after from, in loc. A manual
// schedule has none.
func (s Schedule) Next(from time.Time, loc *time.Location, n int) []time.Time {
	expr := s.Cron()
	if expr == "" || n <= 0 {
		return nil
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return nil
	}
	if loc == nil {
		loc = time.UTC
	}
	var out []time.Time
	t := from.In(loc)
	for len(out) < n {
		t = sched.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t)
	}
	return out
}
