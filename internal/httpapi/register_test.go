package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeMailSender records outgoing mail instead of talking to a real SMTP
// server, so tests can pull the verification token straight out of the
// "sent" body — the only place a raw token ever appears outside the DB,
// which stores only its hash.
type fakeMailSender struct {
	mu   sync.Mutex
	sent []fakeMail
}

type fakeMail struct {
	to, subject, body string
}

func (f *fakeMailSender) Send(to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, fakeMail{to: to, subject: subject, body: body})

	return nil
}

func (f *fakeMailSender) last() (fakeMail, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return fakeMail{}, false
	}

	return f.sent[len(f.sent)-1], true
}

func (f *fakeMailSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.sent)
}

func tokenFromBody(t *testing.T, body string) string {
	t.Helper()

	_, after, ok := strings.Cut(body, "token=")
	if !ok {
		t.Fatalf("no token= found in mail body: %s", body)
	}
	token, _, _ := strings.Cut(after, "\n")

	return strings.TrimSpace(token)
}

func newRegisterHandler(t *testing.T) (http.Handler, *fakeMailSender) {
	t.Helper()

	deps := testDeps(t)
	sender := &fakeMailSender{}
	deps.MailSender = sender
	deps.PublicBaseURL = "http://localhost:3000"

	return NewHandler(discardLogger(), testVersion, deps), sender
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func TestRegisterEndpointCreatesUnverifiedAccountAndBlocksLogin(t *testing.T) {
	t.Parallel()

	handler, sender := newRegisterHandler(t)

	register := doJSON(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"agencyName": "Новое агентство", "fullName": "Владелец Владелецович",
		"email": "owner@example.test", "password": "SuperSecret1234!",
	})
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body = %s", register.Code, register.Body)
	}
	if sender.count() != 1 {
		t.Fatalf("emails sent = %d, want 1", sender.count())
	}

	mail, _ := sender.last()
	if mail.to != "owner@example.test" {
		t.Errorf("mail.to = %q, want owner@example.test", mail.to)
	}

	login := doJSON(t, handler, http.MethodPost, "/api/auth/login", map[string]string{
		"email": "owner@example.test", "password": "SuperSecret1234!",
	})
	if login.Code != http.StatusForbidden {
		t.Fatalf("login status = %d, want 403; body = %s", login.Code, login.Body)
	}
	if !strings.Contains(login.Body.String(), codeEmailNotVerified) {
		t.Errorf("login body = %s, want code %q", login.Body, codeEmailNotVerified)
	}
}

func TestRegisterEndpointValidatesFields(t *testing.T) {
	t.Parallel()

	handler, _ := newRegisterHandler(t)

	response := doJSON(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"agencyName": "", "fullName": "", "email": "not-an-email", "password": "short",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body)
	}

	var body struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, field := range []string{"agencyName", "fullName", "email", "password"} {
		if _, ok := body.Error.Fields[field]; !ok {
			t.Errorf("fields = %+v, want %q present", body.Error.Fields, field)
		}
	}
}

func TestRegisterEndpointWithoutMailSenderReturns503(t *testing.T) {
	t.Parallel()

	deps := testDeps(t)
	handler := NewHandler(discardLogger(), testVersion, deps)

	response := doJSON(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"agencyName": "Агентство без почты", "fullName": "Владелец",
		"email": "nomail@example.test", "password": "SuperSecret1234!",
	})
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", response.Code, response.Body)
	}
}

func TestRegisterEndpointConflictOnDuplicateEmail(t *testing.T) {
	t.Parallel()

	handler, _ := newRegisterHandler(t)
	body := map[string]string{
		"agencyName": "Агентство дублей", "fullName": "Владелец",
		"email": "dup@example.test", "password": "SuperSecret1234!",
	}

	first := doJSON(t, handler, http.MethodPost, "/api/auth/register", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first register status = %d, want 201; body = %s", first.Code, first.Body)
	}

	second := doJSON(t, handler, http.MethodPost, "/api/auth/register", body)
	if second.Code != http.StatusConflict {
		t.Fatalf("second register status = %d, want 409; body = %s", second.Code, second.Body)
	}
}

func TestVerifyEmailEndpointActivatesAccountAndLogsIn(t *testing.T) {
	t.Parallel()

	handler, sender := newRegisterHandler(t)

	register := doJSON(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"agencyName": "Агентство подтверждения", "fullName": "Владелец",
		"email": "verify@example.test", "password": "SuperSecret1234!",
	})
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body = %s", register.Code, register.Body)
	}

	mail, ok := sender.last()
	if !ok {
		t.Fatal("no mail sent")
	}
	token := tokenFromBody(t, mail.body)

	verify := doJSON(t, handler, http.MethodPost, "/api/auth/verify-email", map[string]string{"token": token})
	if verify.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200; body = %s", verify.Code, verify.Body)
	}
	if len(verify.Result().Cookies()) == 0 {
		t.Fatal("verify response set no cookies")
	}

	// Тот же токен второй раз использовать нельзя.
	reuse := doJSON(t, handler, http.MethodPost, "/api/auth/verify-email", map[string]string{"token": token})
	if reuse.Code != http.StatusBadRequest {
		t.Fatalf("reused token status = %d, want 400; body = %s", reuse.Code, reuse.Body)
	}

	// Теперь обычный вход по паролю тоже должен работать.
	login := doJSON(t, handler, http.MethodPost, "/api/auth/login", map[string]string{
		"email": "verify@example.test", "password": "SuperSecret1234!",
	})
	if login.Code != http.StatusOK {
		t.Fatalf("login after verification status = %d, want 200; body = %s", login.Code, login.Body)
	}
}

func TestVerifyEmailEndpointRejectsUnknownToken(t *testing.T) {
	t.Parallel()

	handler, _ := newRegisterHandler(t)

	response := doJSON(t, handler, http.MethodPost, "/api/auth/verify-email", map[string]string{"token": "garbage"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", response.Code, response.Body)
	}
}

func TestResendVerificationEndpointAlwaysRespondsGenerically(t *testing.T) {
	t.Parallel()

	handler, sender := newRegisterHandler(t)

	unknown := doJSON(t, handler, http.MethodPost, "/api/auth/resend-verification",
		map[string]string{"email": "unknown@example.test"})
	if unknown.Code != http.StatusOK {
		t.Fatalf("unknown email status = %d, want 200; body = %s", unknown.Code, unknown.Body)
	}
	if sender.count() != 0 {
		t.Errorf("emails sent for unknown address = %d, want 0", sender.count())
	}

	register := doJSON(t, handler, http.MethodPost, "/api/auth/register", map[string]string{
		"agencyName": "Агентство переотправки", "fullName": "Владелец",
		"email": "resend@example.test", "password": "SuperSecret1234!",
	})
	if register.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body = %s", register.Code, register.Body)
	}

	resend := doJSON(t, handler, http.MethodPost, "/api/auth/resend-verification",
		map[string]string{"email": "resend@example.test"})
	if resend.Code != http.StatusOK {
		t.Fatalf("resend status = %d, want 200; body = %s", resend.Code, resend.Body)
	}
	if sender.count() != 2 {
		t.Fatalf("emails sent = %d, want 2 (register + resend)", sender.count())
	}
}
