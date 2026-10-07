package domain

import (
	"fmt"
	"strings"
	"time"
)

// SupplementaryRequest records a reviewer's formal request for another
// measurement. It keeps the link to the accepted measurement that was under
// review when the request was made, and to the measurement that answered it.
type SupplementaryRequest struct {
	ID            string    `json:"id"`
	CaseID        string    `json:"case_id"`
	MeasurementID string    `json:"measurement_id"`
	Reviewer      string    `json:"reviewer"`
	Reason        string    `json:"reason"`
	FulfilledBy   string    `json:"fulfilled_by,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

func NewSupplementaryRequest(id, caseID, measurementID, reviewer, reason string, at time.Time) (SupplementaryRequest, error) {
	item := SupplementaryRequest{
		ID:            strings.TrimSpace(id),
		CaseID:        strings.TrimSpace(caseID),
		MeasurementID: strings.TrimSpace(measurementID),
		Reviewer:      strings.TrimSpace(reviewer),
		Reason:        strings.TrimSpace(reason),
		CreatedAt:     at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return SupplementaryRequest{}, err
	}
	return item, nil
}

func (r SupplementaryRequest) Validate() error {
	if r.ID == "" || r.CaseID == "" || r.MeasurementID == "" || r.Reviewer == "" || r.Reason == "" {
		return fmt.Errorf("%w: request id, case, measurement, reviewer, and reason are required", ErrInvalidInput)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: request time is required", ErrInvalidInput)
	}
	return nil
}

func (r SupplementaryRequest) IsOpen() bool {
	return r.FulfilledBy == ""
}
