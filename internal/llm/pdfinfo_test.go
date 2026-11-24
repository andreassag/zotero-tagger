package llm

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTempPDF(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.pdf")
	require.NoError(t, os.WriteFile(p, []byte(content), 0600))
	return p
}

func TestInspectPDF_HashSizeAndPages(t *testing.T) {
	content := "%PDF-1.4\n1 0 obj<</Type/Pages/Count 2>>endobj\n2 0 obj<</Type /Page>>endobj\n3 0 obj<< /Type/Page\n>>endobj\n"
	info, err := InspectPDF(writeTempPDF(t, content))
	require.NoError(t, err)
	assert.Equal(t, sha256.Sum256([]byte(content)), info.SHA256)
	assert.Equal(t, int64(len(content)), info.Size)
	assert.Equal(t, 2, info.Pages) // /Pages must not be counted
	assert.Equal(t, 2*pdfTokensPerPage, info.EstimatedTokens())
}

func TestPageCounter_MatchAcrossChunkBoundary(t *testing.T) {
	doc := strings.Repeat("x", 100) + "/Type /Page>>" + strings.Repeat("y", 100) + "/Type/Pages"
	for split := 1; split < len(doc); split++ {
		pc := &pageCounter{}
		_, _ = pc.Write([]byte(doc[:split]))
		_, _ = pc.Write([]byte(doc[split:]))
		assert.Equal(t, 1, pc.count, "split=%d", split)
	}
}

func TestPDFInfo_SizeFallbackAndLargerDocs(t *testing.T) {
	small := PDFInfo{Size: 100}
	assert.Equal(t, pdfTokensPerPage, small.EstimatedTokens())
	big := PDFInfo{Size: 10 * pdfBytesPerPage}
	assert.Equal(t, 10*pdfTokensPerPage, big.EstimatedTokens())
	assert.Greater(t, big.EstimatedTokens(), small.EstimatedTokens())
}
