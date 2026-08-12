package processing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripCitations(t *testing.T) {
	input := "Streptococcus pneumoniae is a human pathogen (Smith et al., 2021). It causes pneumonia [12-15]."
	expected := "Streptococcus pneumoniae is a human pathogen . It causes pneumonia ."
	result := StripCitations(input)
	assert.Equal(t, expected, result)
}

func TestStripBibliography(t *testing.T) {
	input := "Main content of paper\n\nReferences\n1. Smith et al. 2021."
	expected := "Main content of paper\n\n"
	result := StripBibliography(input)
	assert.Equal(t, expected, result)
}

func TestExtractSpeciesCandidates(t *testing.T) {
	input := "We studied Escherichia coli and Streptococcus pneumoniae, along with Enterobacteriaceae."
	candidates := ExtractSpeciesCandidates(input)

	assert.Contains(t, candidates, "Escherichia coli")
	assert.Contains(t, candidates, "Streptococcus pneumoniae")
	assert.Contains(t, candidates, "Enterobacteriaceae")
}

func TestNormalizeWhitespace(t *testing.T) {
	input := "Line 1   with   spaces\n\n\nLine  2"
	expected := "Line 1 with spaces\n\nLine 2"
	result := NormalizeWhitespace(input)
	assert.Equal(t, expected, result)
}
