package pdfops

// OCR shells out to pdftocairo and tesseract to build a searchable PDF.
type OCR struct {
	runner Runner
}

func (o *OCR) ID() string { return EngineTesseract }

func (o *OCR) Available() error { return nil }
