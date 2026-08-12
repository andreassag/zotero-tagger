package processing

import (
	"regexp"
	"sort"
	"strings"
)

func ExtractSpeciesCandidates(text string) []string {
	candidateMap := make(map[string]string)

	binomialRe := regexp.MustCompile(`\b([A-Z][a-z]+)\s+([a-z]{3,})\b`)
	matches := binomialRe.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		full := m[1] + " " + m[2]
		lower := strings.ToLower(full)
		candidateMap[lower] = full
	}

	genusRe := regexp.MustCompile(`\b([A-Z][a-z]+)\s+(?:sp|spp|species)\b`)
	gMatches := genusRe.FindAllStringSubmatch(text, -1)
	for _, m := range gMatches {
		genus := m[1]
		lower := strings.ToLower(genus)
		candidateMap[lower] = genus
	}

	taxonRe := regexp.MustCompile(`\b([A-Z][a-z]+(?:aceae|ales|idae|inae|cocci|bacilli|mycetes))\b`)
	tMatches := taxonRe.FindAllStringSubmatch(text, -1)
	for _, m := range tMatches {
		taxon := m[1]
		lower := strings.ToLower(taxon)
		candidateMap[lower] = taxon
	}

	// Abbreviated binomial: "E. coli", "S. aureus"
	abbrRe := regexp.MustCompile(`\b([A-Z])\.\s*([a-z]{3,})\b`)
	aMatches := abbrRe.FindAllStringSubmatch(text, -1)
	for _, m := range aMatches {
		full := m[1] + ". " + m[2]
		lower := strings.ToLower(full)
		candidateMap[lower] = full
	}

	var results []string
	for _, v := range candidateMap {
		results = append(results, v)
	}

	sort.Strings(results)
	return results
}
