package tagging

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseResponse_ValidJSON(t *testing.T) {
	raw := `{"org_tags": ["org:escherichia-coli"], "group_tags": ["group:enterobacteriaceae"], "topic_tags": ["topic:pcr"]}`
	result, err := ParseResponse(raw)

	assert.NoError(t, err)
	assert.Equal(t, []string{"org:escherichia-coli"}, result.OrgTags)
	assert.Equal(t, []string{"group:enterobacteriaceae"}, result.GroupTags)
	assert.Equal(t, []string{"topic:pcr"}, result.TopicTags)
}

func TestParseResponse_FencedJSON(t *testing.T) {
	raw := "```json\n{\"org_tags\": [\"org:escherichia-coli\"], \"group_tags\": [], \"topic_tags\": []}\n```"
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
