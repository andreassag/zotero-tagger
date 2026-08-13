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
	text = StripMaterialsAndMethods(text)
	text = StripLegendsAndMetadata(text)
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
	bibRe := regexp.MustCompile(`(?mi)^\s*(?:\d+[\.\)]\s*|\d+\s+)?(?:r\s*e\s*f\s*e\s*r\s*e\s*n\s*c\s*e\s*s|references|bibliography|literature\s+cited|works\s+cited)\b.*$`)
	loc := bibRe.FindStringIndex(text)
	if loc != nil {
		return strings.TrimRight(text[:loc[0]], " \t\r\n") + "\n\n"
	}
	return text
}

func StripMaterialsAndMethods(text string) string {
	methodsRe := regexp.MustCompile(`(?mi)^\s*(?:\d+[\.\)]\s*|\d+\s+)?(materials?\s+(?:and|&)\s+methods?|experimental\s+procedures?|methodology|methods?)\b.*$`)
	nextSecRe := regexp.MustCompile(`(?mi)^\s*(?:\d+[\.\)]\s*|\d+\s+)?(results?(?:\s+and\s+discussion)?|discussion(?:\s+and\s+conclusions?)?|conclusions?|references|bibliography|acknowledg?ments?|funding|financial|declarations?|supplementary)\b.*$`)

	loc := methodsRe.FindStringIndex(text)
	if loc == nil {
		return text
	}

	start := loc[0]
	subText := text[loc[1]:]
	nextLoc := nextSecRe.FindStringIndex(subText)
	if nextLoc != nil {
		end := loc[1] + nextLoc[0]
		return text[:start] + "\n\n" + text[end:]
	}

	return strings.TrimRight(text[:start], " \t\r\n") + "\n\n"
}

func StripLegendsAndMetadata(text string) string {
	lines := strings.Split(text, "\n")
	var cleanLines []string

	legendRe := regexp.MustCompile(`(?i)^\s*(?:fig(?:ure)?|table)\s*\d+[\.:]`)
	metadataRe := regexp.MustCompile(`(?i)\b(?:downloaded from|copyright|author contributions|competing interests|financial support|funding|data availability)\b`)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if legendRe.MatchString(trimmed) || metadataRe.MatchString(trimmed) {
			continue
		}
		cleanLines = append(cleanLines, line)
	}

	return strings.Join(cleanLines, "\n")
}

func ExtractSections(text string, sectionPriority []string, introParagraphs int) string {
	headerRe := regexp.MustCompile(`(?mi)^\s*(?:[I|V|X\d]+[\.\)]\s*|\d+\s+)?(abstract|introduction|results?(?:\s+and\s+discussion)?|discussion(?:\s+and\s+conclusions?)?|conclusions?)\b.*$`)
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
		rawName := strings.ToLower(text[loc[2]:loc[3]])
		name := rawName
		if strings.Contains(rawName, "abstract") {
			name = "abstract"
		} else if strings.Contains(rawName, "introduction") {
			name = "introduction"
		} else if strings.Contains(rawName, "conclusion") || strings.Contains(rawName, "discussion") {
			name = "conclusion"
		}

		start := loc[0]
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		sections = append(sections, section{name: name, start: start, end: end})
	}

	secMap := make(map[string]string)
	for _, sec := range sections {
		if _, ok := secMap[sec.name]; !ok {
			secMap[sec.name] = text[sec.start:sec.end]
		}
	}

	var sb strings.Builder

	for _, priority := range sectionPriority {
		key := strings.ToLower(strings.TrimSpace(priority))

		if key == "conclusion" {
			if content, ok := secMap["conclusion"]; ok {
				sb.WriteString(content)
				sb.WriteString("\n\n")
			}
			continue
		}

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
