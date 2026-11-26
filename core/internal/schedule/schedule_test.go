package schedule

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTranslation(t *testing.T) {
	cases := []struct {
		in, cron, en, it string
	}{
		{`{"kind":"manual"}`, "", "Only when run manually", "Solo quando lo avvii manualmente"},
		{``, "", "Only when run manually", "Solo quando lo avvii manualmente"},
		{`{"kind":"interval","every":15,"unit":"minutes"}`, "*/15 * * * *", "Every 15 minutes, Europe/Rome", "Ogni 15 minuti, Europe/Rome"},
		{`{"kind":"interval","every":1,"unit":"hours"}`, "0 */1 * * *", "Every hour, Europe/Rome", "Ogni ora, Europe/Rome"},
		{`{"kind":"interval","every":6,"unit":"hours"}`, "0 */6 * * *", "Every 6 hours, Europe/Rome", "Ogni 6 ore, Europe/Rome"},
		{`{"kind":"daily","time":"7:05"}`, "5 7 * * *", "Every day at 07:05, Europe/Rome", "Ogni giorno alle 07:05, Europe/Rome"},
		{`{"kind":"weekly","days":["fri","mon","tue","wed","thu"],"time":"07:30"}`, "30 7 * * 1-5", "Every weekday at 07:30, Europe/Rome", "Ogni giorno feriale alle 07:30, Europe/Rome"},
		{`{"kind":"weekly","days":["sun","sat"],"time":"22:00"}`, "0 22 * * 0,6", "Every Saturday and Sunday at 22:00, Europe/Rome", "Ogni sabato e domenica alle 22:00, Europe/Rome"},
		{`{"kind":"weekly","days":["mon","wed","fri"],"time":"08:00"}`, "0 8 * * 1,3,5", "Every Monday, Wednesday and Friday at 08:00, Europe/Rome", "Ogni lunedì, mercoledì e venerdì alle 08:00, Europe/Rome"},
		{`{"kind":"weekly","days":["mon","tue","sun"],"time":"08:00"}`, "0 8 * * 0-2", "Every Monday, Tuesday and Sunday at 08:00, Europe/Rome", "Ogni lunedì, martedì e domenica alle 08:00, Europe/Rome"},
		{`{"kind":"weekly","days":["mon","tue","wed","thu","fri","sat","sun"],"time":"08:00"}`, "0 8 * * *", "Every day at 08:00, Europe/Rome", "Ogni giorno alle 08:00, Europe/Rome"},
		{`{"kind":"weekly","days":["thu"],"time":"08:00"}`, "0 8 * * 4", "Every Thursday at 08:00, Europe/Rome", "Ogni giovedì alle 08:00, Europe/Rome"},
		{`{"kind":"monthly","day":1,"time":"06:00"}`, "0 6 1 * *", "Every month on day 1 at 06:00, Europe/Rome", "Ogni mese il giorno 1 alle 06:00, Europe/Rome"},
		{`{"kind":"monthly","day":31,"time":"06:00"}`, "0 6 31 * *", "Every month on day 31 at 06:00 (months without day 31 are skipped), Europe/Rome", "Ogni mese il giorno 31 alle 06:00 (i mesi senza il giorno 31 vengono saltati), Europe/Rome"},
		{`{"kind":"cron","expr":"  0  7 * * 1-5 "}`, "0 7 * * 1-5", "Custom schedule: 0 7 * * 1-5, Europe/Rome", "Pianificazione personalizzata: 0 7 * * 1-5, Europe/Rome"},
		{`{"kind":"cron","expr":"@daily"}`, "@daily", "Custom schedule: @daily, Europe/Rome", "Pianificazione personalizzata: @daily, Europe/Rome"},
	}
	for _, c := range cases {
		s, err := Parse([]byte(c.in))
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got := s.Cron(); got != c.cron {
			t.Errorf("%s: cron = %q, want %q", c.in, got, c.cron)
		}
		if got := s.Describe(English, "Europe/Rome"); got != c.en {
			t.Errorf("%s: en = %q, want %q", c.in, got, c.en)
		}
		if got := s.Describe(Italian, "Europe/Rome"); got != c.it {
			t.Errorf("%s: it = %q, want %q", c.in, got, c.it)
		}
	}
}

