package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/bootstrap"
)

type apiResponse struct {
	Status int
	Body   map[string]any
	Raw    string
}

type harness struct {
	baseURL string
	client  *http.Client
	server  *httptest.Server
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to verify")
	flag.Parse()

	checks := map[string]func() error{
		"register-device":                   checkRegisterDevice,
		"open-verification-case":            checkOpenVerificationCase,
		"record-measurement":                checkRecordMeasurement,
		"reopen-verification-case":          checkReopenVerificationCase,
		"seal-verification":                 checkSealVerification,
		"supplementary-measurement-request": checkSupplementaryMeasurementRequest,
	}
	run, ok := checks[*workflow]
	if !ok {
		fmt.Fprintf(flag.CommandLine.Output(), "unknown workflow %q\n", *workflow)
		flag.Usage()
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Printf("workflow %s: failed: %v\n", *workflow, err)
		os.Exit(1)
	}
	fmt.Printf("workflow %s: ok\n", *workflow)
}

func checkRegisterDevice() error {
	h := newHarness()
	defer h.close()

	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": " met-1001 ",
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return err
	}
	id, err := nestedString(response.Body, "device", "id")
	if err != nil {
		return err
	}
	tag, err := nestedString(response.Body, "device", "asset_tag")
	if err != nil {
		return err
	}
	if tag != "MET-1001" {
		return fmt.Errorf("asset tag was not normalized: %q", tag)
	}

	read, err := h.request(http.MethodGet, "/devices/"+id, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	readID, err := nestedString(read.Body, "device", "id")
	if err != nil {
		return err
	}
	if readID != id {
		return fmt.Errorf("read returned device %q, expected %q", readID, id)
	}

	duplicate, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": "MET-1001",
		"model":     "Other Meter",
		"room":      "Room B",
		"steward":   "Mira",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(duplicate, "duplicate_asset_tag")
}

func checkOpenVerificationCase() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1101")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	state, err := nestedString(opened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("opened case state = %q", state)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}

	second, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(second, "active_case_exists")
}

