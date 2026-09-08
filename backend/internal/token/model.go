package token

type Record struct {
	ID               string
	Name             string
	Email            string
	AccessToken      string
	SessionToken     string
	AccessExpiresAt  string
	Status           string
	CheckState       string
	Note             string
	NextRefreshAt    *int64
	LastCheckedAt    *int64
	LastRefreshedAt  *int64
	LastErrorCode    string
	LastErrorMessage string
	Version          int
	CreatedAt        int64
	UpdatedAt        int64
}

type View struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Email              string `json:"email"`
	AccessTokenMasked  string `json:"access_token_masked"`
	SessionTokenMasked string `json:"session_token_masked"`
	HasSessionToken    bool   `json:"has_session_token"`
	AccessExpiresAt    string `json:"access_expires_at,omitempty"`
	Status             string `json:"status"`
	CheckState         string `json:"check_state"`
	Note               string `json:"note"`
	NextRefreshAt      *int64 `json:"next_refresh_at,omitempty"`
	LastCheckedAt      *int64 `json:"last_checked_at,omitempty"`
	LastRefreshedAt    *int64 `json:"last_refreshed_at,omitempty"`
	LastErrorCode      string `json:"last_error_code,omitempty"`
	LastErrorMessage   string `json:"last_error_message,omitempty"`
	Version            int    `json:"version"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

type EditView struct {
	View
	AccessToken  string `json:"access_token"`
	SessionToken string `json:"session_token"`
}

type CreateInput struct {
	Name            string `json:"name"`
	Email           string `json:"email"`
	AccessToken     string `json:"access_token"`
	SessionToken    string `json:"session_token"`
	AccessExpiresAt string `json:"access_expires_at"`
	Status          string `json:"status"`
	Note            string `json:"note"`
}

type UpdateInput struct {
	Name            *string `json:"name"`
	Email           *string `json:"email"`
	AccessToken     *string `json:"access_token"`
	SessionToken    *string `json:"session_token"`
	AccessExpiresAt *string `json:"access_expires_at"`
	Status          *string `json:"status"`
	Note            *string `json:"note"`
	Version         int     `json:"version"`
}

type ListFilter struct {
	Page     int
	PageSize int
	Search   string
	Status   string
	Sort     string
	Order    string
}

type Attempt struct {
	ID           string `json:"id"`
	TokenID      string `json:"token_id,omitempty"`
	TargetLabel  string `json:"target_label"`
	Operation    string `json:"operation"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	StartedAt    int64  `json:"started_at"`
	CompletedAt  *int64 `json:"completed_at,omitempty"`
}
