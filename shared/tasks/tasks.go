package tasks

import (
	"encoding/json"

	"github.com/hibiken/asynq"
)

// ---------------------------------------------------------------------------
// Task Types — shared between mail-edge (producer) and worker (consumer)
// ---------------------------------------------------------------------------

const (
	// TypeMailIngest is the async task for processing inbound email.
	// Producer: mail-edge (after spam check)
	// Consumer: worker (parse MIME + DB insert + R2 upload)
	TypeMailIngest = "mail:ingest"
)

// ---------------------------------------------------------------------------
// Payloads
// ---------------------------------------------------------------------------

// MailIngestPayload carries the raw email and metadata from mail-edge → worker.
type MailIngestPayload struct {
	From             string  `json:"from"`
	To               string  `json:"to"`
	RawEmail         []byte  `json:"raw_email"`
	SpamScore        float64 `json:"spam_score"`
	QuarantineAction string  `json:"quarantine_action"`
}

// ---------------------------------------------------------------------------
// Task Constructors
// ---------------------------------------------------------------------------

// NewMailIngestTask creates a new Asynq task for mail ingestion.
// The raw email bytes are serialized directly into the task payload.
func NewMailIngestTask(from, to string, rawEmail []byte, spamScore float64, action string) (*asynq.Task, error) {
	payload, err := json.Marshal(MailIngestPayload{
		From:             from,
		To:               to,
		RawEmail:         rawEmail,
		SpamScore:        spamScore,
		QuarantineAction: action,
	})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(
		TypeMailIngest,
		payload,
		asynq.MaxRetry(5),
		asynq.Queue("ingest"),
		asynq.Timeout(120), // 2 minutes max per email
	), nil
}

// ParseMailIngestPayload deserializes the task payload.
func ParseMailIngestPayload(data []byte) (*MailIngestPayload, error) {
	var p MailIngestPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
