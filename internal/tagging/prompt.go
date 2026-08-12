package tagging

import (
	"fmt"
	"strings"
)

const SystemPrompt = `You are an expert microbiological taxonomy and topic classifier for academic papers.

Analyze the provided paper metadata and candidate species list.
Select specific organisms (org:), or broader clades/groups (group:) from the paper and tag them (i.e. org: streptococcus constellatus, org: escherichia coli, group: streptococci, group: aspergillus, etc), and 2-5 controlled topics from the provided list (i.e topic: pcr, topic: quorum sensing, topic: oral microbiology, etc.).

RULES:
1. 'org:' must be lowercase binomial format (e.g., 'org:escherichia-coli').
2. 'group:' represents broader taxonomy/traits (e.g., 'group:enterobacteriaceae', 'group:gram-negative'). If paper discuss larger group mainly, only tag group: and not org:.
3. 'topic:' tags MUST be strictly chosen from this controlled list ONLY: {CONTROLLED_TOPICS}. Do not invent new topics.

Pre-extracted Candidate Species: {candidate_species}
Title: {title}
Abstract/Key Text: {processed_text}

Respond STRICTLY in JSON:
{
  "org_tags": ["org:genus-species"],
  "group_tags": ["group:clade-or-trait"],
  "topic_tags": ["topic:controlled-term"]
}`

func BuildSystemPrompt(controlledTopics []string) string {
	topicsStr := strings.Join(controlledTopics, ", ")
	return strings.Replace(SystemPrompt, "{CONTROLLED_TOPICS}", topicsStr, 1)
}

func BuildUserPrompt(title, processedText string, candidateSpecies []string, existingTags []string) string {
	speciesStr := strings.Join(candidateSpecies, ", ")
	tagsStr := strings.Join(existingTags, ", ")

	return fmt.Sprintf(
		"Title: %s\nExisting Tags: %s\nPre-extracted Candidate Species: %s\nAbstract/Key Text:\n%s",
		title, tagsStr, speciesStr, processedText,
	)
}
