package processing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractSpeciesCandidates_Binomial(t *testing.T) {
	text := "The paper discusses Escherichia coli and Streptococcus pneumoniae in detail."
	candidates := ExtractSpeciesCandidates(text)
	assert.Contains(t, candidates, "Escherichia coli")
	assert.Contains(t, candidates, "Streptococcus pneumoniae")
}

func TestExtractSpeciesCandidates_AbbreviatedBinomial(t *testing.T) {
	text := "Samples of E. coli and S. aureus were analyzed."
	candidates := ExtractSpeciesCandidates(text)
	assert.Contains(t, candidates, "E. coli")
	assert.Contains(t, candidates, "S. aureus")
}

func TestExtractSpeciesCandidates_GenusAndSpp(t *testing.T) {
	text := "Various Streptococcus sp. and Candida spp. isolate strains."
	candidates := ExtractSpeciesCandidates(text)
	assert.Contains(t, candidates, "Streptococcus")
	assert.Contains(t, candidates, "Candida")
}

func TestExtractSpeciesCandidates_TaxonSuffixes(t *testing.T) {
	text := "Members of Enterobacteriaceae and Streptococci family."
	candidates := ExtractSpeciesCandidates(text)
	assert.Contains(t, candidates, "Enterobacteriaceae")
	assert.Contains(t, candidates, "Streptococci")
}

func TestExtractSpeciesCandidates_DeduplicationAndSorting(t *testing.T) {
	text := "escherichia coli, Escherichia coli, E. coli, e. coli"
	candidates := ExtractSpeciesCandidates(text)
	// Lowercase deduplication
	assert.Len(t, candidates, 2) // "Escherichia coli", "E. coli"
}
