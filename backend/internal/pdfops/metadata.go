package pdfops

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// MetadataParams contains standard Info dictionary fields and arbitrary custom
// Info properties. Empty standard values are omitted from writes because the
// pdfcpu API only adds/updates properties; callers cannot clear an existing
// field through this API.
type MetadataParams struct {
	Title    string
	Author   string
	Subject  string
	Keywords string
	Custom   map[string]string
}

type MetadataResult struct {
	Title    string            `json:"title"`
	Author   string            `json:"author"`
	Subject  string            `json:"subject"`
	Keywords string            `json:"keywords"`
	Producer string            `json:"producer"`
	Creator  string            `json:"creator"`
	Created  string            `json:"created"`
	Modified string            `json:"modified"`
	Custom   map[string]string `json:"custom"`
}

func (b *PDFCPU) Metadata(ctx context.Context, input string) (MetadataResult, error) {
	var info *pdfcpu.PDFInfo
	var properties map[string]string
	err := runPDFCPU(ctx, func() error {
		file, err := os.Open(input)
		if err != nil {
			return err
		}
		defer file.Close()
		conf := model.NewDefaultConfiguration()
		info, err = api.PDFInfo(file, input, nil, false, conf)
		if err != nil {
			return err
		}
		if _, err = file.Seek(0, 0); err != nil {
			return err
		}
		properties, err = api.Properties(file, conf)
		return err
	})
	if err != nil {
		return MetadataResult{}, pdfcpuError(err)
	}
	result := MetadataResult{
		Title: info.Title, Author: info.Author, Subject: info.Subject,
		Keywords: strings.Join(info.Keywords, ", "), Producer: info.Producer,
		Creator: info.Creator, Created: info.CreationDate, Modified: info.ModificationDate,
		Custom: map[string]string{},
	}
	for key, value := range properties {
		key = strings.TrimPrefix(key, "/")
		switch key {
		case "Title":
			result.Title = value
		case "Author":
			result.Author = value
		case "Subject":
			result.Subject = value
		case "Keywords":
			result.Keywords = value
		case "Producer":
			result.Producer = value
		case "Creator":
			result.Creator = value
		case "CreationDate":
			result.Created = value
		case "ModDate":
			result.Modified = value
		default:
			result.Custom[key] = value
		}
	}
	return result, nil
}

func (b *PDFCPU) SetMetadata(ctx context.Context, input, output string, p MetadataParams) error {
	properties := make(map[string]string, len(p.Custom)+4)
	for key, value := range p.Custom {
		properties[key] = value
	}
	if p.Title != "" {
		properties["Title"] = p.Title
	}
	if p.Author != "" {
		properties["Author"] = p.Author
	}
	if p.Subject != "" {
		properties["Subject"] = p.Subject
	}
	if p.Keywords != "" {
		properties["Keywords"] = p.Keywords
	}
	if len(properties) == 0 {
		return fmt.Errorf("metadata requires at least one property")
	}
	return runPDFCPU(ctx, func() error {
		return api.AddPropertiesFile(input, output, properties, model.NewDefaultConfiguration())
	})
}
