package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

func runAdjustmentWindow(client *apiClient) error {
	if err := verifyWindowBudgetAndBoundary(client); err != nil {
		return err
	}
	if err := verifyAbortedExecutionIsNoFact(client); err != nil {
		return err
	}
	if err := verifyReviewRefusalLeavesNoTrace(client); err != nil {
		return err
	}
	if err := verifyWindowRelease(client); err != nil {
		return err
	}
	if err := verifyReviewWindowRace(client); err != nil {
		return err
	}
	fmt.Println("adjustment-window: budget enforced at request and review, facts release over time, races never over-admit")
	return nil
}

func windowGateBody(id string, initial, maxAperture, limit float64, windowSeconds int64) map[string]any {
	return map[string]any{
		"id":                    id,
		"name":                  "窗口验证闸 " + id,
		"max_aperture":          maxAperture,
		"initial_aperture":      initial,
		"adjustment_window":     windowSeconds,
		"max_window_adjustment": limit,
	}
}

func createWindowGate(client *apiClient, id string, initial, maxAperture, limit float64, windowSeconds int64) error {
	result, err := client.post("/gates", windowGateBody(id, initial, maxAperture, limit, windowSeconds))
	if err != nil {
		return err
	}
	return requireStatus(result, http.StatusCreated, "create windowed gate")
}

func completeAdjustment(client *apiClient, gateID string, target float64, verifier string) (string, error) {
	command, err := fixtureCommand(client, gateID, target, "窗口累计验证", "dispatcher-lin")
	if err != nil {
		return "", err
	}
	commandID, err := stringField(command, "id")
	if err != nil {
		return "", err
	}
	approved, err := fixtureClearReview(client, commandID, verifier)
	if err != nil {
		return "", err
	}
	token, err := stringField(approved, "execution_token")
	if err != nil {
		return "", err
	}
	executed, err := client.post("/commands/"+commandID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          token,
		"simulate_fault": false,
	})
	if err != nil {
		return "", err
	}
	if err := requireStatus(executed, http.StatusOK, "complete window fact"); err != nil {
		return "", err
	}
	return commandID, nil
}

func readWindow(client *apiClient, gateID string) (map[string]any, error) {
	result, err := client.get("/gates/" + gateID + "/adjustment-window")
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusOK, "read adjustment window"); err != nil {
		return nil, err
	}
	return objectField(result.Body, "adjustment_window")
}

func requestAdjustmentExpectStatus(
	client *apiClient,
	gateID string,
	target float64,
	expected int,
	operation string,
) (apiResult, error) {
	result, err := client.post("/commands", map[string]any{
		"gate_id":         gateID,
		"target_aperture": target,
		"reason":          "窗口边界验证",
		"requested_by":    "dispatcher-lin",
	})
	if err != nil {
		return apiResult{}, err
	}
	if err := requireStatus(result, expected, operation); err != nil {
		return apiResult{}, err
	}
	return result, nil
}

