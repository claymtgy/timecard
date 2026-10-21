package calc

import (
	"testing"
	"time"
)

func TestWorkedMinutes(t *testing.T) {
	// Justin, Mon: 10:00am - 8:30pm with a 0.5h break = 10h (spreadsheet row 2).
	got, err := WorkedMinutes(10*60, 20*60+30, 30)
	if err != nil || got != 600 {
		t.Fatalf("got %d, %v; want 600", got, err)
	}
	// 7:00am - 4:30pm, 30 min break = 9h.
	if got, _ := WorkedMinutes(7*60, 16*60+30, 30); got != 540 {
		t.Fatalf("got %d want 540", got)
	}
	for _, c := range []struct{ s, e, b int }{{600, 600, 0}, {600, 500, 0}, {600, 630, 45}, {-1, 10, 0}} {
		if _, err := WorkedMinutes(c.s, c.e, c.b); err == nil {
			t.Errorf("expected error for %+v", c)
		}
	}
}

func TestAmount(t *testing.T) {
	cases := []struct {
		minutes int
		rate    int64
		want    int64
	}{
		{60 * 60, 3500, 210000},   // invoice 1: 60h x $35 = $2,100
		{19*60 + 30, 3500, 68250}, // invoice 2: 19.5h x $35 = $682.50
		{128 * 60, 2000, 256000},  // invoice 5: 128 man-hours x $20
		{3*60 + 35, 4000, 14333},  // 3.5833h x $40 = $143.33 (spec homes street cleaning)
		{1, 2000, 33},             // 1 minute at $20/h = 33.33c -> 33c
		{0, 2000, 0},
	}
	for _, c := range cases {
		if got := Amount(c.minutes, c.rate); got != c.want {
			t.Errorf("Amount(%d, %d) = %d, want %d", c.minutes, c.rate, got, c.want)
		}
	}
}

func TestBidJobProfit(t *testing.T) {
	// Hyrum lot 117: bid $4,170, 97.75 man-hours at $20, 20% share -> profit $2,215, share $443.
	p := BidJobProfit(417000, 97*60+45, 2000, 2000)
	if p.LaborCostCents != 195500 || p.ProfitCents != 221500 || p.ProfitShareCents != 44300 {
		t.Fatalf("lot 117: %+v", p)
	}
	// Jake's pay on lot 117 = 32.25h x $20 + $443 = $1,088.
	if pay := Amount(32*60+15, 2000) + p.ProfitShareCents; pay != 108800 {
		t.Fatalf("lot 117 Jake pay = %d, want 108800", pay)
	}
	// Smithfield 101/102: bid $8,085, 135h -> profit $5,385, share $1,077.
	p = BidJobProfit(808500, 135*60, 2000, 2000)
	if p.ProfitCents != 538500 || p.ProfitShareCents != 107700 {
		t.Fatalf("101/102: %+v", p)
	}
	// Finish carpentry lot 108 at 25%: bid $4,180, 56h10m -> profit $3,056.67, share $764.17.
	p = BidJobProfit(418000, 56*60+10, 2000, 2500)
	if p.ProfitCents != 305667 || p.ProfitShareCents != 76417 {
		t.Fatalf("108 finish: %+v", p)
	}
	// Losing job: no share.
	if p := BidJobProfit(10000, 60*10, 2000, 2000); p.ProfitCents != -10000 || p.ProfitShareCents != 0 {
		t.Fatalf("loss: %+v", p)
	}
}

func TestParseClock(t *testing.T) {
	ok := map[string]int{
		"7": 420, "7:00": 420, "07:30": 450, "730": 450, "1530": 930, "7am": 420, "7:30 AM": 450,
		"12pm": 720, "12:15am": 15, "3:15 pm": 915, "15:15": 915, "0:00": 0, "11:59pm": 1439, "7a": 420, "4p": 960,
	}
	for in, want := range ok {
		got, err := ParseClock(in)
		if err != nil || got != want {
			t.Errorf("ParseClock(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "25:00", "13pm", "7:5", "7:60", "abc", "0am"} {
		if _, err := ParseClock(in); err == nil {
			t.Errorf("ParseClock(%q) should fail", in)
		}
	}
}

func TestWeekStart(t *testing.T) {
	for in, want := range map[string]string{
		"2026-04-27": "2026-04-27", // Monday
		"2026-05-02": "2026-04-27", // Saturday
		"2026-05-03": "2026-04-27", // Sunday
		"2026-05-04": "2026-05-04",
	} {
		d, _ := time.Parse(DateLayout, in)
		if got := WeekStart(d).Format(DateLayout); got != want {
			t.Errorf("WeekStart(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestSplitShift(t *testing.T) {
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	segs, err := SplitShift(at("2026-05-04 07:00"), at("2026-05-04 18:30"), 30)
	if err != nil || len(segs) != 1 || segs[0] != (Segment{"2026-05-04", 420, 1110, 30}) {
		t.Fatalf("same day: %+v %v", segs, err)
	}
	segs, err = SplitShift(at("2026-05-04 20:00"), at("2026-05-05 02:00"), 15)
	if err != nil || len(segs) != 2 || segs[0] != (Segment{"2026-05-04", 1200, 1440, 15}) || segs[1] != (Segment{"2026-05-05", 0, 120, 0}) {
		t.Fatalf("overnight: %+v %v", segs, err)
	}
	segs, err = SplitShift(at("2026-05-04 20:00"), at("2026-05-05 00:00"), 0)
	if err != nil || len(segs) != 1 || segs[0].End != 1440 {
		t.Fatalf("to midnight: %+v %v", segs, err)
	}
	if _, err := SplitShift(at("2026-05-04 07:00"), at("2026-05-04 07:00"), 0); err == nil {
		t.Fatal("zero-length shift should fail")
	}
	if _, err := SplitShift(at("2026-05-04 07:00"), at("2026-05-04 07:20"), 30); err == nil {
		t.Fatal("break longer than shift should fail")
	}
}
