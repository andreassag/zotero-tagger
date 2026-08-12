package processing

import (
	"regexp"
	"strings"

	"github.com/exterex/zotero-tagger/internal/config"
)

type ProcessedText struct {
	Text               string
	OriginalWordCount  int
	ProcessedWordCount int
	CandidateSpecies   []string
}

func ProcessText(rawText string, cfg config.ProcessingConfig, maxInputTokens int) ProcessedText {
	origWords := countWords(rawText)

	text := StripCitations(rawText)
	text = StripBibliography(text)
	text = ExtractSections(text, cfg.SectionPriority, cfg.IntroParagraphs)
	text = NormalizeWhitespace(text)

	if maxInputTokens > 0 {
		text = TruncateToTokenBudget(text, maxInputTokens)
	}

	procWords := countWords(text)
	species := ExtractSpeciesCandidates(text)

	return ProcessedText{
		Text:               text,
		OriginalWordCount:  origWords,
		ProcessedWordCount: procWords,
		CandidateSpecies:   species,
	}
}

func StripCitations(text string) string {
	parenRe := regexp.MustCompile(`\([A-Z][a-z]+(?:\s+et\s+al\.?|\s+&\s+[A-Z][a-z]+)?,?\s*\d{4}[a-z]?(?:;\s*[A-Z][a-z]+(?:\s+et\s+al\.?|\s+&\s+[A-Z][a-z]+)?,?\s*\d{4}[a-z]?)*\)`)
	text = parenRe.ReplaceAllString(text, "")

	bracketRe := regexp.MustCompile(`\[\d+(?:[,;–-]\s*\d+)*\]`)
	text = bracketRe.ReplaceAllString(text, "")

	return text
}

func StripBibliography(text string) string {
	bibRe := regexp.MustCompile(`(?mi)^\s*(references|bibliography|literature\s+cited|works\s+cited)\s*$`)
	loc := bibRe.FindStringIndex(text)
	if loc != nil {
		return strings.TrimRight(text[:loc[0]], " \t\r\n") + "\n\n"
	}
	return text
}

func ExtractSections(text string, sectionPriority []string, introParagraphs int) string {
	headerRe := regexp.MustCompile(`(?mi)^\s*(abstract|introduction|methods?|results?|discussion|conclusion)\s*$`)
	locs := headerRe.FindAllStringSubmatchIndex(text, -1)

	if len(locs) == 0 {
		return text
	}

	type section struct {
		name  string
		start int
		end   int
	}

	var sections []section
	for i, loc := range locs {
		name := strings.ToLower(text[loc[2]:loc[3]])
		start := loc[0]
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		sections = append(sections, section{name: name, start: start, end: end})
	}

	secMap := make(map[string]string)
	for _, sec := range sections {
		secMap[sec.name] = text[sec.start:sec.end]
	}

	var sb strings.Builder

	for _, priority := range sectionPriority {
		key := strings.ToLower(strings.TrimSpace(priority))

		// "conclusion" falls back to "discussion" if not present
		if key == "conclusion" {
			if content, ok := secMap["conclusion"]; ok {
				sb.WriteString(content)
				sb.WriteString("\n\n")
				continue
			}
			if content, ok := secMap["discussion"]; ok {
				sb.WriteString(content)
				sb.WriteString("\n\n")
			}
			continue
		}

		// "introduction" uses paragraph trimming
		if key == "introduction" {
			if intro, ok := secMap["introduction"]; ok {
				paras := strings.Split(intro, "\n\n")
				if len(paras) <= introParagraphs+1 {
					sb.WriteString(intro)
				} else {
					for i := 0; i < introParagraphs && i < len(paras); i++ {
						sb.WriteString(paras[i])
						sb.WriteString("\n\n")
					}
					sb.WriteString(paras[len(paras)-1])
				}
				sb.WriteString("\n\n")
			}
			continue
		}

		if content, ok := secMap[key]; ok {
			sb.WriteString(content)
			sb.WriteString("\n\n")
		}
	}

	result := sb.String()
	if strings.TrimSpace(result) == "" {
		return text
	}
	return result
}

func NormalizeWhitespace(text string) string {
	spaceRe := regexp.MustCompile(`[ \t]+`)
	paras := strings.Split(text, "\n")
	var cleanLines []string

	for _, line := range paras {
		trimmed := strings.TrimSpace(spaceRe.ReplaceAllString(line, " "))
		cleanLines = append(cleanLines, trimmed)
	}

	result := strings.Join(cleanLines, "\n")
	multiNLRe := regexp.MustCompile(`\n{3,}`)
	result = multiNLRe.ReplaceAllString(result, "\n\n")
	return strings.TrimSpace(result)
}

func TruncateToTokenBudget(text string, maxTokens int) string {
	maxWords := int(float64(maxTokens) / 1.3)
	words := strings.Fields(text)
	if len(words) <= maxWords {
		return text
	}
	return strings.Join(words[:maxWords], " ")
}

func countWords(text string) int {
	return len(strings.Fields(text))
}
