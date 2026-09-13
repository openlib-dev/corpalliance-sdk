package capay

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTimeDecodesEveryObservedLayout(t *testing.T) {
	cases := map[string]time.Time{
		`"2026-09-11T05:59:48.000Z"`:       time.Date(2026, 9, 11, 5, 59, 48, 0, time.UTC),
		`"2023-12-12T01:23:33Z"`:           time.Date(2023, 12, 12, 1, 23, 33, 0, time.UTC),
		`"2026-09-11 05:59:48"`:            time.Date(2026, 9, 11, 5, 59, 48, 0, time.UTC), // live transactionTime
		`"2026-09-11T05:59:48+10:00"`:      time.Date(2026, 9, 10, 19, 59, 48, 0, time.UTC),
		`"2026-06-16T00:00:00.000Z"`:       time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC),
		`"2026-09-11"`:                     time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC),
		`"2026-09-11T05:59:48.123456789Z"`: time.Date(2026, 9, 11, 5, 59, 48, 123456789, time.UTC),
	}
	for in, want := range cases {
		var got Time
		if err := json.Unmarshal([]byte(in), &got); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("%s: got %v, want %v", in, got.Time, want)
		}
	}
}

func TestTimeKeepsUnparseableRaw(t *testing.T) {
	var got Time
	if err := json.Unmarshal([]byte(`"next tuesday"`), &got); err != nil {
		t.Fatalf("an unknown layout must not fail the decode: %v", err)
	}
	if !got.IsZero() || got.Raw != "next tuesday" {
		t.Errorf("got %+v", got)
	}
	var null Time
	if err := json.Unmarshal([]byte(`null`), &null); err != nil || !null.IsZero() {
		t.Errorf("null: %v %+v", err, null)
	}
}

func TestDateRoundTrip(t *testing.T) {
	type doc struct {
		Required Date `json:"required"`
		Optional Date `json:"optional,omitzero"`
	}
	b, err := json.Marshal(doc{Required: NewDate(2024, 7, 30)})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"required":"2024-07-30"}` {
		t.Errorf("marshal = %s", b)
	}

	var back doc
	if err := json.Unmarshal([]byte(`{"required":"2024-07-30","optional":null}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.Required.String() != "2024-07-30" || !back.Optional.IsZero() {
		t.Errorf("unmarshal = %+v", back)
	}

	// A quotation's targetDate arrives as a full timestamp; the date part is
	// what matters.
	var d Date
	if err := json.Unmarshal([]byte(`"2024-08-05T00:00:00.000Z"`), &d); err != nil {
		t.Fatal(err)
	}
	if d.String() != "2024-08-05" {
		t.Errorf("date from timestamp = %s", d)
	}

	if err := json.Unmarshal([]byte(`"30/07/2024"`), &d); err == nil {
		t.Error("a non-ISO date must be rejected")
	}
}

func TestPagePagination(t *testing.T) {
	p := Page[int]{Meta: Meta{Skip: 20, Limit: 10, TotalCount: 25}, Data: []int{1, 2, 3, 4, 5}}
	if p.HasMore() {
		t.Error("20+5 == 25: no more pages")
	}
	p.Meta.TotalCount = 26
	if !p.HasMore() || p.NextSkip() != 25 {
		t.Errorf("HasMore=%v NextSkip=%d", p.HasMore(), p.NextSkip())
	}
}

func TestQueryBuilderSkipsZeroValues(t *testing.T) {
	q := newQuery().
		page(ListOptions{}).
		str("a", "").
		str("b", "x").
		date("d", Date{}).
		time("t", time.Time{}).
		values()
	if q.Encode() != "b=x" {
		t.Errorf("query = %q", q.Encode())
	}

	q = newQuery().page(ListOptions{Limit: 50}).values()
	if q.Get("skip") != "0" || q.Get("limit") != "50" {
		t.Errorf("query = %q", q.Encode())
	}

	q = newQuery().time("t", time.Date(2026, 1, 2, 3, 4, 5, 600_000_000, time.FixedZone("x", 3600))).values()
	if q.Get("t") != "2026-01-02T02:04:05.600Z" {
		t.Errorf("time = %q, want ISO 8601 UTC with milliseconds", q.Get("t"))
	}
}