func verifyWindowBudgetAndBoundary(client *apiClient) error {
	gateID := "gate-window-a"
	if err := createWindowGate(client, gateID, 0, 10, 3.0, 3600); err != nil {
		return err
	}

	window, err := readWindow(client, gateID)
	if err != nil {
		return err
	}
	if enabled, _ := window["enabled"].(bool); !enabled {
		return fmt.Errorf("fresh window expected enabled=true, got %v", window["enabled"])
	}
	used, err := floatField(window, "used")
	if err != nil {
		return err
	}
	if used != 0 {
		return fmt.Errorf("fresh window used: expected 0, got %v", used)
	}
	remaining, err := floatField(window, "remaining")
	if err != nil {
		return err
	}
	if remaining != 3.0 {
		return fmt.Errorf("fresh window remaining: expected 3, got %v", remaining)
	}
	facts, _ := window["facts"].([]any)
	if len(facts) != 0 {
		return fmt.Errorf("fresh window expected no facts, got %d", len(facts))
	}

	// Three completed facts totalling exactly 2.2.
	if _, err := completeAdjustment(client, gateID, 0.7, "safety-wu"); err != nil {
		return err
	}
	if _, err := completeAdjustment(client, gateID, 1.5, "safety-wu"); err != nil {
		return err
	}
	if _, err := completeAdjustment(client, gateID, 2.2, "safety-wu"); err != nil {
		return err
	}

	window, err = readWindow(client, gateID)
	if err != nil {
		return err
	}
	used, _ = floatField(window, "used")
	if used != 2.2 {
		return fmt.Errorf("window used after facts: expected 2.2, got %v", used)
	}
	facts, _ = window["facts"].([]any)
	if len(facts) != 3 {
		return fmt.Errorf("window facts: expected 3 effective facts, got %d", len(facts))
	}
	if _, err := stringField(window, "next_release_at"); err != nil {
		return fmt.Errorf("window must explain the next real release: %w", err)
	}
	nextReleaseAmount, err := floatField(window, "next_release_amount")
	if err != nil {
		return err
	}
	if nextReleaseAmount != 0.7 {
		return fmt.Errorf("next release amount: expected 0.7 (oldest fact), got %v", nextReleaseAmount)
	}

	// Legal boundary: 2.2 + 0.8 == 3.0 must be admitted through request,
	// review and execution.
	if _, err := completeAdjustment(client, gateID, 3.0, "safety-wu"); err != nil {
		return err
	}
	window, err = readWindow(client, gateID)
	if err != nil {
		return err
	}
	used, _ = floatField(window, "used")
	if used != 3.0 {
		return fmt.Errorf("window used at boundary: expected 3, got %v", used)
	}

	// Anything positive now exceeds the limit and must be refused at request
	// time with the stable conflict code.
	rejected, err := requestAdjustmentExpectStatus(client, gateID, 2.9, http.StatusConflict, "reject over-window request")
	if err != nil {
		return err
	}
	code, err := stringField(rejected.Body, "code")
	if err != nil {
		return err
	}
	if code != "adjustment_window_exceeded" {
		return fmt.Errorf("over-window refusal code: expected adjustment_window_exceeded, got %q", code)
	}

	// The refusal must not have created an occupying command: the gate stays
	// idle without an active command.
	gateResult, err := client.get("/gates/" + gateID)
	if err != nil {
		return err
	}
	gate, err := objectField(gateResult.Body, "gate")
	if err != nil {
		return err
	}
	status, err := stringField(gate, "status")
	if err != nil {
		return err
	}
	if active, _ := gate["active_command_id"].(string); status != "idle" || active != "" {
		return fmt.Errorf("refused request must not reserve gate: status=%q active=%q", status, active)
	}
	return nil
}

func verifyAbortedExecutionIsNoFact(client *apiClient) error {
	gateID := "gate-window-aborted"
	if err := createWindowGate(client, gateID, 0, 10, 3.0, 3600); err != nil {
		return err
	}
	command, err := fixtureCommand(client, gateID, 1.0, "模拟故障不计入窗口", "dispatcher-lin")
	if err != nil {
		return err
	}
	commandID, err := stringField(command, "id")
	if err != nil {
		return err
	}
	approved, err := fixtureClearReview(client, commandID, "safety-he")
	if err != nil {
		return err
	}
	token, err := stringField(approved, "execution_token")
	if err != nil {
		return err
	}
	executed, err := client.post("/commands/"+commandID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          token,
		"simulate_fault": true,
	})
	if err != nil {
		return err
	}
	if err := requireStatus(executed, http.StatusOK, "execute with simulated fault"); err != nil {
		return err
	}
	executedCommand, err := objectField(executed.Body, "command")
	if err != nil {
		return err
	}
	status, err := stringField(executedCommand, "status")
	if err != nil {
		return err
	}
	if status != "aborted" {
		return fmt.Errorf("fault execution status: expected aborted, got %q", status)
	}

	// An aborted command never became an effective execution fact: the window
	// must still be empty even though the gate is latched faulted.
	window, err := readWindow(client, gateID)
	if err != nil {
		return err
	}
	used, err := floatField(window, "used")
	if err != nil {
		return err
	}
	if used != 0 {
		return fmt.Errorf("aborted execution must not count toward the window, used=%v", used)
	}
	facts, _ := window["facts"].([]any)
	if len(facts) != 0 {
		return fmt.Errorf("aborted execution must not appear in window history, got %d facts", len(facts))
	}
	return nil
}

