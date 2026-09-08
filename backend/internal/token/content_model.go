package token

type StorageBreakdown struct {
	FileType  string `json:"file_type"`
	UsedBytes int64  `json:"used_bytes"`
	Count     int64  `json:"count"`
}

type StorageUsage struct {
	PlanType            string             `json:"plan_type,omitempty"`
	LimitTier           string             `json:"limit_tier,omitempty"`
	UsedBytes           int64              `json:"used_bytes"`
	AllowedBytes        int64              `json:"allowed_bytes"`
	RemainingBytes      int64              `json:"remaining_bytes"`
	IsOverLimit         bool               `json:"is_over_limit"`
	BreakdownByFileType []StorageBreakdown `json:"breakdown_by_file_type"`
}

type LibraryFile struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	FileID            string `json:"file_id"`
	ParentDirectoryID string `json:"parent_directory_id"`
	FileSizeBytes     int64  `json:"file_size_bytes"`
	UpdatedAt         string `json:"updated_at,omitempty"`
	MimeType          string `json:"mime_type,omitempty"`
}

type LibraryPage struct {
	Items  []LibraryFile `json:"items"`
	Cursor string        `json:"cursor,omitempty"`
}

type DeleteLibraryFile struct {
	LibraryFileID     string `json:"library_file_id"`
	FileID            string `json:"file_id"`
	ParentDirectoryID string `json:"parent_directory_id"`
	FileName          string `json:"file_name"`
}

type LibraryDeleteFailure struct {
	DeleteLibraryFile
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

type LibraryDeleteResult struct {
	DeletedLibraryFileIDs []string               `json:"deleted_library_file_ids"`
	DeletedCount          int                    `json:"deleted_count"`
	FailureCount          int                    `json:"failure_count"`
	Failures              []LibraryDeleteFailure `json:"failures"`
}

type ConversationSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CreateTime string `json:"create_time,omitempty"`
	UpdateTime string `json:"update_time,omitempty"`
}

type ConversationPage struct {
	Items      []ConversationSummary `json:"items"`
	Total      *int                  `json:"total"`
	Offset     int                   `json:"offset"`
	Limit      int                   `json:"limit"`
	NextOffset int                   `json:"next_offset"`
	HasMore    bool                  `json:"has_more"`
}

type ConversationAsset struct {
	FileID string `json:"file_id"`
}

type ConversationMessage struct {
	ID         string              `json:"id"`
	Role       string              `json:"role"`
	Author     string              `json:"author"`
	Content    string              `json:"content"`
	CreateTime string              `json:"create_time,omitempty"`
	Assets     []ConversationAsset `json:"assets"`
}

type ConversationDetail struct {
	ConversationSummary
	Messages []ConversationMessage `json:"messages"`
}

type ContentImage struct {
	ContentType string `json:"content_type"`
	DataURL     string `json:"data_url"`
}
