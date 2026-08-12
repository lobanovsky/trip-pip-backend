package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testLogger writes JSON records into buf, keeping everything at or above level.
func testLogger(buf *bytes.Buffer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: level}))
}

// logRecords decodes every JSON record the logger has written so far.
func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var records []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode log record: %v", err)
		}
		records = append(records, record)
	}

	return records
}

// doRequest runs one request through the full handler and returns the response.
func doRequest(buf *bytes.Buffer, level slog.Level, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	NewHandler(testLogger(buf, level), testVersion).ServeHTTP(response, request)

	return response
}

func TestLoggingRecordsRequest(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	doRequest(&buf, slog.LevelInfo, httptest.NewRequest(http.MethodGet, versionPath, nil))

	records := logRecords(t, &buf)
	if len(records) != 1 {
		t.Fatalf("got %d log records, want 1: %s", len(records), buf.String())
	}

	record := records[0]
	if record["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", record["level"])
	}
	if record["msg"] != "request" {
		t.Errorf("msg = %v, want request", record["msg"])
	}
	if record["method"] != http.MethodGet {
		t.Errorf("method = %v, want %s", record["method"], http.MethodGet)
	}
	if record["path"] != versionPath {
		t.Errorf("path = %v, want %s", record["path"], versionPath)
	}
	if record["status"] != float64(http.StatusOK) {
		t.Errorf("status = %v, want %d", record["status"], http.StatusOK)
	}
	if bytesWritten, ok := record["bytes"].(float64); !ok || bytesWritten == 0 {
		t.Errorf("bytes = %v, want a non-zero number", record["bytes"])
	}
	if _, ok := record["duration_ms"]; !ok {
		t.Error("duration_ms is missing")
	}
	if id, ok := record["request_id"].(string); !ok || id == "" {
		t.Errorf("request_id = %v, want a non-empty string", record["request_id"])
	}
}

func TestLoggingOmitsQueryString(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	doRequest(&buf, slog.LevelInfo, httptest.NewRequest(http.MethodGet, versionPath+"?passport=1234567890", nil))

	records := logRecords(t, &buf)
	if len(records) != 1 {
		t.Fatalf("got %d log records, want 1", len(records))
	}
	if records[0]["path"] != versionPath {
		t.Errorf("path = %v, want %s without the query string", records[0]["path"], versionPath)
	}
	if bytes.Contains(buf.Bytes(), []byte("1234567890")) {
		t.Errorf("query string leaked into the log: %s", buf.String())
	}
}

func TestLoggingKeepsClientRequestID(t *testing.T) {
	t.Parallel()

	const clientID = "trace-from-client"

	request := httptest.NewRequest(http.MethodGet, versionPath, nil)
	request.Header.Set(requestIDHeader, clientID)

	var buf bytes.Buffer
	response := doRequest(&buf, slog.LevelInfo, request)

	if got := response.Header().Get(requestIDHeader); got != clientID {
		t.Errorf("response %s = %q, want %q", requestIDHeader, got, clientID)
	}

	records := logRecords(t, &buf)
	if len(records) != 1 {
		t.Fatalf("got %d log records, want 1", len(records))
	}
	if records[0]["request_id"] != clientID {
		t.Errorf("request_id = %v, want %q", records[0]["request_id"], clientID)
	}
}

func TestLoggingGeneratesRequestID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	response := doRequest(&buf, slog.LevelInfo, httptest.NewRequest(http.MethodGet, versionPath, nil))

	header := response.Header().Get(requestIDHeader)
	if header == "" {
		t.Fatalf("response %s is empty, want a generated id", requestIDHeader)
	}

	records := logRecords(t, &buf)
	if len(records) != 1 {
		t.Fatalf("got %d log records, want 1", len(records))
	}
	if records[0]["request_id"] != header {
		t.Errorf("request_id = %v, want the header value %q", records[0]["request_id"], header)
	}
}

func TestLoggingSkipsPingBelowDebug(t *testing.T) {
	t.Parallel()

	var infoBuf bytes.Buffer
	doRequest(&infoBuf, slog.LevelInfo, httptest.NewRequest(http.MethodGet, pingPath, nil))
	if records := logRecords(t, &infoBuf); len(records) != 0 {
		t.Errorf("got %d log records for ping at info level, want 0: %s", len(records), infoBuf.String())
	}

	var debugBuf bytes.Buffer
	doRequest(&debugBuf, slog.LevelDebug, httptest.NewRequest(http.MethodGet, pingPath, nil))

	records := logRecords(t, &debugBuf)
	if len(records) != 1 {
		t.Fatalf("got %d log records for ping at debug level, want 1", len(records))
	}
	if records[0]["level"] != "DEBUG" {
		t.Errorf("level = %v, want DEBUG", records[0]["level"])
	}
}

func TestLoggingLevelFollowsStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		method  string
		target  string
		status  int
		wantLvl string
	}{
		{"unknown route", http.MethodGet, "/api/nope", http.StatusNotFound, "WARN"},
		{"wrong method", http.MethodPost, pingPath, http.StatusMethodNotAllowed, "WARN"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			doRequest(&buf, slog.LevelInfo, httptest.NewRequest(testCase.method, testCase.target, nil))

			records := logRecords(t, &buf)
			if len(records) != 1 {
				t.Fatalf("got %d log records, want 1", len(records))
			}
			if records[0]["level"] != testCase.wantLvl {
				t.Errorf("level = %v, want %s", records[0]["level"], testCase.wantLvl)
			}
			if records[0]["status"] != float64(testCase.status) {
				t.Errorf("status = %v, want %d", records[0]["status"], testCase.status)
			}
		})
	}
}

func TestRequestIDFromContext(t *testing.T) {
	t.Parallel()

	const clientID = "trace-in-context"

	var got string
	handler := withLogging(testLogger(&bytes.Buffer{}, slog.LevelInfo),
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = RequestID(r.Context())
		}))

	request := httptest.NewRequest(http.MethodGet, versionPath, nil)
	request.Header.Set(requestIDHeader, clientID)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if got != clientID {
		t.Errorf("RequestID(ctx) = %q, want %q", got, clientID)
	}
}
