package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// runAdjustmentWindow verifies the rolling aperture-adjustment window:
//   - the legal boundary is inclusive and explainable via GET .../adjustment-window
//   - request-time and review-time overflow are rejected with a stable code
//   - rejected requests reserve nothing, issue no token, and bump no version
//   - only completed commands consume travel; aborted commands do not
//   - travel is genuinely released as the window rolls while history survives
//   - concurrent review+execute cannot over-admit past the safety limit
func runAdjustmentWindow(client *apiClient) error {
	if err := verifyWindowBoundaryAndReview(client); err != nil {
		return err
	}
	if err := verifyWindowRelease(client); err != nil {
		return err
	}
	if err := verifyAbortedNotCounted(client); err != nil {
		return err
	}
	if err := verifyConcurrentWindow(client); err != nil {
		return err
	}

	fmt.Println("adjustment-window: boundary, review rejection, release, abort and concurrency verified")
	return nil
}

func verifyWindowBoundaryAndReview(client *apiClient) error {
	if _, err := fixtureWindowGate(client, "gate-window-a", "窗口闸A", 10, 0.0, 300, 1.0); err != nil {
		return err
	}

	window, err := getWindow(client, "gate-window-a")
	if err != nil {
		return err
	}
	if err := expectWindow(window, 1.0, 0.0, 1.0, 0); err != nil {
		return err
	}

	// First effective command consumes exactly 0.5.
	first, err := fixtureCommand(client, "gate-window-a", 0.5, "窗口首轮调整", "dispatcher-lin")
	if err != nil {
		return err
	}
	firstID, err := stringField(first, "id")
	if err != nil {
		return err
	}
	// A pending command does not consume travel: admission is based on
	// effective execution facts only.
	window, err = getWindow(client, "gate-window-a")
	if err != nil {
		return err
	}
	if err := expectWindow(window, 1.0, 0.0, 1.0, 0); err != nil {
		return fmt.Errorf("pending command consumed window travel: %w", err)
	}

	executed, err := approveAndExecute(client, firstID, "safety-wu", false)
	if err != nil {
		return err
	}
	if err := requireStatus(executed, http.StatusOK, "execute first window command"); err != nil {
		return err
	}
	window, err = getWindow(client, "gate-window-a")
	if err != nil {
		return err
	}
	if err := expectWindow(window, 1.0, 0.5, 0.5, 1); err != nil {
		return err
	}

	// Request-time rejection: 0.5 used + 0.6 requested would reach 1.1.
	before, err := client.get("/gates/gate-window-a")
	if err != nil {
		return err
	}
	beforeGate, _ := objectField(before.Body, "gate")
	beforeVersion, _ := floatField(beforeGate, "version")

	rejected, err := createCommandRaw(client, "gate-window-a", 1.1)
	if err != nil {
		return err
	}
	if err := requireStatus(rejected, http.StatusConflict, "reject overflowing request"); err != nil {
		return err
	}
	if err := requireErrorCode(rejected.Body, "adjustment_window_exceeded"); err != nil {
		return err
	}
	after, err := client.get("/gates/gate-window-a")
	if err != nil {
		return err
	}
	afterGate, _ := objectField(after.Body, "gate")
	afterVersion, _ := floatField(afterGate, "version")
	if afterVersion != beforeVersion {
		return fmt.Errorf("rejected request bumped gate version: %v -> %v", beforeVersion, afterVersion)
	}
	window, err = getWindow(client, "gate-window-a")
	if err != nil {
		return err
	}
	if err := expectWindow(window, 1.0, 0.5, 0.5, 1); err != nil {
		return fmt.Errorf("rejected request altered window facts: %w", err)
	}

	// Two pending commands each of 0.5 are individually legal against 0.5 used
	// (boundary is inclusive), but only the first one to execute can take effect.
	second, err := fixtureCommand(client, "gate-window-a", 1.0, "窗口边界命令", "dispatcher-lin")
	if err != nil {
		return err
	}
	secondID, err := stringField(second, "id")
	if err != nil {
		return err
	}
	third, err := fixtureCommand(client, "gate-window-a", 1.0, "窗口后到命令", "dispatcher-lin")
	if err != nil {
		return err
	}
	thirdID, err := stringField(third, "id")
	if err != nil {
		return err
	}

	executed, err = approveAndExecute(client, secondID, "safety-wu", false)
	if err != nil {
		return err
	}
	if err := requireStatus(executed, http.StatusOK, "execute boundary command"); err != nil {
		return err
	}
	window, err = getWindow(client, "gate-window-a")
	if err != nil {
		return err
	}
	if err := expectWindow(window, 1.0, 1.0, 0.0, 2); err != nil {
		return err
	}

	// Review-time rejection: the third command was legal when requested but the
	// window is now full at review time.
	reviewRejected, err := client.post("/commands/"+thirdID+"/interlock", map[string]any{
		"verdict":  "clear",
		"verifier": "safety-wu",
		"note":     "窗口复核超额",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(reviewRejected, http.StatusConflict, "reject overflowing clear review"); err != nil {
		return err
	}
	if err := requireErrorCode(reviewRejected.Body, "adjustment_window_exceeded"); err != nil {
		return err
	}

	// No reservation, no credential, no version advancement.
	stored, err := client.get("/commands/" + thirdID)
	if err != nil {
		return err
	}
	if err := requireStatus(stored, http.StatusOK, "read rejected-review command"); err != nil {
		return err
	}
	thirdView, err := objectField(stored.Body, "command")
	if err != nil {
		return err
	}
	status, err := stringField(thirdView, "status")
	if err != nil {
		return err
	}
	version, err := floatField(thirdView, "version")
	if err != nil {
		return err
	}
	if status != "pending" || version != 1 {
		return fmt.Errorf("rejected review mutated command: status=%q version=%v", status, version)
	}
	if token, _ := stringField(thirdView, "execution_token"); token != "" {
		return fmt.Errorf("rejected review issued an execution token: %q", token)
	}
	gateResult, err := client.get("/gates/gate-window-a")
	if err != nil {
		return err
	}
	gateView, _ := objectField(gateResult.Body, "gate")
	gateStatus, _ := stringField(gateView, "status")
	active, _ := stringField(gateView, "active_command_id")
	if gateStatus != "idle" || active != "" {
		return fmt.Errorf("rejected review reserved gate: status=%q active=%q", gateStatus, active)
	}
	window, err = getWindow(client, "gate-window-a")
	if err != nil {
		return err
	}
	if err := expectWindow(window, 1.0, 1.0, 0.0, 2); err != nil {
		return fmt.Errorf("rejected review altered window facts: %w", err)
	}

	// A blocked verdict is not an effective adjustment and must still be
	// recordable even when the window is full.
	blocked, err := client.post("/commands/"+thirdID+"/interlock", map[string]any{
		"verdict":  "blocked",
		"verifier": "safety-wu",
		"note":     "窗口已满，阻塞该命令",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(blocked, http.StatusOK, "blocked review while window full"); err != nil {
		return err
	}
	return nil
}

func verifyWindowRelease(client *apiClient) error {
	if _, err := fixtureWindowGate(client, "gate-window-b", "窗口闸B", 10, 0.0, 2, 1.0); err != nil {
		return err
	}
	first, err := fixtureCommand(client, "gate-window-b", 0.6, "短窗口首轮", "dispatcher-yang")
	if err != nil {
		return err
	}
	firstID, err := stringField(first, "id")
	if err != nil {
		return err
	}
	executed, err := approveAndExecute(client, firstID, "safety-he", false)
	if err != nil {
		return err
	}
	if err := requireStatus(executed, http.StatusOK, "execute short-window command"); err != nil {
		return err
	}

	rejected, err := createCommandRaw(client, "gate-window-b", 1.3)
	if err != nil {
		return err
	}
	if err := requireStatus(rejected, http.StatusConflict, "reject command before window rolls"); err != nil {
		return err
	}

	// Wait for the rolling window to genuinely release.
	time.Sleep(2100 * time.Millisecond)

	window, err := getWindow(client, "gate-window-b")
	if err != nil {
		return err
	}
	if err := expectWindow(window, 1.0, 0.0, 1.0, 0); err != nil {
		return fmt.Errorf("window did not release: %w", err)
	}

	// Completed history remains explainable even after it left the window.
	oldResult, err := client.get("/commands/" + firstID)
	if err != nil {
		return err
	}
	if err := requireStatus(oldResult, http.StatusOK, "read aged-out completed command"); err != nil {
		return err
	}
	oldView, err := objectField(oldResult.Body, "command")
	if err != nil {
		return err
	}
	oldStatus, err := stringField(oldView, "status")
	if err != nil {
		return err
	}
	if oldStatus != "completed" {
		return fmt.Errorf("aged-out command history changed: %q", oldStatus)
	}

	// The same request that was refused before is now admitted.
	released, err := createCommandRaw(client, "gate-window-b", 1.3)
	if err != nil {
		return err
	}
	return requireStatus(released, http.StatusCreated, "admit command after window rolls")
}

func verifyAbortedNotCounted(client *apiClient) error {
	if _, err := fixtureWindowGate(client, "gate-window-c", "窗口闸C", 10, 0.0, 300, 1.0); err != nil {
		return err
	}
	command, err := fixtureCommand(client, "gate-window-c", 0.8, "故障模拟命令", "dispatcher-zhao")
	if err != nil {
		return err
	}
	commandID, err := stringField(command, "id")
	if err != nil {
		return err
	}
	fault, err := approveAndExecute(client, commandID, "safety-he", true)
	if err != nil {
		return err
	}
	if err := requireStatus(fault, http.StatusOK, "execute simulated fault"); err != nil {
		return err
	}
	faultCommand, err := objectField(fault.Body, "command")
	if err != nil {
		return err
	}
	status, err := stringField(faultCommand, "status")
	if err != nil {
		return err
	}
	if status != "aborted" {
		return fmt.Errorf("fault execution status: expected aborted, got %q", status)
	}

	window, err := getWindow(client, "gate-window-c")
	if err != nil {
		return err
	}
	return expectWindow(window, 1.0, 0.0, 1.0, 0)
}

func verifyConcurrentWindow(client *apiClient) error {
	if _, err := fixtureWindowGate(client, "gate-window-d", "窗口闸D", 10, 0.0, 300, 1.0); err != nil {
		return err
	}

	// Pre-consume 0.6 of the 1.0 limit with two effective commands.
	primer, err := fixtureCommand(client, "gate-window-d", 0.3, "并发预热一", "dispatcher-fan")
	if err != nil {
		return err
	}
	primerID, err := stringField(primer, "id")
	if err != nil {
		return err
	}
	executed, err := approveAndExecute(client, primerID, "safety-fan", false)
	if err != nil {
		return err
	}
	if err := requireStatus(executed, http.StatusOK, "execute first primer"); err != nil {
		return err
	}
	primer2, err := fixtureCommand(client, "gate-window-d", 0.6, "并发预热二", "dispatcher-fan")
	if err != nil {
		return err
	}
	primer2ID, err := stringField(primer2, "id")
	if err != nil {
		return err
	}
	executed, err = approveAndExecute(client, primer2ID, "safety-fan", false)
	if err != nil {
		return err
	}
	if err := requireStatus(executed, http.StatusOK, "execute second primer"); err != nil {
		return err
	}
	window, err := getWindow(client, "gate-window-d")
	if err != nil {
		return err
	}
	if err := expectWindow(window, 1.0, 0.6, 0.4, 2); err != nil {
		return err
	}

	// Two candidates each need 0.3; only one fits in the remaining 0.4.
	var ids []string
	for range 2 {
		command, err := fixtureCommand(client, "gate-window-d", 0.9, "并发边界命令", "dispatcher-fan")
		if err != nil {
			return err
		}
		id, err := stringField(command, "id")
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}

	outcomes := make([]raceOutcome, len(ids))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, id := range ids {
		wg.Add(1)
		go func(index int, commandID string) {
			defer wg.Done()
			<-start
			outcomes[index] = raceClearAndExecute(client, commandID)
		}(i, id)
	}
	close(start)
	wg.Wait()

	var ok, overflow int
	for _, outcome := range outcomes {
		switch outcome.code {
		case "":
			ok++
		case "adjustment_window_exceeded":
			overflow++
		default:
			return fmt.Errorf("concurrent window race returned unexpected code %q", outcome.code)
		}
	}
	if ok != 1 || overflow != 1 {
		return fmt.Errorf("concurrent window admission: %d executed, %d window-rejected (expected 1 and 1)", ok, overflow)
	}

	window, err = getWindow(client, "gate-window-d")
	if err != nil {
		return err
	}
	if err := expectWindow(window, 1.0, 0.9, 0.1, 3); err != nil {
		return fmt.Errorf("concurrent window over-admitted: %w", err)
	}
	return nil
}

// raceOutcome records the terminal result of one concurrent candidate.
type raceOutcome struct {
	code string
}

// raceClearAndExecute clears and executes one candidate. A transient
// gate_not_idle conflict means another racer currently holds the gate; the
// review is retried so the eventual decision reflects the window boundary
// rather than lock timing. adjustment_window_exceeded is terminal.
func raceClearAndExecute(client *apiClient, commandID string) raceOutcome {
	const attempts = 50
	for attempt := 0; attempt < attempts; attempt++ {
		review, err := client.post("/commands/"+commandID+"/interlock", map[string]any{
			"verdict":  "clear",
			"verifier": "safety-fan",
			"note":     "并发窗口复核",
		})
		if err != nil {
			return raceOutcome{code: "transport_error"}
		}
		switch review.Status {
		case http.StatusOK:
			command, err := objectField(review.Body, "command")
			if err != nil {
				return raceOutcome{code: "malformed_response"}
			}
			token, err := stringField(command, "execution_token")
			if err != nil {
				return raceOutcome{code: "malformed_response"}
			}
			executeResult, err := client.post("/commands/"+commandID+"/execute", map[string]any{
				"operator":       "operator-chen",
				"token":          token,
				"simulate_fault": false,
			})
			if err != nil || executeResult.Status != http.StatusOK {
				return raceOutcome{code: "execute_failed"}
			}
			return raceOutcome{}
		case http.StatusConflict:
			code, _ := review.Body["code"].(string)
			if code == "adjustment_window_exceeded" {
				return raceOutcome{code: code}
			}
			if code == "gate_not_idle" || code == "gate_already_reserved" {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return raceOutcome{code: code}
		default:
			return raceOutcome{code: fmt.Sprintf("http_%d", review.Status)}
		}
	}
	return raceOutcome{code: "retry_exhausted"}
}

func expectWindow(
	window map[string]any,
	maxTravel, usedTravel, remaining float64,
	facts int,
) error {
	enabled, _ := window["enabled"].(bool)
	if !enabled {
		return fmt.Errorf("window should be enabled: %s", prettyBody(window))
	}
	gotMax, err := floatField(window, "max_travel")
	if err != nil {
		return err
	}
	gotUsed, err := floatField(window, "used_travel")
	if err != nil {
		return err
	}
	gotRemaining, err := floatField(window, "remaining_travel")
	if err != nil {
		return err
	}
	rawFacts, _ := window["facts"].([]any)
	if !floatNearlyEqual(gotMax, maxTravel) ||
		!floatNearlyEqual(gotUsed, usedTravel) ||
		!floatNearlyEqual(gotRemaining, remaining) ||
		len(rawFacts) != facts {
		return fmt.Errorf(
			"window mismatch: max=%v used=%v remaining=%v facts=%d (want %v %v %v %d) body=%s",
			gotMax, gotUsed, gotRemaining, len(rawFacts),
			maxTravel, usedTravel, remaining, facts, prettyBody(window),
		)
	}
	return nil
}

func requireErrorCode(body map[string]any, expected string) error {
	code, err := stringField(body, "code")
	if err != nil {
		return err
	}
	if code != expected {
		return fmt.Errorf("error code: expected %q, got %q (%s)", expected, code, prettyBody(body))
	}
	return nil
}

func floatNearlyEqual(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-9
}
