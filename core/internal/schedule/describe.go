package schedule

import (
	"fmt"
	"strings"
)

// Lang is a language for sentences.
type Lang string

// Supported sentence languages (the UI languages).
const (
	English Lang = "en"
	Italian Lang = "it"
)

// ParseLang picks the sentence language from a language tag or an
// Accept-Language header; anything that is not Italian is English.
func ParseLang(v string) Lang {
	for _, part := range strings.Split(v, ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case tag == "it" || strings.HasPrefix(tag, "it-"):
			return Italian
		case tag == "en" || strings.HasPrefix(tag, "en-"):
			return English
		}
	}
	return English
}

var dayNames = map[Lang]map[string]string{
	English: {"mon": "Monday", "tue": "Tuesday", "wed": "Wednesday", "thu": "Thursday", "fri": "Friday", "sat": "Saturday", "sun": "Sunday"},
	Italian: {"mon": "lunedì", "tue": "martedì", "wed": "mercoledì", "thu": "giovedì", "fri": "venerdì", "sat": "sabato", "sun": "domenica"},
}

// Describe returns the schedule as a sentence, e.g. "Every weekday at
// 07:30, Europe/Rome". The schedule must be normalised.
func (s Schedule) Describe(lang Lang, tz string) string {
	it := lang == Italian
	pick := func(en, itv string) string {
		if it {
			return itv
		}
		return en
	}
	var text string
	switch s.Kind {
	case Interval:
		switch {
		case s.Unit == Hours && s.Every == 1:
			text = pick("Every hour", "Ogni ora")
		case s.Unit == Hours:
			text = fmt.Sprintf(pick("Every %d hours", "Ogni %d ore"), s.Every)
		default:
			text = fmt.Sprintf(pick("Every %d minutes", "Ogni %d minuti"), s.Every)
		}
	case Daily:
		text = fmt.Sprintf(pick("Every day at %s", "Ogni giorno alle %s"), s.Time)
	case Weekly:
		text = fmt.Sprintf(pick("%s at %s", "%s alle %s"), weekdays(lang, s.Days), s.Time)
	case Monthly:
		text = fmt.Sprintf(pick("Every month on day %d at %s", "Ogni mese il giorno %d alle %s"), s.Day, s.Time)
		if s.Day > 28 {
			text += fmt.Sprintf(pick(" (months without day %d are skipped)", " (i mesi senza il giorno %d vengono saltati)"), s.Day)
		}
	case Cron:
		text = pick("Custom schedule: ", "Pianificazione personalizzata: ") + s.Expr
	default:
		return pick("Only when run manually", "Solo quando lo avvii manualmente")
	}
	if tz == "" {
		return text
	}
	return text + ", " + tz
}

func weekdays(lang Lang, days []string) string {
	it := lang == Italian
	joined := strings.Join(days, ",")
	switch joined {
	case "mon,tue,wed,thu,fri,sat,sun":
		if it {
			return "Ogni giorno"
		}
		return "Every day"
	case "mon,tue,wed,thu,fri":
		if it {
			return "Ogni giorno feriale"
		}
		return "Every weekday"
	}
	names := make([]string, 0, len(days))
	for _, d := range days {
		names = append(names, dayNames[lang][d])
	}
	and := " and "
	prefix := "Every "
	if it {
		and, prefix = " e ", "Ogni "
	}
	if len(names) == 1 {
		return prefix + names[0]
	}
	return prefix + strings.Join(names[:len(names)-1], ", ") + and + names[len(names)-1]
}
