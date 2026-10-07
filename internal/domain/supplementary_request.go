package domain

import (
	"fmt"
	"strings"
	"time"
)

type SupplementaryRequestStatus string

const (
	SupplementaryRequested SupplementaryRequestStatus = "requested"
	SupplementaryAccepted  SupplementaryRequestStatus = "accepted"
	SupplementaryRejected  SupplementaryRequestStatus = "rejected"
)

// SupplementaryMeasurementRequest is a verifier's formal request, issued while a
// case is review_ready, for the technician to perform another measurement. It
// ties the measurement under review (BasedOnMeasurementID) to the measurement
// that eventually resolves the request (ResultMeasurementID).
type SupplementaryMeasurementRequest struct {
	ID                   string                     `json:"id"`
	CaseID               string                     `json:"case_id"`
	Reviewer             string                     `json:"reviewer"`
	Reason               string                     `json:"reason"`
	BasedOnMeasurementID string                     `json:"based_on_measurement_id"`
	Status               SupplementaryRequestStatus `json:"status"`
	ResultMeasurementID  string                     `json:"result_measurement_id,omitempty"`
	CreatedAt            time.Time                  `json:"created_at"`
	ResolvedAt           *time.Time                 `json:"resolved_at,omitempty"`
}

func NewSupplementaryMeasurementRequest(id, caseID, reviewer, reason, basedOnMeasurementID string, at time.Time) (SupplementaryMeasurementRequest, error) {
	item := SupplementaryMeasurementRequest{
		ID:                   strings.TrimSpace(id),
		CaseID:               strings.TrimSpace(caseID),
		Reviewer:             strings.TrimSpace(reviewer),
		Reason:               strings.TrimSpace(reason),
		BasedOnMeasurementID: strings.TrimSpace(basedOnMeasurementID),
		Status:               SupplementaryRequested,
		CreatedAt:            at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return SupplementaryMeasurementRequest{}, err
	}
	return item, nil
}

func (r SupplementaryMeasurementRequest) Validate() error {
	if r.ID == "" || r.CaseID == "" || r.Reviewer == "" || r.Reason == "" || r.BasedOnMeasurementID == "" {
		return fmt.Errorf("%w: request id, case, reviewer, reason, and source measurement are required", ErrInvalidInput)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: request time is required", ErrInvalidInput)
	}
	switch r.Status {
	case SupplementaryRequested, SupplementaryAccepted, SupplementaryRejected:
	default:
		return fmt.Errorf("%w: unsupported supplementary request status %q", ErrInvalidInput, r.Status)
	}
	if r.Status == SupplementaryRequested {
		if r.ResultMeasurementID != "" || r.ResolvedAt != nil {
			return fmt.Errorf("%w: an open supplementary request cannot carry a result", ErrInvalidInput)
		}
		return nil
	}
	if r.ResultMeasurementID == "" || r.ResolvedAt == nil {
		return fmt.Errorf("%w: a resolved supplementary request requires a result measurement and time", ErrInvalidInput)
	}
	return nil
}

func (r SupplementaryMeasurementRequest) IsOpen() bool {
	return r.Status == SupplementaryRequested
}

// Resolve closes the request with the measurement that answered it. The
// accepted flag follows the tolerance decision for that measurement.
func (r *SupplementaryMeasurementRequest) Resolve(resultMeasurementID string, accepted bool, at time.Time) error {
	if r.Status != SupplementaryRequested {
		return fmt.Errorf("%w: supplementary measurement request is already %s", ErrStateConflict, r.Status)
	}
	id := strings.TrimSpace(resultMeasurementID)
	if id == "" {
		return fmt.Errorf("%w: result measurement is required", ErrInvalidInput)
	}
	stamp := at.UTC()
	if accepted {
		r.Status = SupplementaryAccepted
	} else {
		r.Status = SupplementaryRejected
	}
	r.ResultMeasurementID = id
	r.ResolvedAt = &stamp
	return nil
}
