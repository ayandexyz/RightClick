package protocol

import "encoding/json"

type ImageOptions struct {
	Quality    *int
	MaxSize    *int
	Background string
}

// EngineStatus reports whether a registered engine can run and, if not, why.
type EngineStatus struct {
	ID        string `json:"id"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

type ConflictPolicy string

const (
	ConflictRename    ConflictPolicy = "rename"
	ConflictSkip      ConflictPolicy = "skip"
	ConflictOverwrite ConflictPolicy = "overwrite"
)

// Request is a JSON-RPC style request sent by clients.
type Request struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Response is a successful JSON-RPC response.
type Response struct {
	ID     int         `json:"id"`
	Result interface{} `json:"result"`
}

// Error is an error response carrying a code and message.
type Error struct {
	ID    int        `json:"id"`
	Error ErrorValue `json:"error"`
}

type ErrorValue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Event is a server-to-client event with no id.
type Event struct {
	Name string      `json:"event"`
	Data interface{} `json:"data"`
}

// Target represents a conversion target returned by the targets method.
type Target struct {
	Preset   string `json:"preset"`
	Label    string `json:"label"`
	Format   string `json:"format"`
	Category string `json:"category"`
	Enabled  bool   `json:"enabled"`
	Reason   string `json:"reason,omitempty"`
}

// Operation describes a PDF tool applicable to the current selection, returned
// by the operations method.
type Operation struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Group   string `json:"group"`
	Mode    string `json:"mode"`
	Engine  string `json:"engine,omitempty"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

// Params carries an operation's parameters. A nil field means "not provided",
// so an operation can tell a missing parameter from a zero value.
type Params struct {
	Pages *string `json:"pages,omitempty"`
	Angle *int    `json:"angle,omitempty"`
	// Edit is the page list for pdf-edit. It is optional in the wire type
	// because an older client omits it entirely, and the operation reports
	// that the list is missing rather than the request being malformed.
	Edit      []PageItem `json:"edit,omitempty"`
	Ranges    *string    `json:"ranges,omitempty"`
	Every     *int       `json:"every,omitempty"`
	FilePages []string   `json:"file_pages,omitempty"`
}

// PageItem is one page of an edit: a source page, and an optional clockwise
// rotation delta. Src 0 means a blank page.
type PageItem struct {
	Src    int `json:"src"`
	Rotate int `json:"rotate,omitempty"`
}

// Job represents the state of a conversion job.
type Job struct {
	ID       string    `json:"id"`
	Preset   string    `json:"preset"`
	Label    string    `json:"label"`
	State    string    `json:"state"`
	Progress float64   `json:"progress"`
	Files    []FileJob `json:"files"`
}

// FileJob represents the state of a single file in a job.
type FileJob struct {
	Source   string     `json:"source"`
	State    string     `json:"state"`
	Progress float64    `json:"progress"`
	Outputs  []string   `json:"outputs"`
	Error    *FileError `json:"error,omitempty"`
	Notice   string     `json:"notice,omitempty"`
}

// FileError represents an error on a single file.
type FileError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
