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
	Pages    *string `json:"pages,omitempty"`
	Angle    *int    `json:"angle,omitempty"`
	Password *string `json:"password,omitempty"`
	// Edit is the page list for pdf-edit. It is optional in the wire type
	// because an older client omits it entirely, and the operation reports
	// that the list is missing rather than the request being malformed.
	Edit             []PageItem     `json:"edit,omitempty"`
	Ranges           *string        `json:"ranges,omitempty"`
	Every            *int           `json:"every,omitempty"`
	FilePages        []string       `json:"file_pages,omitempty"`
	EncryptUser      *string        `json:"encrypt_user,omitempty"`
	EncryptOwner     *string        `json:"encrypt_owner,omitempty"`
	PermissionsOwner *string        `json:"permissions_owner,omitempty"`
	Permissions      []string       `json:"permissions,omitempty"`
	Stamp            StampParams    `json:"stamp,omitempty"`
	Crop             CropParams     `json:"crop,omitempty"`
	Resize           ResizeParams   `json:"resize,omitempty"`
	Layout           LayoutParams   `json:"layout,omitempty"`
	Metadata         MetadataParams `json:"metadata,omitempty"`
	PDFA             string         `json:"pdfa,omitempty"`
	Redact           RedactParams   `json:"redact,omitempty"`
}

type RedactParams struct {
	Areas []RedactArea `json:"areas,omitempty"`
	Terms []string     `json:"terms,omitempty"`
	DPI   int          `json:"dpi,omitempty"`
}

// FindMatch is a search result in visual page coordinates, expressed as page fractions.
type FindMatch struct {
	Page int     `json:"page"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
	Term string  `json:"term"`
}

type RedactArea struct {
	Page int     `json:"page"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

type MetadataParams struct {
	Title    string            `json:"title,omitempty"`
	Author   string            `json:"author,omitempty"`
	Subject  string            `json:"subject,omitempty"`
	Keywords string            `json:"keywords,omitempty"`
	Custom   map[string]string `json:"custom,omitempty"`
}

// StampParams is the typed stamp request shared by text and image operations.
type CropParams struct {
	Unit   string  `json:"unit,omitempty"`
	Top    float64 `json:"top,omitempty"`
	Right  float64 `json:"right,omitempty"`
	Bottom float64 `json:"bottom,omitempty"`
	Left   float64 `json:"left,omitempty"`
	Pages  string  `json:"pages,omitempty"`
}

type ResizeParams struct {
	To    string `json:"to,omitempty"`
	Fit   string `json:"fit,omitempty"`
	Pages string `json:"pages,omitempty"`
}

type LayoutParams struct {
	PerSheet    int    `json:"per_sheet,omitempty"`
	Orientation string `json:"orientation,omitempty"`
	Margin      int    `json:"margin,omitempty"`
	Border      bool   `json:"border,omitempty"`
}

type StampParams struct {
	Kind     string `json:"kind,omitempty"`
	Text     string `json:"text,omitempty"`
	Image    string `json:"image,omitempty"`
	Position string `json:"position,omitempty"`
	Opacity  int    `json:"opacity,omitempty"`
	Rotation int    `json:"rotation,omitempty"`
	Size     int    `json:"size,omitempty"`
	Color    string `json:"color,omitempty"`
	Pages    string `json:"pages,omitempty"`
	OnTop    bool   `json:"on_top,omitempty"`
	Start    int    `json:"start,omitempty"`
	Format   string `json:"format,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
	Left     string `json:"left,omitempty"`
	Centre   string `json:"centre,omitempty"`
	Right    string `json:"right,omitempty"`
	Edge     string `json:"edge,omitempty"`
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
