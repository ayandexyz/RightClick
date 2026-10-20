package pdfops

// catalogue is the ordered list of operations the daemon offers. Adding one is
// a single entry here plus, if it needs new behaviour, a case in Operation.Run.
var catalogue = []Operation{
	{ID: "pdf-merge", Label: "Merge", Group: "organise", Mode: "instant", MinInputs: 2, Merges: true},
	{ID: "pdf-extract", Label: "Extract pages", Group: "organise", Mode: "studio", MinInputs: 1, NeedsPages: true, Suffix: "-extracted"},
	{ID: "pdf-delete", Label: "Delete pages", Group: "organise", Mode: "studio", MinInputs: 1, NeedsPages: true, Suffix: "-edited"},
	{ID: "pdf-rotate", Label: "Rotate", Group: "organise", Mode: "instant", MinInputs: 1, NeedsAngle: true, OptionalPages: true, Suffix: "-rotated"},
	{ID: "pdf-reverse", Label: "Reverse pages", Group: "organise", Mode: "instant", MinInputs: 1, Suffix: "-reversed"},
}
