package overview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	platformsqlite "account-hub/internal/platform/sqlite"
)

func TestOverviewReadsExistingTablesWithoutWriting(t *testing.T) {
	db, err := platformsqlite.Open(context.Background(), filepath.Join(t.TempDir(), "overview.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO tokens(id,access_token,status,created_at,updated_at) VALUES
        ('enabled','dummy-enabled','enabled',1,1),('disabled','dummy-disabled','disabled',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA query_only = ON"); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	NewHandler(db).Get(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil))
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || len(body.Data) != 3 || body.Data["trend"] != nil {
		t.Fatalf("want only two providers and observation time: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var actual counts
	if err := json.Unmarshal(body.Data["tokens"], &actual); err != nil {
		t.Fatal(err)
	}
	if actual != (counts{Total: 2, Enabled: 1, Disabled: 1}) {
		t.Fatalf("counts=%+v", actual)
	}
	var extraTables int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='overview_snapshots'").Scan(&extraTables); err != nil || extraTables != 0 {
		t.Fatalf("unexpected snapshot table: count=%d err=%v", extraTables, err)
	}
}
