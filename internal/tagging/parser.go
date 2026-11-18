package tagging

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type TagResult struct {
	OrgTags   []string `json:"org_tags"`
	GroupTags []string `json:"group_tags"`
	TopicTags []string `json:"topic_tags"`
}

type DecisionAnswer struct {
	Type       string  `json:"type,omitempty"`
	Noul       float64 `json:"noul"`
	Confidence float64 `json:"confidence"`
}

type DecisionResponse struct {
	Answers map[string]DecisionAnswer `json:"answers"`
}

type TopicMatch struct {
	Topic      string
	Tag        string
	Noul       float64
	Confidence float64
}

func ParseResponse(raw string) (*TagResult, error) {
	var result TagResult
	if err := json.Unmarshal([]byte(raw), &result); err == nil {
		return &result, nil
	}

	fenceRe := regexp.MustCompile("(?s)```(?:json)?\\s*(.+?)\\s*```")
	if matches := fenceRe.FindStringSubmatch(raw); len(matches) > 1 {
		if err := json.Unmarshal([]byte(matches[1]), &result); err == nil {
			return &result, nil
		}
	}

	return nil, fmt.Errorf("failed to parse JSON from LLM response")
}

func SelectTopicsFromDecisions(rawJSON string, allowedTopics []string, probThreshold, confThreshold float64, maxTopics int) ([]TopicMatch, error) {
	var resp DecisionResponse
	if err := json.Unmarshal([]byte(rawJSON), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse decision response JSON: %w", err)
	}
	if resp.Answers == nil {
		return nil, fmt.Errorf("decision response missing answers")
	}

	seen := make(map[string]bool, len(allowedTopics))
	matches := make([]TopicMatch, 0)
	for _, topic := range allowedTopics {
		clean := strings.TrimSpace(topic)
		if clean == "" || seen[clean] {
			continue
		}
		seen[clean] = true

		ans, ok := resp.Answers[clean]
		if !ok {
			continue
		}
		if ans.Type != "" && ans.Type != "noul" {
			continue
		}
		if ans.Noul >= probThreshold && ans.Confidence >= confThreshold {
			matches = append(matches, TopicMatch{
				Topic:      clean,
				Tag:        FormatTag(clean, "topic:"),
				Noul:       ans.Noul,
				Confidence: ans.Confidence,
			})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Noul != matches[j].Noul {
			return matches[i].Noul > matches[j].Noul
		}
		if matches[i].Confidence != matches[j].Confidence {
			return matches[i].Confidence > matches[j].Confidence
		}
		return matches[i].Topic < matches[j].Topic
	})

	deduped := make([]TopicMatch, 0, len(matches))
	seenTags := make(map[string]bool, len(matches))
	for _, m := range matches {
		if seenTags[m.Tag] {
			continue
		}
		seenTags[m.Tag] = true
		deduped = append(deduped, m)
	}
	matches = deduped

	if maxTopics > 0 && len(matches) > maxTopics {
		matches = matches[:maxTopics]
	}

	return matches, nil
}