func TestNormalizeDropsForeignFields(t *testing.T) {
	s := MustParse(`{"kind":"daily","time":"07:30","days":["mon"],"every":5,"expr":"x"}`)
	if got := string(s.JSON()); got != `{"kind":"daily","time":"07:30"}` {
		t.Fatalf("json = %s", got)
	}
	if got := string(MustParse(``).JSON()); got != `{"kind":"manual"}` {
		t.Fatalf("manual json = %s", got)
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]string{
		`{"kind":"interval","every":7,"unit":"minutes"}`: "schedule.every",
		`{"kind":"interval","every":5,"unit":"hours"}`:   "schedule.every",
		`{"kind":"interval","every":5,"unit":"days"}`:    "schedule.unit",
		`{"kind":"daily","time":"24:00"}`:                "schedule.time",
		`{"kind":"daily","time":"7.30"}`:                 "schedule.time",
		`{"kind":"daily"}`:                               "schedule.time",
		`{"kind":"weekly","days":[],"time":"07:30"}`:     "schedule.days",
		`{"kind":"weekly","days":["mo"],"time":"07:30"}`: "schedule.days",
		`{"kind":"monthly","day":0,"time":"07:30"}`:      "schedule.day",
		`{"kind":"monthly","day":32,"time":"07:30"}`:     "schedule.day",
		`{"kind":"cron","expr":""}`:                      "schedule.expr",
		`{"kind":"cron","expr":"0 0 7 * * 1"}`:           "schedule.expr", // seconds field
		`{"kind":"cron","expr":"0 7 * *"}`:               "schedule.expr",
		`{"kind":"cron","expr":"61 7 * * *"}`:            "schedule.expr",
		`{"kind":"cron","expr":"CRON_TZ=UTC 0 7 * * *"}`: "schedule.expr",
		`{"kind":"cron","expr":"@every 5m"}`:             "schedule.expr",
		`{"kind":"cron","expr":"0 0 30 2 *"}`:            "schedule.expr", // never runs
		`{"kind":"hourly"}`:                              "schedule.kind",
		`[1,2]`:                                          "schedule",
		`{"kind":"daily","time":7}`:                      "schedule",
	}
	for in, field := range cases {
		_, err := Parse([]byte(in))
		var fe FieldErrors
		if !errors.As(err, &fe) || fe[field] == "" {
			t.Errorf("%s: err = %v, want a message for %s", in, err, field)
		}
	}
}

func TestNext(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	from := time.Date(2026, 11, 26, 6, 0, 0, 0, time.UTC) // Thursday 07:00 in Rome

	got := MustParse(`{"kind":"weekly","days":["mon","tue","wed","thu","fri"],"time":"07:30"}`).Next(from, rome, 5)
	want := []string{"2026-11-26 07:30", "2026-11-27 07:30", "2026-11-30 07:30", "2026-12-01 07:30", "2026-12-02 07:30"}
	if len(got) != 5 {
		t.Fatalf("next = %v", got)
	}
	for i, g := range got {
		if g.Location() != rome || g.Format("2006-01-02 15:04") != want[i] {
			t.Errorf("next[%d] = %v, want %s Rome", i, g, want[i])
		}
	}
	// Strictly after from.
	at := time.Date(2026, 11, 26, 7, 30, 0, 0, rome)
	if n := MustParse(`{"kind":"daily","time":"07:30"}`).Next(at, rome, 1); !n[0].Equal(at.AddDate(0, 0, 1)) {
		t.Errorf("strictly after: %v", n)
	}
	// Day 31 skips short months.
	n := MustParse(`{"kind":"monthly","day":31,"time":"06:00"}`).Next(from, time.UTC, 3)
	if n[0].Format("01-02") != "12-31" || n[1].Format("01-02") != "01-31" || n[2].Format("01-02") != "03-31" {
		t.Errorf("monthly 31: %v", n)
	}
	// Daylight saving time: 07:30 stays 07:30 local across the change.
	n = MustParse(`{"kind":"daily","time":"07:30"}`).Next(time.Date(2026, 3, 28, 6, 0, 0, 0, rome), rome, 2)
	if n[0].Hour() != 7 || n[1].Hour() != 7 || n[1].Sub(n[0]) != 23*time.Hour {
		t.Errorf("dst: %v", n)
	}
	// Intervals line up with the clock.
	n = MustParse(`{"kind":"interval","every":20,"unit":"minutes"}`).Next(time.Date(2026, 1, 1, 10, 7, 0, 0, time.UTC), time.UTC, 3)
	if n[0].Minute() != 20 || n[1].Minute() != 40 || n[2].Minute() != 0 {
		t.Errorf("interval: %v", n)
	}
	if MustParse(`{"kind":"manual"}`).Next(from, rome, 5) != nil {
		t.Error("manual has no runs")
	}
}

func TestParseLang(t *testing.T) {
	for in, want := range map[string]Lang{
		"": English, "it": Italian, "it-IT,it;q=0.9,en;q=0.8": Italian, "en-US,it;q=0.5": English, "de-DE": English, "IT": Italian,
	} {
		if got := ParseLang(in); got != want {
			t.Errorf("ParseLang(%q) = %s, want %s", in, got, want)
		}
	}
	if !strings.Contains(FieldErrors{"schedule.time": "x"}.Error(), "schedule.time: x") {
		t.Error("FieldErrors.Error")
	}
}
