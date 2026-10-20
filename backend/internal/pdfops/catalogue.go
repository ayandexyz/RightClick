package pdfops

// catalogue is the ordered list of operations the daemon offers. Adding one is
// a single entry here plus, if it needs new behaviour, a case in Run.
var catalogue = []Operation{
	{ID: "pdf-merge", Label: "Merge", Group: "organise", Mode: "instant", MinInputs: 2, Merges: true},
	{ID: "pdf-extract", Label: "Extract pages", Group: "organise", Mode: "studio", MinInputs: 1, NeedsPages: true, Suffix: "-extracted"},
	{ID: "pdf-delete", Label: "Delete pages", Group: "organise", Mode: "studio", MinInputs: 1, NeedsPages: true, Suffix: "-edited"},
	{ID: "pdf-rotate", Label: "Rotate", Group: "organise", Mode: "studio", MinInputs: 1, NeedsAngle: true, OptionalPages: true, Suffix: "-rotated"},
	{ID: "pdf-repair", Label: "Repair", Group: "fix", Mode: "instant", Engine: EngineQPDF, MinInputs: 1, Suffix: "-repaired"},
	{ID: "pdf-linearize", Label: "Linearize", Group: "fix", Mode: "instant", Engine: EngineQPDF, MinInputs: 1, Suffix: "-linearized"},
	{ID: "pdf-flatten", Label: "Flatten form fields", Group: "fix", Mode: "instant", Engine: EngineQPDF, MinInputs: 1, Suffix: "-flattened"},
	{ID: "pdf-compress", Label: "Compress", Group: "optimise", Mode: "studio", Engine: EngineGhostscript, MinInputs: 1, OptionalPages: false, Suffix: "-compressed"},
	{ID: "pdf-grayscale", Label: "Convert to grayscale", Group: "optimise", Mode: "instant", Engine: EngineGhostscript, MinInputs: 1, Suffix: "-gray"},
	{ID: "pdf-rotate-cw", Label: "Rotate 90° clockwise", Group: "organise", Mode: "instant", MinInputs: 1, FixedAngle: 90, Suffix: "-rotated"},
	{ID: "pdf-rotate-ccw", Label: "Rotate 90° anticlockwise", Group: "organise", Mode: "instant", MinInputs: 1, FixedAngle: 270, Suffix: "-rotated"},
	{ID: "pdf-rotate-180", Label: "Rotate 180°", Group: "organise", Mode: "instant", MinInputs: 1, FixedAngle: 180, Suffix: "-rotated"},
	{ID: "pdf-reverse", Label: "Reverse pages", Group: "organise", Mode: "instant", MinInputs: 1, Suffix: "-reversed"},
}
