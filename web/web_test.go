package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eriicafes/httpx/httperrors"
	"github.com/eriicafes/tmpl/vite"

	"github.com/eriicafes/loveloop/lib/telegram"
	"github.com/eriicafes/loveloop/web/templates"
)

// testApp returns an App whose bot calls a fake Telegram, and the webhook
// the fake was given.
func testApp(t *testing.T) (*App, *string) {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "test")
	t.Setenv("VERCEL_PROJECT_PRODUCTION_URL", "loveloop.example.com")

	webhook := new(string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var params map[string]any
		_ = json.NewDecoder(r.Body).Decode(&params)
		var result any = true
		switch r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:] {
		case "getMe":
			result = map[string]any{"id": 1, "is_bot": true, "first_name": "LoveLoop", "username": "loveloop_bot"}
		case "getWebhookInfo":
			result = map[string]any{"url": *webhook, "pending_update_count": 2, "last_error_date": 1793450000, "last_error_message": "Wrong response from the webhook: 500"}
		case "setWebhook":
			*webhook = params["url"].(string)
		case "deleteWebhook":
			*webhook = ""
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(server.Close)
	bot := telegram.New("test")
	bot.BaseURL = server.URL

	// A production Vite over an in-memory build, so tests need no dev server.
	v, err := vite.New(vite.Config{Output: fstest.MapFS{
		".vite/manifest.json":    {Data: []byte(`{"web/src/main.css":{"file":"assets/main-abc123.css","src":"web/src/main.css","isEntry":true}}`)},
		"assets/main-abc123.css": {Data: []byte("body{}")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := templates.Parse(v)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{Bot: bot, Vite: v, Templates: parsed, Password: "hunter2", WebhookSecret: "secret"}
	if app.Session, app.Flash, err = cookies(app.Password, false); err != nil {
		t.Fatal(err)
	}
	return app, webhook
}

// browser keeps cookies across requests, like a browser on the same origin.
type browser struct {
	t       *testing.T
	handler http.Handler
	cookies map[string]*http.Cookie
}

func newBrowser(t *testing.T, handler http.Handler) *browser {
	return &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
}

func (b *browser) do(method, path string, form url.Values) *httptest.ResponseRecorder {
	b.t.Helper()
	req := httptest.NewRequest(method, "https://loveloop.example.com"+path, strings.NewReader(form.Encode()))
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for _, cookie := range b.cookies {
		req.AddCookie(cookie)
	}
	res := httptest.NewRecorder()
	b.handler.ServeHTTP(res, req)
	for _, cookie := range res.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(b.cookies, cookie.Name)
		} else {
			b.cookies[cookie.Name] = cookie
		}
	}
	return res
}

func wantRedirect(t *testing.T, res *httptest.ResponseRecorder, to string) {
	t.Helper()
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != to {
		t.Fatalf("got %d to %q, want a redirect to %s", res.Code, res.Header().Get("Location"), to)
	}
}

func TestLogin(t *testing.T) {
	app, _ := testApp(t)
	b := newBrowser(t, app.Handler())

	wantRedirect(t, b.do("GET", "/", nil), "/login")
	if res := b.do("GET", "/login", nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `name="password"`) {
		t.Fatalf("login page: %d", res.Code)
	}

	res := b.do("POST", "/login", url.Values{"password": {"wrong"}})
	if res.Code != http.StatusUnauthorized || !strings.Contains(res.Body.String(), "isn&#39;t right") {
		t.Fatalf("wrong password: %d", res.Code)
	}
	wantRedirect(t, b.do("GET", "/", nil), "/login")

	wantRedirect(t, b.do("POST", "/login", url.Values{"password": {"hunter2"}}), "/")
	res = b.do("GET", "/", nil)
	body := res.Body.String()
	if res.Code != http.StatusOK {
		t.Fatalf("setup page: %d %s", res.Code, body)
	}
	for _, want := range []string{"@loveloop_bot", "Not connected", "https://loveloop.example.com", "Wrong response from the webhook", "/assets/main-abc123.css"} {
		if !strings.Contains(body, want) {
			t.Errorf("setup page lacks %q", want)
		}
	}
	if path := os.Getenv("LOVELOOP_DUMP_PAGES"); path != "" {
		os.WriteFile(path+"/index.html", res.Body.Bytes(), 0o644)
		os.WriteFile(path+"/login.html", newBrowser(t, app.Handler()).do("POST", "/login", url.Values{"password": {"wrong"}}).Body.Bytes(), 0o644)
	}

	wantRedirect(t, b.do("POST", "/logout", url.Values{}), "/login")
	wantRedirect(t, b.do("GET", "/", nil), "/login")
}

func TestLoginWithoutPassword(t *testing.T) {
	app, _ := testApp(t)
	app.Password = ""
	b := newBrowser(t, app.Handler())
	if res := b.do("GET", "/login", nil); !strings.Contains(res.Body.String(), "SETUP_PASSWORD") {
		t.Fatal("login page does not ask for SETUP_PASSWORD")
	}
	if res := b.do("POST", "/login", url.Values{"password": {""}}); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty password signed in: %d", res.Code)
	}
}

func TestConnect(t *testing.T) {
	app, webhook := testApp(t)
	b := newBrowser(t, app.Handler())
	wantRedirect(t, b.do("POST", "/login", url.Values{"password": {"hunter2"}}), "/")

	req := httptest.NewRequest("POST", "https://loveloop.example.com/setup/connect", strings.NewReader("public_url=https://loveloop.example.com"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	for _, cookie := range b.cookies {
		req.AddCookie(cookie)
	}
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || *webhook != "" {
		t.Fatalf("cross-site post: %d, webhook %q", res.Code, *webhook)
	}

	wantRedirect(t, b.do("POST", "/setup/connect", url.Values{"public_url": {"http://loveloop.example.com"}}), "/")
	if body := b.do("GET", "/", nil).Body.String(); !strings.Contains(body, "only delivers to https") || *webhook != "" {
		t.Fatalf("http URL: webhook %q, page lacks the error", *webhook)
	}

	wantRedirect(t, b.do("POST", "/setup/connect", url.Values{"public_url": {"https://loveloop.example.com/"}}), "/")
	body := b.do("GET", "/", nil).Body.String()
	if !strings.Contains(body, "Connected!") || !strings.Contains(body, "● Connected") {
		t.Fatal("connect: page lacks the success message or status")
	}
	if *webhook != "https://loveloop.example.com/telegram" {
		t.Fatalf("webhook = %q", *webhook)
	}
	if body := b.do("GET", "/", nil).Body.String(); strings.Contains(body, "Connected!") {
		t.Fatal("flash message showed twice")
	}

	wantRedirect(t, b.do("POST", "/setup/disconnect", url.Values{}), "/")
	if *webhook != "" {
		t.Fatalf("webhook after disconnect = %q", *webhook)
	}
}

func TestServesBuiltAssets(t *testing.T) {
	app, _ := testApp(t)
	res := newBrowser(t, app.Handler()).do("GET", "/assets/main-abc123.css", nil)
	if res.Code != http.StatusOK || res.Body.String() != "body{}" {
		t.Fatalf("asset: %d %q", res.Code, res.Body.String())
	}
}

func TestWebhookRejectsWrongSecret(t *testing.T) {
	app, _ := testApp(t)
	req := httptest.NewRequest("POST", TelegramWebhookPath, strings.NewReader(`{"update_id":1}`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong")
	res := httptest.NewRecorder()
	app.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", res.Code)
	}
}

func TestPageErrorRendersErrorPage(t *testing.T) {
	app, _ := testApp(t)
	res := httptest.NewRecorder()
	app.handlePageError(res, httptest.NewRequest("GET", "/", nil), httperrors.New("no such thing", http.StatusNotFound))
	if body := res.Body.String(); res.Code != http.StatusNotFound || !strings.Contains(body, "Not Found") || !strings.Contains(body, "no such thing") {
		t.Fatalf("got %d %q", res.Code, body)
	}

	res = httptest.NewRecorder()
	app.handlePageError(res, httptest.NewRequest("GET", "/", nil), errors.New("redis: secret detail"))
	if body := res.Body.String(); res.Code != http.StatusInternalServerError || strings.Contains(body, "secret detail") || !strings.Contains(body, "Something went wrong") {
		t.Fatalf("got %d %q", res.Code, body)
	}
}
