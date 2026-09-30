package domain

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

const (
	// adjustmentTolerance keeps boundary arithmetic explainable when requests
	// land exactly on the configured limit after float addition.
	adjustmentTolerance = 1e-9
	// maxWindowSeconds bounds a duty period to at most one year.
	maxWindowSeconds = 365 * 24 * 60 * 60
)

// AdjustmentWindow limits the cumulative aperture travel that may take effect
// during one rolling duty period. The zero value disables window enforcement.
type AdjustmentWindow struct {
	Duration      time.Duration
	MaxAdjustment float64
}

// NewAdjustmentWindow builds a window from a duration expressed in seconds.
// Duration and limit must be configured together; both zero means disabled.
func NewAdjustmentWindow(durationSeconds int64, maxAdjustment float64) (AdjustmentWindow, error) {
	if durationSeconds < 0 || durationSeconds > maxWindowSeconds {
		return AdjustmentWindow{}, Invalid(
			"invalid_adjustment_window",
			"window duration must be between 0 and 31536000 seconds",
		)
	}
	if math.IsNaN(maxAdjustment) || math.IsInf(maxAdjustment, 0) || maxAdjustment < 0 {
		return AdjustmentWindow{}, Invalid(
			"invalid_adjustment_window",
			"window adjustment limit must be a finite non-negative number",
		)
	}
	if (durationSeconds == 0) != (maxAdjustment == 0) {
		return AdjustmentWindow{}, Invalid(
			"invalid_adjustment_window",
			"window duration and adjustment limit must be configured together",
		)
	}
	return AdjustmentWindow{
		Duration:      time.Duration(durationSeconds) * time.Second,
		MaxAdjustment: maxAdjustment,
	}, nil
}

func (w AdjustmentWindow) Enabled() bool {
	return w.Duration > 0 && w.MaxAdjustment > 0
}

// Start is the inclusive lower bound of the rolling window.
func (w AdjustmentWindow) Start(now time.Time) time.Time {
	return now.UTC().Add(-w.Duration)
}

// Admits reports whether a candidate adjustment may take effect on top of the
// travel already consumed inside the window. The legal boundary is inclusive:
// a request whose cumulative travel lands exactly on the limit is admitted.
func (w AdjustmentWindow) Admits(candidate, used float64) bool {
	if !w.Enabled() {
		return true
	}
	return used+candidate <= w.MaxAdjustment+adjustmentTolerance
}

// Exceeded builds the stable conflict reported when admission is denied.
func (w AdjustmentWindow) Exceeded(candidate, used float64) error {
	return Conflict(
		"adjustment_window_exceeded",
		fmt.Sprintf(
			"adjustment window exceeded: requested %s on top of %s already executed would reach %s, limit %s",
			formatAperture(candidate),
			formatAperture(used),
			formatAperture(used+candidate),
			formatAperture(w.MaxAdjustment),
		),
	)
}

func formatAperture(value float64) string {
	return strconv.FormatFloat(value, 'f', 3, 64)
}

// AdjustmentFact is one effective execution: a command that actually completed
// and moved the gate by ApertureDelta. Pending, rejected and aborted commands
// never become facts.
type AdjustmentFact struct {
	CommandID        string
	StartingAperture float64
	TargetAperture   float64
	ApertureDelta    float64
	CompletedAt      time.Time
}

// AdjustmentWindowStatus is an explainable projection of the rolling window
// for one gate: the configured boundary, how much travel has genuinely been
// consumed by completed facts, and the facts themselves.
type AdjustmentWindowStatus struct {
	GateID      string
	Window      AdjustmentWindow
	EvaluatedAt time.Time
	WindowStart time.Time
	Used        float64
	Facts       []AdjustmentFact
}

// NewAdjustmentWindowStatus computes window usage purely from effective
// execution facts. Commands that completed at or after the inclusive window
// start contribute their frozen aperture delta.
func NewAdjustmentWindowStatus(gate Gate, commands []GateCommand, now time.Time) AdjustmentWindowStatus {
	now = now.UTC()
	status := AdjustmentWindowStatus{
		GateID:      gate.ID,
		Window:      gate.AdjustmentWindow,
		EvaluatedAt: now,
		Facts:       []AdjustmentFact{},
	}
	if !gate.AdjustmentWindow.Enabled() {
		return status
	}

	status.WindowStart = gate.AdjustmentWindow.Start(now)
	for _, command := range commands {
		if command.GateID != gate.ID || command.Status != CommandCompleted {
			continue
		}
		completedAt := command.CompletedAt
		if completedAt.IsZero() {
			completedAt = command.UpdatedAt
		}
		completedAt = completedAt.UTC()
		if completedAt.Before(status.WindowStart) || completedAt.After(now) {
			continue
		}
		status.Facts = append(status.Facts, AdjustmentFact{
			CommandID:        command.ID,
			StartingAperture: command.StartingAperture,
			TargetAperture:   command.TargetAperture,
			ApertureDelta:    AdjustmentDelta(command.StartingAperture, command.TargetAperture),
			CompletedAt:      completedAt,
		})
	}
	sort.Slice(status.Facts, func(i, j int) bool {
		if status.Facts[i].CompletedAt.Equal(status.Facts[j].CompletedAt) {
			return status.Facts[i].CommandID < status.Facts[j].CommandID
		}
		return status.Facts[i].CompletedAt.Before(status.Facts[j].CompletedAt)
	})
	for _, fact := range status.Facts {
		status.Used += fact.ApertureDelta
	}
	return status
}

// Headroom is the travel still genuinely available inside the window.
func (s AdjustmentWindowStatus) Headroom() float64 {
	if !s.Window.Enabled() {
		return 0
	}
	remaining := s.Window.MaxAdjustment - s.Used
	if math.Abs(remaining) <= adjustmentTolerance {
		return 0
	}
	if remaining < 0 {
		return 0
	}
	return remaining
}

// AdjustmentDelta is the travel a command would consume: the absolute distance
// between its frozen starting aperture and its target aperture.
func AdjustmentDelta(startingAperture, targetAperture float64) float64 {
	return math.Abs(targetAperture - startingAperture)
}
