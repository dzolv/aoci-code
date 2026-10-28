// Package taskcontext provides optional, persistent, bounded task reading.
// Its evidence is task-local and never constitutes a formal Apply receipt.
package taskcontext

const (
	DefaultBudgetBytes = 96 * 1024
	ReserveBytes       = 16 * 1024
	DefaultPageBytes   = 8 * 1024
)

type Request struct {
	Action             string        `json:"action"`
	TaskID             string        `json:"task_id,omitempty"`
	Goal               string        `json:"goal,omitempty"`
	EventID            string        `json:"event_id,omitempty"`
	Kind               string        `json:"kind,omitempty"`
	Refs               []string      `json:"refs,omitempty"`
	Path               string        `json:"path,omitempty"`
	Query              string        `json:"query,omitempty"`
	Cursor             string        `json:"cursor,omitempty"`
	LimitBytes         int           `json:"limit_bytes,omitempty"`
	BudgetBytes        int           `json:"budget_bytes,omitempty"`
	ExternalBytes      int           `json:"external_bytes,omitempty"`
	Note               string        `json:"note,omitempty"`
	Pending            []string      `json:"pending,omitempty"`
	Resolved           []string      `json:"resolved,omitempty"`
	ResolutionEvidence []EvidenceRef `json:"resolution_evidence,omitempty"`
	MapText            string        `json:"map_text,omitempty"`
	MapEvidence        []EvidenceRef `json:"map_evidence,omitempty"`
}

type EvidenceRef struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	SHA256 string `json:"sha256"`
	Start  int    `json:"start,omitempty"`
	End    int    `json:"end,omitempty"`
}

type Item struct {
	Kind     string       `json:"kind"`
	Ref      string       `json:"ref"`
	Text     string       `json:"text,omitempty"`
	Evidence *EvidenceRef `json:"evidence,omitempty"`
	From     string       `json:"from,omitempty"`
	To       string       `json:"to,omitempty"`
	Token    string       `json:"token,omitempty"`
	Resolved bool         `json:"resolved,omitempty"`
	Decision *Resolution  `json:"decision,omitempty"`
}

// Resolution is a bounded Host judgment backed by evidence actually delivered
// in this task. It is independent of machine Apply and is reopened on drift.
type Resolution struct {
	Pending  []string      `json:"pending"`
	Note     string        `json:"note"`
	Evidence []EvidenceRef `json:"evidence"`
	Valid    bool          `json:"valid"`
}

type Summary struct {
	Goal                 string `json:"goal"`
	Phase                int    `json:"phase"`
	BudgetBytes          int    `json:"budget_bytes"`
	UsedBytes            int    `json:"used_bytes"`
	ReserveBytes         int    `json:"reserve_bytes"`
	PageBytes            int    `json:"page_bytes"`
	EvidenceCount        int    `json:"evidence_count"`
	InvalidEvidenceCount int    `json:"invalid_evidence_count"`
	PendingCount         int    `json:"pending_count"`
	Progress             int    `json:"progress"`
	CompletedBatchCount  int    `json:"completed_batch_count"`
	LastBatchID          string `json:"last_batch_id,omitempty"`
	DecisionCount        int    `json:"decision_count"`
	MapVersion           int    `json:"map_version"`
	MapValid             bool   `json:"map_valid"`
	ConfigSHA256         string `json:"config_sha256"`
	IndexIdentity        string `json:"index_identity,omitempty"`
}

type Response struct {
	Status       string  `json:"status"`
	TaskID       string  `json:"task_id"`
	State        Summary `json:"state"`
	Items        []Item  `json:"items,omitempty"`
	NextCursor   string  `json:"next_cursor,omitempty"`
	Remaining    int     `json:"remaining,omitempty"`
	Next         string  `json:"next,omitempty"`
	Verification string  `json:"verification,omitempty"`
	// Metering covers this API's serialized responses plus explicitly charged
	// external bytes; it cannot impose a hard cap on arbitrary Host tools.
	Metering string `json:"metering"`
}
