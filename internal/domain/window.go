package domain

import (
	"math"
	"sort"
	"strconv"
	"time"
)

// adjustmentEpsilon absorbs floating point rounding at the legal boundary so
// that used + candidate == limit is accepted exactly.
const adjustmentEpsilon = 1e-9

// AdjustmentFact is one effective execution fact: a command whose movement has
// settled inside the current window. Only completed commands count; pending,
// approved, executing and aborted commands never consume the budget.
type AdjustmentFact struct {
	CommandID   string
	StartingAt  float64
	Target      float64
	Amount      float64
	CompletedAt time.Time
	ReleasedAt  time.Time
}

// AdjustmentWindow is the explainable projection of one gate's cumulative
// adjustment budget for a duty period.
type AdjustmentWindow struct {
	GateID            string
	Window            time.Duration
	MaxAdjustment     float64
	Enabled           bool
	At                time.Time
	WindowStart       time.Time
	Facts             []AdjustmentFact
	Used              float64
	Remaining         float64
	NextReleaseAt     time.Time
	NextReleaseAmount float64
}

// AdjustmentAmount is the structural travel of one command, counted the same
// for raising and lowering the gate.
func AdjustmentAmount(starting, target float64) float64 {
	return math.Abs(target - starting)
}

// AdjustmentWindowExceeded reports the stable conflict raised when a new request
// or clear review would push the window past its safety limit.
func AdjustmentWindowExceeded(window AdjustmentWindow, candidate float64) error {
	return Conflict(
		"adjustment_window_exceeded",
		"cumulative adjustment "+formatAmount(window.Used)+" plus requested "+
			formatAmount(candidate)+" exceeds the window safety limit "+
			formatAmount(window.MaxAdjustment),
	)
}

func formatAmount(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// CheckAdjustmentWindow computes the window from committed execution facts and
// decides whether candidateAdjustment may be admitted at time now.
func CheckAdjustmentWindow(gate Gate, facts []AdjustmentFact, candidate float64, now time.Time) (AdjustmentWindow, error) {
	window := BuildAdjustmentWindow(gate, facts, now)
	if !window.Enabled {
		return window, nil
	}
	if window.Used+candidate > window.MaxAdjustment+adjustmentEpsilon {
		return window, AdjustmentWindowExceeded(window, candidate)
	}
	return window, nil
}

// BuildAdjustmentWindow projects the current budget from raw facts, dropping
// anything that has already aged out and ordering the survivors by completion.
func BuildAdjustmentWindow(gate Gate, facts []AdjustmentFact, now time.Time) AdjustmentWindow {
	now = now.UTC()
	window := AdjustmentWindow{
		GateID:        gate.ID,
		Window:        gate.AdjustmentWindow,
		MaxAdjustment: gate.MaxWindowAdjustment,
		Enabled:       gate.AdjustmentWindow > 0 && gate.MaxWindowAdjustment > 0,
		At:            now,
	}
	if !window.Enabled {
		window.Facts = []AdjustmentFact{}
		return window
	}

	window.WindowStart = now.Add(-gate.AdjustmentWindow)
	for _, fact := range facts {
		completedAt := fact.CompletedAt.UTC()
		if completedAt.Before(window.WindowStart) {
			continue
		}
		fact.CompletedAt = completedAt
		fact.ReleasedAt = completedAt.Add(gate.AdjustmentWindow)
		window.Facts = append(window.Facts, fact)
		window.Used += fact.Amount
	}

	sort.SliceStable(window.Facts, func(i, j int) bool {
		if window.Facts[i].CompletedAt.Equal(window.Facts[j].CompletedAt) {
			return window.Facts[i].CommandID < window.Facts[j].CommandID
		}
		return window.Facts[i].CompletedAt.Before(window.Facts[j].CompletedAt)
	})

	window.Remaining = gate.MaxWindowAdjustment - window.Used
	if len(window.Facts) > 0 {
		window.NextReleaseAt = window.Facts[0].ReleasedAt
		window.NextReleaseAmount = window.Facts[0].Amount
	}
	return window
}
