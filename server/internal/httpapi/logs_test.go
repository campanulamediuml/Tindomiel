package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tindomiel/internal/logging"
)

func TestFrontendLogsUseIndependentRollingFile(t *testing.T) {
	directory := t.TempDir()
	backendPath := filepath.Join(directory, "tindomiel.log")
	frontendPath := filepath.Join(directory, "frontend.log")
	_, closeBackend, err := logging.Setup(backendPath, "")
	if err != nil {
		t.Fatal(err)
	}
	defer closeBackend()
	frontendLogger, closeFrontend, err := logging.Setup(frontendPath, "")
	if err != nil {
		t.Fatal(err)
	}
	defer closeFrontend()
	server := New("test-token", "", nil, nil, nil, backendPath, frontendPath, frontendLogger)

	body := bytes.NewBufferString(`{"message":"本机资源采样 processCount=7","level":"warn"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/logs/frontend", body)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected write status: %d %s", response.Code, response.Body.String())
	}

	frontendData, err := os.ReadFile(frontendPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frontendData), `"msg":"frontend_event"`) || !strings.Contains(string(frontendData), "processCount=7") || !strings.Contains(string(frontendData), `"level":"WARN"`) {
		t.Fatalf("unexpected frontend log: %s", frontendData)
	}
	backendData, err := os.ReadFile(backendPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(backendData), "processCount=7") {
		t.Fatalf("frontend event leaked into backend log: %s", backendData)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/logs/frontend/recent?lines=10", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "processCount=7") || !strings.Contains(response.Body.String(), frontendPath) {
		t.Fatalf("unexpected frontend log response: %d %s", response.Code, response.Body.String())
	}
}
