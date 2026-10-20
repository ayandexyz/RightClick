package pdfops

import "file-converter/backend/internal/formats"

// SupportsImageImport reports whether pdfcpu can import an image of this format
// into a PDF page. pdfcpu decodes JPEG, PNG and TIFF (see the image decoders it
// registers in pkg/pdfcpu/model) and nothing else, so every other format is
// rejected up front rather than failing part way through an import.
func SupportsImageImport(format formats.Format) bool {
	switch format {
	case formats.JPEG, formats.PNG, formats.TIFF:
		return true
	default:
		return false
	}
}
