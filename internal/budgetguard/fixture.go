// fixture.go — replay fixture validation (finding C).
//
// Replay used to json.Unmarshal straight into two SlotCounts structs:
// missing fields defaulted to 0 and negative counts were accepted, so a
// malformed fixture could decide PASS. A fixture is evidence — it is
// validated before any gate runs: required fields present, finite numbers,
// and 0 <= bad <= slow_or_bad <= eligible. Fractional counts (Prometheus
// increase() extrapolates) are preserved for gating, never rounded first.
package budgetguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// FixtureCounts is one validated slot from a replay fixture.
type FixtureCounts struct {
	Eligible float64
	Bad      float64
	SlowBad  float64
}

// counts converts to raw gate counts (H6: gate on the raw fractions).
func (f FixtureCounts) counts() Counts {
	return Counts{
		Eligible: f.Eligible,
		Good:     f.Eligible - f.Bad,
		FastGood: f.Eligible - f.SlowBad,
	}
}

// display renders the integer report counts (rounding at the JSON edge only).
func (f FixtureCounts) display() SlotCounts {
	return SlotCounts{
		Eligible: int64(f.Eligible + 0.5),
		Bad:      int64(f.Bad + 0.5),
		SlowBad:  int64(f.SlowBad + 0.5),
	}
}

// fractional reports whether any count carries a fraction.
func (f FixtureCounts) fractional() bool {
	for _, v := range []float64{f.Eligible, f.Bad, f.SlowBad} {
		if v != math.Trunc(v) {
			return true
		}
	}
	return false
}

// ParseFixture validates {"stable": {...}, "candidate": {...}} and returns
// both slots. Every error names the slot and field that failed.
func ParseFixture(raw []byte) (FixtureCounts, FixtureCounts, error) {
	var top map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&top); err != nil {
		return FixtureCounts{}, FixtureCounts{}, fmt.Errorf("fixture: %w", err)
	}
	var stable, cand FixtureCounts
	var err error
	if stable, err = parseSlot("stable", top["stable"]); err != nil {
		return FixtureCounts{}, FixtureCounts{}, err
	}
	if cand, err = parseSlot("candidate", top["candidate"]); err != nil {
		return FixtureCounts{}, FixtureCounts{}, err
	}
	return stable, cand, nil
}

func parseSlot(name string, raw json.RawMessage) (FixtureCounts, error) {
	var f FixtureCounts
	if len(raw) == 0 {
		return f, fmt.Errorf("fixture: missing required slot %q", name)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return f, fmt.Errorf("fixture %s: %w", name, err)
	}
	var err error
	if f.Eligible, err = numberField(name, fields, "eligible"); err != nil {
		return f, err
	}
	if f.Bad, err = numberField(name, fields, "bad"); err != nil {
		return f, err
	}
	if f.SlowBad, err = numberField(name, fields, "slow_or_bad"); err != nil {
		return f, err
	}
	switch {
	case f.Eligible < 0 || f.Bad < 0 || f.SlowBad < 0:
		return f, fmt.Errorf("fixture %s: negative count (eligible=%v bad=%v slow_or_bad=%v)",
			name, f.Eligible, f.Bad, f.SlowBad)
	case f.Bad > f.SlowBad:
		return f, fmt.Errorf("fixture %s: bad %v > slow_or_bad %v", name, f.Bad, f.SlowBad)
	case f.SlowBad > f.Eligible:
		return f, fmt.Errorf("fixture %s: slow_or_bad %v > eligible %v", name, f.SlowBad, f.Eligible)
	}
	return f, nil
}

// numberField decodes one required finite number (integers and fractions).
func numberField(slot string, fields map[string]json.RawMessage, key string) (float64, error) {
	raw, ok := fields[key]
	if !ok {
		return 0, fmt.Errorf("fixture %s: missing required field %q", slot, key)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return 0, fmt.Errorf("fixture %s: %s: %w", slot, key, err)
	}
	if dec.More() {
		return 0, fmt.Errorf("fixture %s: %s: trailing content after value", slot, key)
	}
	num, ok := v.(json.Number)
	if !ok {
		return 0, fmt.Errorf("fixture %s: %s: not a number (%v)", slot, key, v)
	}
	f, err := strconv.ParseFloat(string(num), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("fixture %s: %s: not a finite number: %s", slot, key, num)
	}
	return f, nil
}
