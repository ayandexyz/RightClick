package pdfops

import (
	"fmt"

	"file-converter/backend/internal/formats"
)

// Toolbox holds one object per external PDF engine. A nil engine means the
// binary is not installed; Availability of an operation is decided per engine so
// a missing tool disables only its own operations.
type Toolbox struct {
	PDFCPU      Backend
	QPDF        *QPDF
	GS          *Ghostscript
	Poppler     *Poppler
	OCR         *OCR
	LibreOffice *LibreOffice
}

// Available reports whether the named engine can run, or why it cannot. An
// engine that is not installed, and one that is not part of the toolbox at all,
// are both reported as unavailable.
func (t Toolbox) Available(id string) error {
	switch id {
	case EnginePDFCPU:
		if t.PDFCPU == nil {
			return errEngineMissing(id)
		}
		return t.PDFCPU.Available()
	case EngineQPDF:
		if t.QPDF == nil {
			return errEngineMissing(id)
		}
		return t.QPDF.Available()
	case EngineGhostscript:
		if t.GS == nil {
			return errEngineMissing(id)
		}
		return t.GS.Available()
	case EnginePoppler:
		if t.Poppler == nil {
			return errEngineMissing(id)
		}
		return t.Poppler.Available()
	case EngineTesseract:
		if t.OCR == nil {
			return errEngineMissing(id)
		}
		return t.OCR.Available()
	case EngineLibreOffice:
		if t.LibreOffice == nil {
			return errEngineMissing(id)
		}
		return t.LibreOffice.Available()
	default:
		return errEngineMissing(id)
	}
}

// engineFor returns the operation's engine, so Run can reject an operation whose
// engine is not installed before touching any file.
func (o Operation) engineFor(tb Toolbox) error { return tb.Available(o.Engine) }

// EngineIDs lists every engine the toolbox may hold, in reporting order. All of
// them are always reported so a client can tell "not installed" apart from
// "this daemon is older and has never heard of it".
func EngineIDs() []string {
	return []string{EnginePDFCPU, EngineQPDF, EngineGhostscript, EnginePoppler, EngineTesseract, EngineLibreOffice}
}

// unavailableReason explains why an operation cannot run against this toolbox
// and selection, or returns "" when it can. An operation whose engine is missing
// is disabled by that alone, so a missing tool never takes other tools with it.
func (o Operation) UnavailableReason(tb Toolbox, files []string) string {
	if o.Unimplemented != "" {
		return o.Unimplemented
	}
	if err := tb.Available(o.EngineID()); err != nil {
		return err.Error()
	}
	if o.Merges {
		if len(files) < 2 {
			return fmt.Sprintf("needs at least %d files", 2)
		}
	} else if len(files) < o.MinInputs {
		if o.MinInputs == 1 {
			return "needs at least 1 file"
		}
		return fmt.Sprintf("needs at least %d files", o.MinInputs)
	}
	if o.MaxInputs > 0 && len(files) > o.MaxInputs {
		return fmt.Sprintf("accepts at most %d files", o.MaxInputs)
	}
	if o.InputKind() == InputImage {
		if reason := o.imageSupportReason(files); reason != "" {
			return reason
		}
	}
	return ""
}

// imageSupportReason reports the first image whose format this operation cannot
// import, so the menu can say why instead of offering a choice that will fail.
func (o Operation) imageSupportReason(files []string) string {
	for _, file := range files {
		format, err := formats.Detect(file)
		if err != nil {
			continue
		}
		if !SupportsImageImport(format) {
			return fmt.Sprintf("%s is not supported for PDF import", format)
		}
	}
	return ""
}
