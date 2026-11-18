package tagging

import (
	"fmt"
	"strings"
)

const SystemPrompt = `You are an expert microbiological taxonomy classifier for academic papers.

Analyze the provided paper document or text, title, and existing tags.
Identify the specific organisms (org:) and broader taxonomic clades or traits (group:) that represent the CORE SCIENTIFIC FOCUS of the paper.

RULES:
1. 'org:' tags MUST be lowercase binomial species names with hyphens (e.g. 'org:actinobacillus-actinomycetemcomitans', 'org:streptococcus-pneumoniae').
2. 'group:' tags represent broader taxonomic clades, families, or phenotypic traits (e.g. 'group:pasteurellaceae', 'group:gram-negative', 'group:mycobacteria', 'group:fungi').
3. CRITICAL ORGANISM SELECTION RULE: Only tag organisms ('org:') or groups ('group:') that are the PRIMARY SUBJECT or CORE FOCUS of the research paper. DO NOT tag model organisms, expression hosts, laboratory helper strains, or cloning vectors (such as Escherichia coli, Saccharomyces cerevisiae, or phages) IF THEY ARE ONLY USED AS CLONING HOSTS, EXPRESSION SYSTEMS, OR ROUTINE METHODOLOGY TOOLS in the experiments.

Respond STRICTLY in JSON:
{
  "org_tags": ["org:genus-species"],
  "group_tags": ["group:clade-or-trait"]
}`

type DecisionQuestion struct {
	Type             string `json:"type"`
	Instructions     string `json:"instructions"`
	TrueDescription  string `json:"true_description,omitempty"`
	FalseDescription string `json:"false_description,omitempty"`
}

func BuildSystemPrompt() string {
	return SystemPrompt
}

func BuildTopicDecisionQuestions(controlledTopics []string) map[string]DecisionQuestion {
	questions := make(map[string]DecisionQuestion, len(controlledTopics))
	for _, topic := range controlledTopics {
		clean := strings.TrimSpace(topic)
		if clean == "" {
			continue
		}
		questions[clean] = DecisionQuestion{
			Type:             "noul",
			Instructions:     fmt.Sprintf("Is %q a primary research subject, core scientific focus, or central methodology of this paper?", clean),
			TrueDescription:  fmt.Sprintf("The paper directly investigates, measures, models, or centers on %q as a core scientific topic or primary methodology.", clean),
			FalseDescription: fmt.Sprintf("The paper does not cover %q, or only mentions it in passing as background context, related literature, or routine boilerplate.", clean),
		}
	}
	return questions
}

func BuildUserPrompt(title, extraText string, existingTags []string) string {
	tagsStr := strings.Join(existingTags, ", ")
	if tagsStr == "" {
		tagsStr = "none"
	}

	if strings.TrimSpace(extraText) != "" {
		return fmt.Sprintf("Title: %s\nExisting Tags: %s\nAbstract/Context:\n%s", title, tagsStr, extraText)
	}
	return fmt.Sprintf("Title: %s\nExisting Tags: %s\nPlease analyze the attached paper document.", title, tagsStr)
}
