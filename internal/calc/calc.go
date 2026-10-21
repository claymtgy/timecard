// Package calc holds the pure time and money arithmetic used across the app.
// Durations are integer minutes, money is integer cents, percentages are basis points.
package calc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const DateLayout = "2006-01-02"

// WorkedMinutes returns (end - start) - break for a same-day shift expressed in
// minutes after midnight. This is the spreadsheet's (End-Start)*24-Break.
func WorkedMinutes(start, end, breakMinutes int) (int, error) {
	if start < 0 || start > 1440 || end < 0 || end > 1440 {
		return 0, errors.New("times must be within the day")
	}
	if end <= start {
		return 0, errors.New("end time must be after start time")
	}
	if breakMinutes < 0 {
		return 0, errors.New("break cannot be negative")
	}
	worked := end - start - breakMinutes
	if worked < 0 {
		return 0, errors.New("break is longer than the shift")
	}
	return worked, nil
}

// Amount is minutes x hourly rate, rounded half-up to the cent.
func Amount(minutes int, ratePerHourCents int64) int64 {
	return divRound(int64(minutes)*ratePerHourCents, 60)
}

// ShareOf returns amount x bps/10000, rounded half-up. Negative amounts yield 0,
// so a job that lost money pays no profit share.
func ShareOf(amountCents int64, bps int) int64 {
	if amountCents <= 0 || bps <= 0 {
		return 0
	}
	return divRound(amountCents*int64(bps), 10000)
}

func divRound(n, d int64) int64 {
	if n >= 0 {
		return (n + d/2) / d
	}
	return -((-n + d/2) / d)
}

// JobProfit is the bid-job math from the "Bid Jobs" sheet:
// labor = hours x labor cost rate; profit = bid - labor; share = profit x share%.
type JobProfit struct {
	LaborCostCents   int64 `json:"labor_cost_cents"`
	ProfitCents      int64 `json:"profit_cents"`
	ProfitShareCents int64 `json:"profit_share_cents"`
}

func BidJobProfit(bidCents int64, totalMinutes int, laborRateCents int64, shareBps int) JobProfit {
	labor := Amount(totalMinutes, laborRateCents)
	profit := bidCents - labor
	return JobProfit{LaborCostCents: labor, ProfitCents: profit, ProfitShareCents: ShareOf(profit, shareBps)}
}

// ParseClock parses "7", "7:30", "730", "07:30", "7:30am", "3:15 PM", "15:15" into
// minutes after midnight. "24:00" / "12am" at end of day are not supported.
func ParseClock(s string) (int, error) {
	orig := s
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if s == "" {
		return 0, errors.New("time is required")
	}
	suffix := ""
	for _, suf := range []string{"am", "pm", "a", "p"} {
		if strings.HasSuffix(s, suf) {
			suffix = suf[:1]
			s = strings.TrimSuffix(s, suf)
			break
		}
	}
	var h, m int
	var err error
	switch {
	case strings.Contains(s, ":"):
		hs, ms, _ := strings.Cut(s, ":")
		h, err = strconv.Atoi(hs)
		if err == nil {
			m, err = strconv.Atoi(ms)
		}
		if len(ms) != 2 {
			err = errors.New("bad minutes")
		}
	case len(s) >= 3 && len(s) <= 4:
		h, err = strconv.Atoi(s[:len(s)-2])
		if err == nil {
			m, err = strconv.Atoi(s[len(s)-2:])
		}
	default:
		h, err = strconv.Atoi(s)
	}
	if err != nil || h < 0 || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid time %q", orig)
	}
	switch suffix {
	case "a":
		if h < 1 || h > 12 {
			return 0, fmt.Errorf("invalid time %q", orig)
		}
		if h == 12 {
			h = 0
		}
	case "p":
		if h < 1 || h > 12 {
			return 0, fmt.Errorf("invalid time %q", orig)
		}
		if h != 12 {
			h += 12
		}
	default:
		if h > 23 {
			return 0, fmt.Errorf("invalid time %q", orig)
		}
	}
	return h*60 + m, nil
}

// ParseDate parses YYYY-MM-DD.
func ParseDate(s string) (time.Time, error) {
	t, err := time.Parse(DateLayout, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q (want YYYY-MM-DD)", s)
	}
	return t, nil
}

// WeekStart returns the Monday on or before d.
func WeekStart(d time.Time) time.Time {
	offset := (int(d.Weekday()) + 6) % 7 // Monday=0 ... Sunday=6
	return time.Date(d.Year(), d.Month(), d.Day()-offset, 0, 0, 0, 0, time.UTC)
}

// Hours converts minutes to decimal hours (for display and invoice quantities).
func Hours(minutes int) float64 { return float64(minutes) / 60 }

// Segment is one day's portion of a clocked shift, in minutes after local midnight.
type Segment struct {
	Date  string
	Start int
	End   int
	Break int
}

// SplitShift turns a clock-in/clock-out pair (already in the company time zone) into
// per-day segments, splitting at midnight. The break is deducted from the longest segment.
func SplitShift(start, end time.Time, breakMinutes int) ([]Segment, error) {
	if !end.After(start) {
		return nil, errors.New("clock-out must be after clock-in")
	}
	if end.Sub(start) > 7*24*time.Hour {
		return nil, errors.New("shift is longer than a week; fix it on the timesheet instead")
	}
	var segs []Segment
	for day := start; ; {
		y, m, d := day.Date()
		nextMidnight := time.Date(y, m, d+1, 0, 0, 0, 0, day.Location())
		s := day.Hour()*60 + day.Minute()
		if end.Before(nextMidnight) || end.Equal(nextMidnight) {
			e := end.Hour()*60 + end.Minute()
			if end.Equal(nextMidnight) {
				e = 1440
			}
			if e > s {
				segs = append(segs, Segment{Date: day.Format(DateLayout), Start: s, End: e})
			}
			break
		}
		if s < 1440 {
			segs = append(segs, Segment{Date: day.Format(DateLayout), Start: s, End: 1440})
		}
		day = nextMidnight
	}
	if len(segs) == 0 {
		return nil, errors.New("shift was shorter than a minute")
	}
	longest := 0
	for i, sg := range segs {
		if sg.End-sg.Start > segs[longest].End-segs[longest].Start {
			longest = i
		}
	}
	if breakMinutes > segs[longest].End-segs[longest].Start {
		return nil, errors.New("break is longer than the time worked")
	}
	segs[longest].Break = breakMinutes
	return segs, nil
}
