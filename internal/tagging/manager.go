package tagging

import (
	"strings"

	"github.com/andreassag/zotero-tagger/internal/zotero"
)

func FormatTag(tag string, prefix string) string {
	tag = strings.TrimSpace(tag)
	tag = strings.TrimPrefix(tag, prefix)

	tag = strings.ToLower(tag)
	tag = strings.ReplaceAll(tag, " ", "-")
	tag = strings.ReplaceAll(tag, "_", "-")

	return prefix + tag
}

func BuildTagList(result *TagResult, sentinelTag string) []zotero.Tag {
	tagMap := make(map[string]bool)
	var zoteroTags []zotero.Tag

	addTag := func(t string) {
		if !tagMap[t] && t != "" {
			tagMap[t] = true
			zoteroTags = append(zoteroTags, zotero.Tag{Tag: t})
		}
	}

	for _, t := range result.OrgTags {
		addTag(FormatTag(t, "org:"))
	}

	for _, t := range result.GroupTags {
		addTag(FormatTag(t, "group:"))
	}

	for _, t := range result.TopicTags {
		addTag(FormatTag(t, "topic:"))
	}

	if sentinelTag != "" {
		addTag(sentinelTag)
	}

	return zoteroTags
}
