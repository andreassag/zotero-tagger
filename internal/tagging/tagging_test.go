package tagging

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseResponse_ValidJSON(t *testing.T) {
	raw := `{"org_tags": ["org:escherichia-coli"], "group_tags": ["group:enterobacteriaceae"]}`
	result, err := ParseResponse(raw)

	assert.NoError(t, err)
	assert.Equal(t, []string{"org:escherichia-coli"}, result.OrgTags)
	assert.Equal(t, []string{"group:enterobacteriaceae"}, result.GroupTags)
	assert.Empty(t, result.TopicTags)
}

func TestParseResponse_FencedJSON(t *testing.T) {
	raw := "```json\n{\"org_tags\": [\"org:escherichia-coli\"], \"group_tags\": []}\n```"
	result, err := ParseResponse(raw)

	assert.NoError(t, err)
	assert.Equal(t, []string{"org:escherichia-coli"}, result.OrgTags)
}

func TestFormatTag(t *testing.T) {
	tag := FormatTag("Escherichia coli", "org:")
	assert.Equal(t, "org:escherichia-coli", tag)

	tag2 := FormatTag("org:streptococcus pneumoniae", "org:")
	assert.Equal(t, "org:streptococcus-pneumoniae", tag2)
}

func TestBuildTagList(t *testing.T) {
	result := &TagResult{
		OrgTags:   []string{"org:escherichia-coli"},
		GroupTags: []string{"group:enterobacteriaceae"},
		TopicTags: []string{"topic:biofilm"},
	}

	tags := BuildTagList(result, "_ai-tagged")
	assert.Len(t, tags, 4)
	assert.Equal(t, "org:escherichia-coli", tags[0].Tag)
	assert.Equal(t, "group:enterobacteriaceae", tags[1].Tag)
	assert.Equal(t, "topic:biofilm", tags[2].Tag)
	assert.Equal(t, "_ai-tagged", tags[3].Tag)
}

func TestBuildSystemPrompt(t *testing.T) {
	prompt := BuildSystemPrompt()
	assert.Contains(t, prompt, `"org_tags"`)
	assert.Contains(t, prompt, `"group_tags"`)
	assert.NotContains(t, prompt, "{CONTROLLED_TOPICS}")
	assert.NotContains(t, prompt, "topic_tags")
}

func TestBuildTopicDecisionQuestions(t *testing.T) {
	topics := []string{"biofilm", " oral microbiology ", "", "   "}
	questions := BuildTopicDecisionQuestions(topics)

	require.Len(t, questions, 2)

	q1, ok := questions["biofilm"]
	require.True(t, ok)
	assert.Equal(t, "noul", q1.Type)
	assert.Contains(t, q1.Instructions, `"biofilm"`)
	assert.Contains(t, q1.TrueDescription, `"biofilm"`)
	assert.Contains(t, q1.FalseDescription, `"biofilm"`)

	q2, ok := questions["oral microbiology"]
	require.True(t, ok)
	assert.Equal(t, "noul", q2.Type)
	assert.Contains(t, q2.Instructions, `"oral microbiology"`)
}

func TestSelectTopicsFromDecisions(t *testing.T) {
	rawJSON := `{
		"answers": {
			"biofilm": {"type": "noul", "noul": 0.92, "confidence": 0.85},
			"oral microbiology": {"type": "noul", "noul": 0.85, "confidence": 0.90},
			"quorum-sensing": {"type": "noul", "noul": 0.85, "confidence": 0.95},
			"genomics": {"type": "noul", "noul": 0.85, "confidence": 0.95},
			"pcr": {"type": "noul", "noul": 0.69, "confidence": 0.95},
			"metagenomics": {"type": "noul", "noul": 0.95, "confidence": 0.49},
			"microbiome": {"type": "noul", "noul": 0.70, "confidence": 0.50},
			"unlisted-topic": {"type": "noul", "noul": 0.99, "confidence": 0.99}
		}
	}`
	allowed := []string{
		"biofilm",
		"oral microbiology",
		"quorum-sensing",
		"genomics",
		"pcr",
		"metagenomics",
		"microbiome",
		"biofilm",
	}

	// Cap at 3: should select biofilm (0.92), genomics (0.85, conf 0.95, 'g' < 'q'), quorum-sensing (0.85, conf 0.95)
	matches, err := SelectTopicsFromDecisions(rawJSON, allowed, 0.70, 0.50, 3)
	require.NoError(t, err)
	require.Len(t, matches, 3)
	assert.Equal(t, "topic:biofilm", matches[0].Tag)
	assert.Equal(t, "topic:genomics", matches[1].Tag)
	assert.Equal(t, "topic:quorum-sensing", matches[2].Tag)

	// Unlimited (maxTopics = 0): includes oral microbiology (0.85, conf 0.90) and inclusive threshold microbiome (0.70, conf 0.50)
	allMatches, err := SelectTopicsFromDecisions(rawJSON, allowed, 0.70, 0.50, 0)
	require.NoError(t, err)
	require.Len(t, allMatches, 5)
	assert.Equal(t, "topic:oral-microbiology", allMatches[3].Tag)
	assert.Equal(t, "topic:microbiome", allMatches[4].Tag)
}

func TestSelectTopicsFromDecisions_DeduplicatesTagsAndSkipsNonNoul(t *testing.T) {
	rawJSON := `{
		"answers": {
			"Biofilm": {"noul": 0.75, "confidence": 0.80},
			"biofilm": {"type": "noul", "noul": 0.91, "confidence": 0.85},
			"pcr": {"type": "choice", "noul": 0.99, "confidence": 0.99},
			"genomics": {"type": "noul", "noul": 0.80, "confidence": 0.70}
		}
	}`
	allowed := []string{"Biofilm", "biofilm", "pcr", "genomics"}

	matches, err := SelectTopicsFromDecisions(rawJSON, allowed, 0.70, 0.50, 2)
	require.NoError(t, err)
	require.Len(t, matches, 2)
	assert.Equal(t, "topic:biofilm", matches[0].Tag)
	assert.InDelta(t, 0.91, matches[0].Noul, 1e-9)
	assert.Equal(t, "topic:genomics", matches[1].Tag)
}

func TestSelectTopicsFromDecisions_Errors(t *testing.T) {
	_, err := SelectTopicsFromDecisions("not-json", []string{"biofilm"}, 0.7, 0.5, 5)
	assert.Error(t, err)

	_, err = SelectTopicsFromDecisions(`{}`, []string{"biofilm"}, 0.7, 0.5, 5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing answers")

	matches, err := SelectTopicsFromDecisions(`{"answers":{}}`, []string{"biofilm"}, 0.7, 0.5, 5)
	assert.NoError(t, err)
	assert.Empty(t, matches)
}

func TestBuildUserPrompt(t *testing.T) {
	p1 := BuildUserPrompt("Test Title", "Sample abstract", []string{"tag1", "tag2"})
	assert.Contains(t, p1, "Title: Test Title")
	assert.Contains(t, p1, "Existing Tags: tag1, tag2")
	assert.Contains(t, p1, "Sample abstract")

	p2 := BuildUserPrompt("Test Title 2", "", nil)
	assert.Contains(t, p2, "Title: Test Title 2")
	assert.Contains(t, p2, "Existing Tags: none")
	assert.Contains(t, p2, "Please analyze the attached paper document.")
}
