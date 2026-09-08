package dreamina

type Record struct {
	ID               string
	Email            string
	Password         string
	SessionID        string
	SessionExpiresAt string
	Status           string
	CheckState       string
	CreditBalance    string
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
	ID               string `json:"id"`
	Email            string `json:"email"`
	PasswordMasked   string `json:"password_masked"`
	SessionIDMasked  string `json:"session_id_masked"`
	HasSessionID     bool   `json:"has_session_id"`
	SessionExpiresAt string `json:"session_expires_at,omitempty"`
	Status           string `json:"status"`
	CheckState       string `json:"check_state"`
	CreditBalance    string `json:"credit_balance,omitempty"`
	Note             string `json:"note"`
	NextRefreshAt    *int64 `json:"next_refresh_at,omitempty"`
	LastCheckedAt    *int64 `json:"last_checked_at,omitempty"`
	LastRefreshedAt  *int64 `json:"last_refreshed_at,omitempty"`
	LastErrorCode    string `json:"last_error_code,omitempty"`
	LastErrorMessage string `json:"last_error_message,omitempty"`
	Version          int    `json:"version"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

type EditView struct {
	View
	Password  string `json:"password"`
	SessionID string `json:"session_id"`
}

type CreateInput struct {
	Email            string `json:"email"`
	Password         string `json:"password"`
	SessionID        string `json:"session_id"`
	SessionExpiresAt string `json:"session_expires_at"`
	Status           string `json:"status"`
	Note             string `json:"note"`
}

type UpdateInput struct {
	Email            *string `json:"email"`
	Password         *string `json:"password"`
	SessionID        *string `json:"session_id"`
	SessionExpiresAt *string `json:"session_expires_at"`
	Status           *string `json:"status"`
	Note             *string `json:"note"`
	Version          int     `json:"version"`
}

type ListFilter struct {
	Page, PageSize int
	Search         string
	Status         string
	Sort           string
	Order          string
}

type Attempt struct {
	ID           string `json:"id"`
	AccountID    string `json:"account_id,omitempty"`
	TargetLabel  string `json:"target_label"`
	Operation    string `json:"operation"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	StartedAt    int64  `json:"started_at"`
	CompletedAt  *int64 `json:"completed_at,omitempty"`
}

type LoginResult struct {
	SessionID        string
	SessionExpiresAt string
}

type CreditResult struct {
	Balance string
	Raw     any
}
