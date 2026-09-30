package main

import (
	"fmt"
	"net/http"
)

func fixtureGate(
	client *apiClient,
	id string,
	name string,
	maxAperture float64,
	initialAperture float64,
) (map[string]any, error) {
	result, err := client.post("/gates", map[string]any{
		"id":               id,
		"name":             name,
		"max_aperture":     maxAperture,
		"initial_aperture": initialAperture,
	})
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusCreated, "create fixture gate"); err != nil {
		return nil, err
	}
	gate, err := objectField(result.Body, "gate")
	if err != nil {
		return nil, fmt.Errorf("create fixture gate response: %w", err)
	}
	return gate, nil
}

func fixtureWindowGate(
	client *apiClient,
	id string,
	name string,
	maxAperture float64,
	initialAperture float64,
	windowSeconds int64,
	maxTravel float64,
) (map[string]any, error) {
	result, err := client.post("/gates", map[string]any{
		"id":                           id,
		"name":                         name,
		"max_aperture":                 maxAperture,
		"initial_aperture":             initialAperture,
		"adjustment_window_seconds":    windowSeconds,
		"adjustment_window_max_travel": maxTravel,
	})
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusCreated, "create fixture window gate"); err != nil {
		return nil, err
	}
	return objectField(result.Body, "gate")
}

func fixtureCommand(
	client *apiClient,
	gateID string,
	targetAperture float64,
	reason string,
	requestedBy string,
) (map[string]any, error) {
	result, err := client.post("/commands", map[string]any{
		"gate_id":         gateID,
		"target_aperture": targetAperture,
		"reason":          reason,
		"requested_by":    requestedBy,
	})
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusCreated, "create fixture command"); err != nil {
		return nil, err
	}
	command, err := objectField(result.Body, "command")
	if err != nil {
		return nil, fmt.Errorf("create fixture command response: %w", err)
	}
	return command, nil
}

func fixtureClearReview(
	client *apiClient,
	commandID string,
	verifier string,
) (map[string]any, error) {
	result, err := client.post("/commands/"+commandID+"/interlock", map[string]any{
		"verdict":  "clear",
		"verifier": verifier,
		"note":     "联锁条件满足",
	})
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusOK, "clear fixture interlock"); err != nil {
		return nil, err
	}
	command, err := objectField(result.Body, "command")
	if err != nil {
		return nil, fmt.Errorf("clear fixture interlock response: %w", err)
	}
	return command, nil
}

// approveAndExecute clears, executes and returns the executed response body.
func approveAndExecute(
	client *apiClient,
	commandID string,
	verifier string,
	simulateFault bool,
) (apiResult, error) {
	approved, err := fixtureClearReview(client, commandID, verifier)
	if err != nil {
		return apiResult{}, err
	}
	token, err := stringField(approved, "execution_token")
	if err != nil {
		return apiResult{}, err
	}
	return client.post("/commands/"+commandID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          token,
		"simulate_fault": simulateFault,
	})
}

// createCommandRaw submits a command without requiring 201, so rejection
// branches can assert the exact status and body.
func createCommandRaw(
	client *apiClient,
	gateID string,
	targetAperture float64,
) (apiResult, error) {
	return client.post("/commands", map[string]any{
		"gate_id":         gateID,
		"target_aperture": targetAperture,
		"reason":          "调整窗口验证",
		"requested_by":    "dispatcher-lin",
	})
}

func getWindow(client *apiClient, gateID string) (map[string]any, error) {
	result, err := client.get("/gates/" + gateID + "/adjustment-window")
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusOK, "read adjustment window"); err != nil {
		return nil, err
	}
	return objectField(result.Body, "window")
}