func verifyReviewRefusalLeavesNoTrace(client *apiClient) error {
	gateID := "gate-window-b"
	if err := createWindowGate(client, gateID, 0, 10, 3.0, 3600); err != nil {
		return err
	}
	// Existing effective facts total 2.2.
	if _, err := completeAdjustment(client, gateID, 0.7, "safety-he"); err != nil {
		return err
	}
	if _, err := completeAdjustment(client, gateID, 1.5, "safety-he"); err != nil {
		return err
	}
	if _, err := completeAdjustment(client, gateID, 2.2, "safety-he"); err != nil {
		return err
	}

	// Two pending commands each move 0.8: individually they sit on the legal
	// boundary (2.2 + 0.8 == 3.0) when requested.
	commandA, err := requestAdjustmentExpectStatus(client, gateID, 3.0, http.StatusCreated, "request pending command A")
	if err != nil {
		return err
	}
	aObject, err := objectField(commandA.Body, "command")
	if err != nil {
		return err
	}
	aID, err := stringField(aObject, "id")
	if err != nil {
		return err
	}
	aVersion, ok := aObject["version"].(float64)
	if !ok {
		return fmt.Errorf("created command A version: expected number, got %v", aObject["version"])
	}

	commandB, err := requestAdjustmentExpectStatus(client, gateID, 1.4, http.StatusCreated, "request pending command B")
	if err != nil {
		return err
	}
	bObject, err := objectField(commandB.Body, "command")
	if err != nil {
		return err
	}
	bID, err := stringField(bObject, "id")
	if err != nil {
		return err
	}

	// B takes the last budget and executes; facts now total 3.0.
	approvedB, err := fixtureClearReview(client, bID, "safety-he")
	if err != nil {
		return err
	}
	tokenB, err := stringField(approvedB, "execution_token")
	if err != nil {
		return err
	}
	executedB, err := client.post("/commands/"+bID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          tokenB,
		"simulate_fault": false,
	})
	if err != nil {
		return err
	}
	if err := requireStatus(executedB, http.StatusOK, "execute command B"); err != nil {
		return err
	}

	// A's clear review must now be refused by the window: it cannot reserve the
	// gate, receive a token or advance its version.
	refused, err := client.post("/commands/"+aID+"/interlock", map[string]any{
		"verdict":  "clear",
		"verifier": "safety-he",
		"note":     "窗口复核拒绝验证",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(refused, http.StatusConflict, "reject over-window clear review"); err != nil {
		return err
	}
	code, err := stringField(refused.Body, "code")
	if err != nil {
		return err
	}
	if code != "adjustment_window_exceeded" {
		return fmt.Errorf("review refusal code: expected adjustment_window_exceeded, got %q", code)
	}

	fetched, err := client.get("/commands/" + aID)
	if err != nil {
		return err
	}
	aAfter, err := objectField(fetched.Body, "command")
	if err != nil {
		return err
	}
	aStatus, err := stringField(aAfter, "status")
	if err != nil {
		return err
	}
	if aStatus != "pending" {
		return fmt.Errorf("refused review must leave command pending, got %q", aStatus)
	}
	afterVersion, _ := aAfter["version"].(float64)
	if afterVersion != aVersion {
		return fmt.Errorf("refused review must not advance version: %v -> %v", aVersion, afterVersion)
	}
	if token, _ := aAfter["execution_token"].(string); token != "" {
		return fmt.Errorf("refused review must not issue a token, got %q", token)
	}

	gateResult, err := client.get("/gates/" + gateID)
	if err != nil {
		return err
	}
	gate, err := objectField(gateResult.Body, "gate")
	if err != nil {
		return err
	}
	gateStatus, err := stringField(gate, "status")
	if err != nil {
		return err
	}
	if active, _ := gate["active_command_id"].(string); gateStatus != "idle" || active != "" {
		return fmt.Errorf("refused review must not reserve gate: status=%q active=%q", gateStatus, active)
	}

	// A blocked verdict is a safety rejection, not an effective adjustment, so
	// it remains legal even when the window is full.
	blocked, err := client.post("/commands/"+aID+"/interlock", map[string]any{
		"verdict":  "blocked",
		"verifier": "safety-he",
		"note":     "窗口已满，禁止放行",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(blocked, http.StatusOK, "blocked review still allowed in full window"); err != nil {
		return err
	}
	blockedCommand, err := objectField(blocked.Body, "command")
	if err != nil {
		return err
	}
	blockedStatus, err := stringField(blockedCommand, "status")
	if err != nil {
		return err
	}
	if blockedStatus != "rejected" {
		return fmt.Errorf("blocked review status: expected rejected, got %q", blockedStatus)
	}
	return nil
}

func verifyWindowRelease(client *apiClient) error {
	gateID := "gate-window-c"
	if err := createWindowGate(client, gateID, 0, 10, 2.0, 2); err != nil {
		return err
	}
	if _, err := completeAdjustment(client, gateID, 1.0, "safety-wu"); err != nil {
		return err
	}

	if _, err := requestAdjustmentExpectStatus(
		client, gateID, 2.5, http.StatusConflict, "1.5 more must not fit against used 1.0 / limit 2.0",
	); err != nil {
		return err
	}

	// Real release: once the fact ages out of the sliding window the budget
	// returns in full and history no longer counts the command.
	time.Sleep(2100 * time.Millisecond)
	window, err := readWindow(client, gateID)
	if err != nil {
		return err
	}
	used, err := floatField(window, "used")
	if err != nil {
		return err
	}
	if used != 0 {
		return fmt.Errorf("released window used: expected 0, got %v", used)
	}
	facts, _ := window["facts"].([]any)
	if len(facts) != 0 {
		return fmt.Errorf("released window must drop aged facts, got %d", len(facts))
	}
	remaining, err := floatField(window, "remaining")
	if err != nil {
		return err
	}
	if remaining != 2.0 {
		return fmt.Errorf("released window remaining: expected 2, got %v", remaining)
	}
	if _, err := requestAdjustmentExpectStatus(client, gateID, 1.5, http.StatusCreated, "released window admits new request"); err != nil {
		return err
	}
	return nil
}

func verifyReviewWindowRace(client *apiClient) error {
	// Repeat the admission race: a clear review and the completion of the last
	// budget happen concurrently. Exactly one side may win and the window must
	// never admit cumulative travel beyond the limit.
	// budget happen concurrently. Exactly one side may win and the window must
	// never admit cumulative travel beyond the limit.
	for round := 0; round < 20; round++ {
		gateID := fmt.Sprintf("gate-race-%02d", round)
		if err := createWindowGate(client, gateID, 0, 10, 3.0, 3600); err != nil {
			return err
		}
		if _, err := completeAdjustment(client, gateID, 0.7, "safety-wu"); err != nil {
			return err
		}
		if _, err := completeAdjustment(client, gateID, 1.5, "safety-wu"); err != nil {
			return err
		}
		if _, err := completeAdjustment(client, gateID, 2.2, "safety-wu"); err != nil {
			return err
		}

		// A and B each move 0.8 against used 2.2: each alone is exactly at the
		// boundary; both together would be 3.8 and must never be admitted.
		commandA, err := requestAdjustmentExpectStatus(client, gateID, 3.0, http.StatusCreated, "race request A")
		if err != nil {
			return err
		}
		aObj, err := objectField(commandA.Body, "command")
		if err != nil {
			return err
		}
		aID, err := stringField(aObj, "id")
		if err != nil {
			return err
		}
		commandB, err := requestAdjustmentExpectStatus(client, gateID, 1.4, http.StatusCreated, "race request B")
		if err != nil {
			return err
		}
		bObj, err := objectField(commandB.Body, "command")
		if err != nil {
			return err
		}
		bID, err := stringField(bObj, "id")
		if err != nil {
			return err
		}
		approvedB, err := fixtureClearReview(client, bID, "safety-wu")
		if err != nil {
			return err
		}
		tokenB, err := stringField(approvedB, "execution_token")
		if err != nil {
			return err
		}

		var wg sync.WaitGroup
		wg.Add(2)
		var reviewStatus, executeStatus int
		go func() {
			defer wg.Done()
			result, err := client.post("/commands/"+aID+"/interlock", map[string]any{
				"verdict":  "clear",
				"verifier": "safety-wu",
				"note":     "竞态复核",
			})
			if err == nil {
				reviewStatus = result.Status
			}
		}()
		go func() {
			defer wg.Done()
			result, err := client.post("/commands/"+bID+"/execute", map[string]any{
				"operator":       "operator-chen",
				"token":          tokenB,
				"simulate_fault": false,
			})
			if err == nil {
				executeStatus = result.Status
			}
		}()
		wg.Wait()

		if executeStatus != http.StatusOK {
			return fmt.Errorf("round %d: B execution expected 200, got %d", round, executeStatus)
		}
		if reviewStatus != http.StatusConflict {
			return fmt.Errorf("round %d: A review racing B completion must be a conflict, got %d", round, reviewStatus)
		}

		window, err := readWindow(client, gateID)
		if err != nil {
			return err
		}
		used, err := floatField(window, "used")
		if err != nil {
			return err
		}
		if used > 3.0+1e-9 {
			return fmt.Errorf("round %d: window over-admitted under race, used=%v", round, used)
		}

		// After B's completion fills the window, a serial retry of A must be
		// refused by the window itself.
		retry, err := client.post("/commands/"+aID+"/interlock", map[string]any{
			"verdict":  "clear",
			"verifier": "safety-wu",
			"note":     "串行复核",
		})
		if err != nil {
			return err
		}
		if err := requireStatus(retry, http.StatusConflict, "serial review after race must be refused"); err != nil {
			return err
		}
	}

	// Request-time race: new applications are created concurrently while the
	// last budget is consumed by an execution. Requests may optimistically
	// survive as pending commands, but the review-time gate must ensure that
	// none of the survivors can ever execute past the limit.
	for round := 0; round < 10; round++ {
		gateID := fmt.Sprintf("gate-reqrace-%02d", round)
		if err := createWindowGate(client, gateID, 0, 10, 3.0, 3600); err != nil {
			return err
		}
		if _, err := completeAdjustment(client, gateID, 0.7, "safety-wu"); err != nil {
			return err
		}
		if _, err := completeAdjustment(client, gateID, 1.5, "safety-wu"); err != nil {
			return err
		}
		if _, err := completeAdjustment(client, gateID, 2.2, "safety-wu"); err != nil {
			return err
		}

		seed, err := requestAdjustmentExpectStatus(client, gateID, 3.0, http.StatusCreated, "seed racing request")
		if err != nil {
			return err
		}
		seedObj, err := objectField(seed.Body, "command")
		if err != nil {
			return err
		}
		seedID, err := stringField(seedObj, "id")
		if err != nil {
			return err
		}
		approved, err := fixtureClearReview(client, seedID, "safety-wu")
		if err != nil {
			return err
		}
		seedToken, err := stringField(approved, "execution_token")
		if err != nil {
			return err
		}

		pendingIDs := make([]string, 0, 5)
		var wg sync.WaitGroup
		start := make(chan struct{})
		var raceMu sync.Mutex
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				result, err := client.post("/commands", map[string]any{
					"gate_id":         gateID,
					"target_aperture": 2.9,
					"reason":          "并发申请窗口",
					"requested_by":    "dispatcher-lin",
				})
				if err == nil && result.Status == http.StatusCreated {
					if command, err := objectField(result.Body, "command"); err == nil {
						if id, err := stringField(command, "id"); err == nil {
							raceMu.Lock()
							pendingIDs = append(pendingIDs, id)
							raceMu.Unlock()
						}
					}
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = client.post("/commands/"+seedID+"/execute", map[string]any{
				"operator":       "operator-chen",
				"token":          seedToken,
				"simulate_fault": false,
			})
		}()
		close(start)
		wg.Wait()

		approvedCount := 0
		for _, id := range pendingIDs {
			review, err := client.post("/commands/"+id+"/interlock", map[string]any{
				"verdict":  "clear",
				"verifier": "safety-wu",
				"note":     "竞态后串行复核",
			})
			if err != nil {
				return err
			}
			if review.Status == http.StatusOK {
				approvedCount++
			} else if review.Status != http.StatusConflict {
				return fmt.Errorf("round %d: survivor review expected 200/409, got %d", round, review.Status)
			}
		}
		if approvedCount > 0 {
			return fmt.Errorf("round %d: no request surviving the race may clear after the window filled, %d did", round, approvedCount)
		}

		window, err := readWindow(client, gateID)
		if err != nil {
			return err
		}
		used, err := floatField(window, "used")
		if err != nil {
			return err
		}
		if used > 3.0+1e-9 {
			return fmt.Errorf("round %d: request race over-admitted the window, used=%v", round, used)
		}
	}
	return nil
}
