package zotero

type Item struct {
	Key     string   `json:"key"`
	Version int      `json:"version"`
	Data    ItemData `json:"data"`
}

type ItemData struct {
	ItemType     string `json:"itemType"`
	Title        string `json:"title"`
	AbstractNote string `json:"abstractNote"`
	Tags         []Tag  `json:"tags"`
	ParentItem   string `json:"parentItem"`
	ContentType  string `json:"contentType"`
	Filename     string `json:"filename"`
}

type Tag struct {
	Tag  string `json:"tag"`
	Type int    `json:"type,omitempty"`
}

type FetchOptions struct {
	CollectionKey string
	GroupID       string
	ItemKey       string
	Limit         int
	ExcludeTag    string
	ItemTypes     []string
	Reprocess     bool
}