func checkRecordMeasurement() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1201")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	accepted, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(accepted, http.StatusCreated); err != nil {
		return err
	}
	state, err := nestedString(accepted.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "review_ready" {
		return fmt.Errorf("accepted measurement state = %q", state)
	}
	acceptedValue, err := nestedBool(accepted.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if !acceptedValue {
		return errors.New("in-tolerance measurement was not accepted")
	}

	second, err := h.recordMeasurement(caseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	rejectDeviceID, err := h.createDevice("MET-1202")
	if err != nil {
		return err
	}
	rejectCase, err := h.openCase(rejectDeviceID)
	if err != nil {
		return err
	}
	rejectCaseID, err := nestedString(rejectCase.Body, "case", "id")
	if err != nil {
		return err
	}
	rejected, err := h.recordMeasurement(rejectCaseID, 26.5, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(rejected, http.StatusCreated); err != nil {
		return err
	}
	rejectedState, err := nestedString(rejected.Body, "case", "state")
	if err != nil {
		return err
	}
	if rejectedState != "recheck_required" {
		return fmt.Errorf("out-of-tolerance state = %q", rejectedState)
	}
	rejectedValue, err := nestedBool(rejected.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if rejectedValue {
		return errors.New("out-of-tolerance measurement was accepted")
	}
	return nil
}

func checkReopenVerificationCase() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.rejectedCase("MET-1301")
	if err != nil {
		return err
	}
	reopened, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "measurement setup was replaced",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reopened, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(reopened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("reopened case state = %q", state)
	}
	measurements, err := nestedSlice(reopened.Body, "measurements")
	if err != nil {
		return err
	}
	if len(measurements) != 1 {
		return fmt.Errorf("reopen changed measurement history: %d entries", len(measurements))
	}

	second, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "duplicate request",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_reopened") {
		return errors.New("reopen audit event is missing")
	}
	return nil
}

func checkSealVerification() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.readyCase("MET-1401")
	if err != nil {
		return err
	}
	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
				"reviewer": fmt.Sprintf("Reviewer-%d", index+1),
				"summary":  "accepted measurement sealed for review",
			})
			results <- outcome{response: response, err: err}
		}(index)
	}
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			return fmt.Errorf("unexpected concurrent seal status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if successes != 1 || conflicts != 1 {
		return fmt.Errorf("concurrent seal outcomes: %d success, %d conflict", successes, conflicts)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(read.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "sealed" {
		return fmt.Errorf("sealed case state = %q", state)
	}
	sealID, err := nestedString(read.Body, "seal", "id")
	if err != nil {
		return err
	}
	if sealID == "" {
		return errors.New("evidence seal is missing")
	}
	return nil
}

func checkSupplementaryMeasurementRequest() error {
	h := newHarness()
	defer h.close()

	// Happy path: reviewer requests another measurement, technician answers
	// with an in-tolerance value, request resolves accepted and the case
	// becomes review_ready again with every measurement preserved.
	caseID, err := h.readyCase("MET-1501")
	if err != nil {
		return err
	}
	before, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	basisID, err := nestedStringAt(before.Body, "measurements", 0, "id")
	if err != nil {
		return err
	}
	requested, err := h.requestSupplementary(caseID, "Reviewer-A", "reading looks unstable; repeat at midpoint")
	if err != nil {
		return err
	}
	if err := expectStatus(requested, http.StatusCreated); err != nil {
		return err
	}
	state, err := nestedString(requested.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("case state after supplementary request = %q", state)
	}
	requestsView, err := nestedSlice(requested.Body, "supplementary_measurement_requests")
	if err != nil {
		return err
	}
	if len(requestsView) != 1 {
		return fmt.Errorf("supplementary request count = %d, expected 1", len(requestsView))
	}
	requestID, err := nestedStringAt(requested.Body, "supplementary_measurement_requests", 0, "id")
	if err != nil {
		return err
	}
	if requestID == "" {
		return errors.New("supplementary request id is missing")
	}
	requestStatus, err := nestedStringAt(requested.Body, "supplementary_measurement_requests", 0, "status")
	if err != nil {
		return err
	}
	if requestStatus != "requested" {
		return fmt.Errorf("new request status = %q", requestStatus)
	}
	basedOn, err := nestedStringAt(requested.Body, "supplementary_measurement_requests", 0, "based_on_measurement_id")
	if err != nil {
		return err
	}
	if basedOn != basisID {
		return fmt.Errorf("request based on %q, expected %q", basedOn, basisID)
	}
	measurementsView, err := nestedSlice(requested.Body, "measurements")
	if err != nil {
		return err
	}
	if len(measurementsView) != 1 {
		return fmt.Errorf("requesting supplementary measurement changed measurement history: %d entries", len(measurementsView))
	}

	// A duplicate request while the case is back in measurement is rejected.
	duplicate, err := h.requestSupplementary(caseID, "Reviewer-A", "duplicate request")
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(duplicate, "state_conflict"); err != nil {
		return err
	}

	// Technician answers; in-tolerance result closes the request accepted and
	// returns the case to review_ready.
	answered, err := h.recordMeasurement(caseID, 25.0, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(answered, http.StatusCreated); err != nil {
		return err
	}
	answeredState, err := nestedString(answered.Body, "case", "state")
	if err != nil {
		return err
	}
	if answeredState != "review_ready" {
		return fmt.Errorf("state after accepted supplementary measurement = %q", answeredState)
	}
	resolvedStatus, err := nestedStringAt(answered.Body, "supplementary_measurement_requests", 0, "status")
	if err != nil {
		return err
	}
	if resolvedStatus != "accepted" {
		return fmt.Errorf("request status after accepted measurement = %q", resolvedStatus)
	}
	resultID, err := nestedStringAt(answered.Body, "measurements", 1, "id")
	if err != nil {
		return err
	}
	linkedResult, err := nestedStringAt(answered.Body, "supplementary_measurement_requests", 0, "result_measurement_id")
	if err != nil {
		return err
	}
	if linkedResult != resultID {
		return fmt.Errorf("request linked to result %q, expected %q", linkedResult, resultID)
	}
	history, err := nestedSlice(answered.Body, "measurements")
	if err != nil {
		return err
	}
	if len(history) != 2 {
		return fmt.Errorf("measurement history after supplementary measurement = %d entries", len(history))
	}

	// After acceptance a new request is allowed (request already resolved);
	// racing that request against a seal yields exactly one trajectory.
	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		response, err := h.requestSupplementary(caseID, "Reviewer-B", "request racing the seal")
		results <- outcome{response: response, err: err}
	}()
	go func() {
		defer wait.Done()
		response, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
			"reviewer": "Reviewer-C",
			"summary":  "sealed after accepted supplementary measurement",
		})
		results <- outcome{response: response, err: err}
	}()
	wait.Wait()
	close(results)
	successes := 0
	conflicts := 0
	var finalState string
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusCreated:
			successes++
			finalState, _ = nestedString(item.response.Body, "case", "state")
		case http.StatusConflict:
			conflicts++
		default:
			return fmt.Errorf("unexpected concurrent status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if successes != 1 || conflicts != 1 {
		return fmt.Errorf("request/seal race outcomes: %d success, %d conflict", successes, conflicts)
	}
	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	finalReadState, err := nestedString(read.Body, "case", "state")
	if err != nil {
		return err
	}
	if finalReadState != finalState {
		return fmt.Errorf("final state inconsistent: response %q, stored %q", finalState, finalReadState)
	}
	if finalReadState != "sealed" && finalReadState != "awaiting_measurement" {
		return fmt.Errorf("unexpected final state after race: %q", finalReadState)
	}
	openRequests := 0
	allRequests, err := nestedSlice(read.Body, "supplementary_measurement_requests")
	if err != nil {
		return err
	}
	for _, item := range allRequests {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if entry["status"] == "requested" {
			openRequests++
		}
	}
	_, hasSeal := read.Body["seal"]
	switch finalReadState {
	case "sealed":
		if openRequests != 0 {
			return fmt.Errorf("sealed case carries %d open supplementary requests", openRequests)
		}
		if !hasSeal {
			return errors.New("sealed case has no evidence seal")
		}
	case "awaiting_measurement":
		// The request beat the seal: one open request must exist and no seal.
		if openRequests != 1 {
			return fmt.Errorf("request-won race leaves %d open requests, expected 1", openRequests)
		}
		if hasSeal {
			return errors.New("awaiting case unexpectedly carries an evidence seal")
		}
	default:
		return fmt.Errorf("unexpected final state after race: %q", finalReadState)
	}
	// Whatever happened, a second request is refused: sealed cases are
	// terminal, and an open request already exists otherwise.
	afterRace, err := h.requestSupplementary(caseID, "Reviewer-B", "second request after race")
	if err != nil {
		return err
	}
	if err := expectStatus(afterRace, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(afterRace, "state_conflict"); err != nil {
		return err
	}
	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "supplementary_measurement_requested") || !hasEvent(events.Body, "supplementary_measurement_accepted") {
		return errors.New("supplementary measurement audit events are missing")
	}

	// Failed supplementary measurement: request is rejected and the case
	// follows the unchanged recheck -> reopen trajectory with a single
	// consistent chain, and a second request stays impossible until resolved.
	rejectCase, err := h.readyCase("MET-1502")
	if err != nil {
		return err
	}
	firstRequest, err := h.requestSupplementary(rejectCase, "Reviewer-A", "repeat with new setup")
	if err != nil {
		return err
	}
	if err := expectStatus(firstRequest, http.StatusCreated); err != nil {
		return err
	}
	failed, err := h.recordMeasurement(rejectCase, 26.5, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(failed, http.StatusCreated); err != nil {
		return err
	}
	failedState, err := nestedString(failed.Body, "case", "state")
	if err != nil {
		return err
	}
	if failedState != "recheck_required" {
		return fmt.Errorf("state after failed supplementary measurement = %q", failedState)
	}
	failedStatus, err := nestedStringAt(failed.Body, "supplementary_measurement_requests", 0, "status")
	if err != nil {
		return err
	}
	if failedStatus != "rejected" {
		return fmt.Errorf("request status after failed measurement = %q", failedStatus)
	}
	failedResult, err := nestedStringAt(failed.Body, "supplementary_measurement_requests", 0, "result_measurement_id")
	if err != nil {
		return err
	}
	if failedResult == "" {
		return errors.New("rejected request has no result measurement link")
	}
	// Case is in recheck, not review_ready: requesting again must conflict
	// rather than fork the trajectory.
	reRequest, err := h.requestSupplementary(rejectCase, "Reviewer-A", "another request during recheck")
	if err != nil {
		return err
	}
	if err := expectStatus(reRequest, http.StatusConflict); err != nil {
		return err
	}
	// The original steward reopen path is unchanged and still works.
	reopened, err := h.request(http.MethodPost, "/cases/"+rejectCase+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "replace fixture before next measurement",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reopened, http.StatusOK); err != nil {
		return err
	}
	reopenedState, err := nestedString(reopened.Body, "case", "state")
	if err != nil {
		return err
	}
	if reopenedState != "awaiting_measurement" {
		return fmt.Errorf("state after reopen = %q", reopenedState)
	}

	// Requests are invalid outside review_ready (here: awaiting_measurement).
	awaitingCaseID, err := func() (string, error) {
		deviceID, err := h.createDevice("MET-1503")
		if err != nil {
			return "", err
		}
		opened, err := h.openCase(deviceID)
		if err != nil {
			return "", err
		}
		return nestedString(opened.Body, "case", "id")
	}()
	if err != nil {
		return err
	}
	early, err := h.requestSupplementary(awaitingCaseID, "Reviewer-A", "no accepted measurement yet")
	if err != nil {
		return err
	}
	if err := expectStatus(early, http.StatusConflict); err != nil {
		return err
	}

	// Missing reviewer/reason is a validation error.
	missingInput, err := h.requestSupplementary(caseID, "", "missing reviewer")
	if err != nil {
		return err
	}
	if err := expectStatus(missingInput, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(missingInput, "invalid_input"); err != nil {
		return err
	}
	return nil
}

func newHarness() *harness {
	server := httptest.NewServer(bootstrap.NewHandler())
	return &harness{
		baseURL: server.URL,
		client:  server.Client(),
		server:  server,
	}
}

func (h *harness) close() {
	h.server.Close()
}

func (h *harness) request(method, path string, body any) (apiResponse, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return apiResponse{}, err
		}
		reader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, reader)
	if err != nil {
		return apiResponse{}, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		return apiResponse{}, err
	}
	defer response.Body.Close()
	rawBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return apiResponse{}, err
	}
	result := apiResponse{Status: response.StatusCode, Raw: string(rawBytes)}
	if len(bytes.TrimSpace(rawBytes)) > 0 {
		if err := json.Unmarshal(rawBytes, &result.Body); err != nil {
			return apiResponse{}, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return result, nil
}

func (h *harness) createDevice(tag string) (string, error) {
	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": tag,
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return nestedString(response.Body, "device", "id")
}

func (h *harness) openCase(deviceID string) (apiResponse, error) {
	response, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return apiResponse{}, err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return apiResponse{}, err
	}
	return response, nil
}

