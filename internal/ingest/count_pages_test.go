package ingest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCountPages_ReturnsPDFPageCount(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "two-pages.pdf")
	if err := os.WriteFile(filePath, minimalPDF(t, 2), 0o644); err != nil {
		t.Fatalf("write pdf fixture: %v", err)
	}

	got, err := CountPages(filePath)
	if err != nil {
		t.Fatalf("CountPages: %v", err)
	}
	if got != 2 {
		t.Fatalf("page count: got %d, want 2", got)
	}
}

func minimalPDF(t *testing.T, pageCount int) []byte {
	t.Helper()

	if pageCount < 1 {
		t.Fatal("pageCount must be at least 1")
	}

	var buf bytes.Buffer
	offsets := []int{0}
	writeObject := func(id int, body string) {
		for len(offsets) <= id {
			offsets = append(offsets, 0)
		}
		offsets[id] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", id, body)
	}

	buf.WriteString("%PDF-1.4\n")
	kids := make([]string, 0, pageCount)
	for i := 0; i < pageCount; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", 3+i))
	}
	writeObject(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObject(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", joinPDFRefs(kids), pageCount))
	for i := 0; i < pageCount; i++ {
		writeObject(3+i, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 72 72] >>")
	}

	xrefOffset := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(offsets))
	buf.WriteString("0000000000 65535 f \n")
	for id := 1; id < len(offsets); id++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Root 1 0 R /Size %d >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xrefOffset)

	return buf.Bytes()
}

func joinPDFRefs(values []string) string {
	var buf bytes.Buffer
	for i, value := range values {
		if i > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(value)
	}
	return buf.String()
}
