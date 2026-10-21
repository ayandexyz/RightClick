package pdfops

import (
	"archive/zip"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func writeZip(t *testing.T, path string, files [][2]string) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	z := zip.NewWriter(out)
	for _, f := range files {
		w, err := z.Create(f[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(f[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
}

// hostileDocuments builds an ODT and a DOCX that, if LibreOffice were
// generous, would phone home (a linked image on a local listener) and run code
// (a Basic macro bound to the load event that writes a marker file).
func hostileDocuments(t *testing.T, imageURL, marker string) (odt, docx string) {
	t.Helper()
	dir := t.TempDir()
	odt = filepath.Join(dir, "hostile.odt")
	ns := `xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:xlink="http://www.w3.org/1999/xlink" xmlns:script="urn:oasis:names:tc:opendocument:xmlns:script:1.0" xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0" xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"`
	content := `<?xml version="1.0" encoding="UTF-8"?><office:document-content ` + ns + ` office:version="1.3">` +
		`<office:scripts><office:event-listeners><script:event-listener script:language="ooo:script" script:event-name="dom:load" xlink:href="vnd.sun.star.script:Standard.Module1.Main?language=Basic&amp;location=document"/></office:event-listeners></office:scripts>` +
		`<office:body><office:text><text:p>HOSTILEWORD</text:p><text:p><draw:frame draw:name="i" text:anchor-type="paragraph" svg:width="2cm" svg:height="2cm">` +
		`<draw:image xlink:href="` + imageURL + `" xlink:type="simple" xlink:show="embed" xlink:actuate="onLoad"/></draw:frame></text:p></office:text></office:body></office:document-content>`
	manifest := `<?xml version="1.0" encoding="UTF-8"?><manifest:manifest ` + ns + ` manifest:version="1.3"><manifest:file-entry manifest:full-path="/" manifest:media-type="application/vnd.oasis.opendocument.text"/><manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/><manifest:file-entry manifest:full-path="Basic/script-lc.xml" manifest:media-type="text/xml"/><manifest:file-entry manifest:full-path="Basic/Standard/script-lb.xml" manifest:media-type="text/xml"/><manifest:file-entry manifest:full-path="Basic/Standard/Module1.xml" manifest:media-type="text/xml"/></manifest:manifest>`
	module := `<?xml version="1.0" encoding="UTF-8"?><script:module xmlns:script="http://openoffice.org/2000/script" script:name="Module1" script:language="StarBasic">Sub Main
Open "` + marker + `" For Output As #1
Print #1, "ran"
Close #1
End Sub
</script:module>`
	writeZip(t, odt, [][2]string{
		{"mimetype", "application/vnd.oasis.opendocument.text"},
		{"content.xml", content},
		{"META-INF/manifest.xml", manifest},
		{"Basic/script-lc.xml", `<?xml version="1.0" encoding="UTF-8"?><library:libraries xmlns:library="http://openoffice.org/2000/library" xmlns:xlink="http://www.w3.org/1999/xlink"><library:library library:name="Standard" xlink:href="$(USER)/basic/Standard/script.xlb/" xlink:type="simple" library:link="false"/></library:libraries>`},
		{"Basic/Standard/script-lb.xml", `<?xml version="1.0" encoding="UTF-8"?><library:library xmlns:library="http://openoffice.org/2000/library" library:name="Standard" library:readonly="false" library:passwordprotected="false"><library:element library:name="Module1"/></library:library>`},
		{"Basic/Standard/Module1.xml", module},
	})

	docx = filepath.Join(dir, "hostile.docx")
	writeZip(t, docx, [][2]string{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="` + imageURL + `" TargetMode="External"/></Relationships>`},
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><w:body><w:p><w:r><w:t>HOSTILEWORD</w:t></w:r></w:p><w:p><w:r><w:drawing><wp:inline><wp:extent cx="720000" cy="720000"/><wp:docPr id="1" name="x"/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:nvPicPr><pic:cNvPr id="1" name="x"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:link="rId9"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="720000" cy="720000"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p></w:body></w:document>`},
	})
	return odt, docx
}

// A document is untrusted input. Converting it must not make a network request
// for a linked image and must not run a macro. The listener is on loopback and
// counts every request, so a fetch cannot go unnoticed. (A headless convert
// does not run document macros at all; the marker file is how this test would
// notice if that ever changed. No setting short of lowering the macro security
// level made the macro fire here, so there is no positive control for it.)
func TestLibreOfficeConvertFollowsNoLinksAndRunsNoMacros(t *testing.T) {
	requireTool(t, "pdftotext")
	if err := NewLibreOffice(nil).Available(); err != nil {
		t.Skip(err)
	}
	engine := NewLibreOffice(nil)
	var hits atomic.Int32
	// Not loopback: LibreOffice never proxies loopback addresses, so a
	// listener there would hide the proxy half of the protection.
	host := nonLoopbackAddress()
	if host == "" {
		t.Skip("no non-loopback address to listen on")
	}
	listener, err := net.Listen("tcp", host+":0")
	if err != nil {
		t.Skip(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.NotFound(w, nil)
	})}
	go server.Serve(listener)
	defer server.Close()
	imageURL := "http://" + listener.Addr().String() + "/pixel.png"

	// Harness check: the listener sees a request when one is made.
	if resp, err := http.Get(imageURL); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	if hits.Load() != 1 {
		t.Fatalf("the listener missed a direct request (%d hits)", hits.Load())
	}
	hits.Store(0)

	if !engine.isolated {
		t.Log("no network isolation here: the linked-image check is skipped, the macro check still runs")
	}
	marker := filepath.Join(t.TempDir(), "macro-ran.txt")
	odt, docx := hostileDocuments(t, imageURL, marker)
	for name, input := range map[string]string{"odt": odt, "docx": docx} {
		t.Run(name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "out.pdf")
			if err := engine.Convert(context.Background(), input, output); err != nil {
				t.Fatal(err)
			}
			text, err := exec.Command("pdftotext", output, "-").Output()
			if err != nil || !strings.Contains(string(text), "HOSTILEWORD") {
				t.Fatalf("the document did not convert: %v %q", err, text)
			}
			if n := hits.Load(); n != 0 && engine.isolated {
				t.Errorf("converting fetched the linked image %d time(s)", n)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Error("converting ran the document's macro")
			}
		})
	}
}

func nonLoopbackAddress() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() && ipnet.IP.IsGlobalUnicast() {
			return ipnet.IP.String()
		}
	}
	return ""
}