func (h *harness) recordMeasurement(caseID string, observed float64, technician string) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/measurements", map[string]any{
		"observed":   observed,
		"technician": technician,
	})
}

func (h *harness) requestSupplementary(caseID, reviewer, reason string) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/supplementary-measurement-requests", map[string]any{
		"reviewer": reviewer,
		"reason":   reason,
	})
}

func (h *harness) rejectedCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 26.5, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) readyCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func expectStatus(response apiResponse, expected int) error {
	if response.Status != expected {
		return fmt.Errorf("status = %d, expected %d: %s", response.Status, expected, response.Raw)
	}
	return nil
}

func expectErrorCode(response apiResponse, expected string) error {
	code, err := nestedString(response.Body, "error", "code")
	if err != nil {
		return err
	}
	if code != expected {
		return fmt.Errorf("error code = %q, expected %q", code, expected)
	}
	return nil
}

func nestedString(root map[string]any, path ...string) (string, error) {
	value, err := nestedValue(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s is not a string", strings.Join(path, "."))
	}
	return text, nil
}

func nestedStringAt(root map[string]any, path ...any) (string, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%v is not a string", path)
	}
	return text, nil
}

func nestedBool(root map[string]any, path ...any) (bool, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return false, err
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%v is not a boolean", path)
	}
	return flag, nil
}

func nestedSlice(root map[string]any, key string) ([]any, error) {
	value, ok := root[key]
	if !ok {
		return nil, fmt.Errorf("missing %s", key)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", key)
	}
	return items, nil
}

func hasEvent(root map[string]any, kind string) bool {
	items, err := nestedSlice(root, "events")
	if err != nil {
		return false
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if event["kind"] == kind {
			return true
		}
	}
	return false
}

func nestedValue(root map[string]any, path ...string) (any, error) {
	parts := make([]any, 0, len(path))
	for _, part := range path {
		parts = append(parts, part)
	}
	return nestedAny(root, parts...)
}

func nestedAny(root map[string]any, path ...any) (any, error) {
	var current any = root
	for _, part := range path {
		switch typed := part.(type) {
		case string:
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not an object", typed)
			}
			value, ok := object[typed]
			if !ok {
				return nil, fmt.Errorf("missing %s", typed)
			}
			current = value
		case int:
			items, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("index %d is not inside an array", typed)
			}
			if typed < 0 || typed >= len(items) {
				return nil, fmt.Errorf("index %d is out of range", typed)
			}
			current = items[typed]
		default:
			return nil, fmt.Errorf("unsupported path element %T", part)
		}
	}
	return current, nil
}
