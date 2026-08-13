package tagging

import (
	"fmt"
	"strings"
)

const SystemPrompt = `You are an expert microbiological taxonomy and topic classifier for academic papers.

Analyze the provided paper title, abstract/text, candidate species list, and existing tags.
Select specific organisms (org:), or broader clades/groups (group:) that are the CORE SCIENTIFIC FOCUS of the paper, and 2-5 controlled topics from the provided list.

RULES:
1. 'org:' tags MUST be lowercase binomial species names with hyphens (e.g. 'org:actinobacillus-actinomycetemcomitans').
2. 'group:' tags represent broader taxonomic clades or biological groups (e.g. 'group:pasteurellaceae', 'group:gram-negative').
3. 'topic:' tags MUST be strictly chosen from this controlled list ONLY: {CONTROLLED_TOPICS}. Do not invent new topics.
4. CRITICAL ORGANISM SELECTION RULE: Only tag organisms ('org:') or groups ('group:') that are the PRIMARY SUBJECT or CORE FOCUS of the research paper. DO NOT tag model organisms, expression hosts, laboratory helper strains, or cloning vectors (such as Escherichia coli, Saccharomyces cerevisiae, or phages) IF THEY ARE ONLY USED AS CLONING HOSTS, EXPRESSION SYSTEMS, OR ROUTINE METHODOLOGY TOOLS in the experiments.

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
