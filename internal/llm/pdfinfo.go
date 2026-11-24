package llm

import (
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const (
	// pdfFallbackTokens is used only when no local file information is available.
	pdfFallbackTokens = 2000
	// pdfTokensPerPage is a conservative per-page cost; actual usage is reconciled after each response.
	pdfTokensPerPage = 560
	// pdfBytesPerPage is the size-based fallback when page objects are not visible (compressed object streams).
	pdfBytesPerPage = 60 * 1024

	pdfScanTailBytes = 64
)

var pdfPageObjRe = regexp.MustCompile(`/Type\s*/Page[^s]`)

// PDFInfo describes a PDF file, computed in a single streaming pass.
type PDFInfo struct {
	SHA256 [sha256.Size]byte
	Size   int64
	Pages  int // 0 when page objects were not detectable
}

// EstimatedTokens returns a conservative document token estimate.
func (p PDFInfo) EstimatedTokens() int {
	pages := p.Pages
	if pages == 0 {
		pages = max(1, int(p.Size/pdfBytesPerPage))
	}
	return pages * pdfTokensPerPage
}

// pageCounter is an io.Writer counting "/Type /Page" objects across chunk boundaries.
type pageCounter struct {
	tail  []byte
	count int
}

func (pc *pageCounter) Write(p []byte) (int, error) {
	buf := make([]byte, 0, len(pc.tail)+len(p))
	buf = append(buf, pc.tail...)
	buf = append(buf, p...)

	for _, loc := range pdfPageObjRe.FindAllIndex(buf, -1) {
		// Matches ending inside the previously scanned tail were already counted.
		if loc[1] > len(pc.tail) {
			pc.count++
		}
	}

	keep := min(pdfScanTailBytes, len(buf))
	pc.tail = append(pc.tail[:0], buf[len(buf)-keep:]...)
	return len(p), nil
}

// InspectPDF streams the file once, computing its SHA-256, size and page count with constant memory.
func InspectPDF(path string) (PDFInfo, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return PDFInfo{}, err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	pc := &pageCounter{}
	n, err := io.Copy(io.MultiWriter(h, pc), f)
	if err != nil {
		return PDFInfo{}, err
	}

	var info PDFInfo
	copy(info.SHA256[:], h.Sum(nil))
	info.Size = n
	info.Pages = pc.count
	return info, nil
}
