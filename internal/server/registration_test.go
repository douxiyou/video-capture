package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"douxiyou.com/video-capture/internal/face"
	"douxiyou.com/video-capture/internal/storage"
)

type testModel struct{}

type failingModel struct{}

func (failingModel) Embed(image.Image) ([]float32, error) {
	return nil, errors.New("model unavailable")
}

func (testModel) Embed(img image.Image) ([]float32, error) {
	vector := make([]float32, face.Dimension)
	r, _, _, _ := img.At(0, 0).RGBA()
	if r > 0 {
		vector[1] = 1
	} else {
		vector[0] = 1
	}
	return vector, nil
}

func testRegistration(t *testing.T) (*registrationService, http.Handler) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := storage.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	service, err := newRegistrationService(db, testModel{}, bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), 0.6)
	if err != nil {
		t.Fatal(err)
	}
	return service, newRouter(service)
}

func requestTest(t *testing.T, handler http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.RemoteAddr = "127.0.0.1:12345"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func guestToken(t *testing.T, service *registrationService, handler http.Handler) string {
	t.Helper()
	response := requestTest(t, handler, http.MethodGet, "/api/captcha", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("captcha: %s", response.Body.String())
	}
	var captcha struct {
		ID    string `json:"captcha_id"`
		Image string `json:"image"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &captcha); err != nil {
		t.Fatal(err)
	}
	if captcha.ID == "" || captcha.Image == "" {
		t.Fatal("missing captcha")
	}
	service.mu.Lock()
	answer := service.challenges[captcha.ID].answer
	service.mu.Unlock()
	response = requestTest(t, handler, http.MethodPost, "/api/guest", "", map[string]string{"captcha_id": captcha.ID, "answer": answer})
	if response.Code != http.StatusOK {
		t.Fatalf("guest: %s", response.Body.String())
	}
	var guest struct {
		Token string `json:"guest_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &guest); err != nil {
		t.Fatal(err)
	}
	if guest.Token == "" {
		t.Fatal("missing guest token")
	}
	response = requestTest(t, handler, http.MethodPost, "/api/guest", "", map[string]string{"captcha_id": captcha.ID, "answer": answer})
	if response.Code != http.StatusUnauthorized {
		t.Fatal("captcha reused")
	}
	return guest.Token
}

func testImage(t *testing.T, red uint8) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 112, 112))
	img.Set(0, 0, color.RGBA{R: red, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestRegistrationLifecycle(t *testing.T) {
	service, handler := testRegistration(t)
	image := testImage(t, 0)
	token := guestToken(t, service, handler)
	response := requestTest(t, handler, http.MethodDelete, "/api/user", token, nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("guest authorized as user: %d", response.Code)
	}
	response = requestTest(t, handler, http.MethodPost, "/api/register", token, map[string]string{"name": " Alice ", "image": image})
	if response.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", response.Code, response.Body.String())
	}
	var user struct {
		Token string `json:"user_token"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	if user.Token == "" || user.Name != "Alice" {
		t.Fatalf("invalid result: %+v", user)
	}
	if requestTest(t, handler, http.MethodGet, "/api/user", token, nil).Code != http.StatusUnauthorized {
		t.Fatal("guest token authorized as user")
	}
	if requestTest(t, handler, http.MethodGet, "/api/user", user.Token, nil).Code != http.StatusOK {
		t.Fatal("user token rejected")
	}
	loginGuest := guestToken(t, service, handler)
	if response := requestTest(t, handler, http.MethodPost, "/api/login", loginGuest, map[string]string{"name": "Alice", "image": image}); response.Code != http.StatusOK {
		t.Fatalf("login: %d %s", response.Code, response.Body.String())
	}
	if response := requestTest(t, handler, http.MethodPost, "/api/login", loginGuest, map[string]string{"name": "Alice", "image": image}); response.Code != http.StatusUnauthorized {
		t.Fatal("login guest reused")
	}
	wrongGuest := guestToken(t, service, handler)
	if response := requestTest(t, handler, http.MethodPost, "/api/login", wrongGuest, map[string]string{"name": "Alice", "image": testImage(t, 255)}); response.Code != http.StatusUnauthorized {
		t.Fatal("incorrect face accepted")
	}
	response = requestTest(t, handler, http.MethodPost, "/api/register", token, map[string]string{"name": "Other", "image": image})
	if response.Code != http.StatusUnauthorized {
		t.Fatal("guest token reused")
	}
	var encrypted []byte
	if err := service.db.Get(&encrypted, "SELECT encrypted_embedding FROM face_embeddings"); err != nil {
		t.Fatal(err)
	}
	if len(encrypted) != face.Dimension*4+service.aead.NonceSize()+service.aead.Overhead() {
		t.Fatal("unexpected encrypted size")
	}
	if _, err := service.decrypt(encrypted); err != nil {
		t.Fatal(err)
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := service.decrypt(encrypted); err == nil {
		t.Fatal("tampered vector accepted")
	}

	duplicate := guestToken(t, service, handler)
	response = requestTest(t, handler, http.MethodPost, "/api/register", duplicate, map[string]string{"name": "Bob", "image": image})
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d", response.Code)
	}
	response = requestTest(t, handler, http.MethodPost, "/api/register", duplicate, map[string]string{"name": "Bob", "image": testImage(t, 255)})
	if response.Code != http.StatusUnauthorized {
		t.Fatal("duplicate did not consume guest")
	}

	nameDuplicate := guestToken(t, service, handler)
	response = requestTest(t, handler, http.MethodPost, "/api/register", nameDuplicate, map[string]string{"name": "Alice", "image": testImage(t, 255)})
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate name: %d", response.Code)
	}
	response = requestTest(t, handler, http.MethodDelete, "/api/user", user.Token, nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	var count int
	if err := service.db.Get(&count, "SELECT COUNT(*) FROM face_embeddings"); err != nil || count != 0 {
		t.Fatalf("vectors remaining %d: %v", count, err)
	}
	response = requestTest(t, handler, http.MethodDelete, "/api/user", user.Token, nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("deleted user token accepted")
	}
	if requestTest(t, handler, http.MethodGet, "/api/user", user.Token, nil).Code != http.StatusUnauthorized {
		t.Fatal("deleted user token accepted for lookup")
	}
	newGuest := guestToken(t, service, handler)
	if response := requestTest(t, handler, http.MethodPost, "/api/register", newGuest, map[string]string{"name": "New", "image": image}); response.Code != http.StatusCreated {
		t.Fatalf("register after deletion: %d", response.Code)
	}
	if requestTest(t, handler, http.MethodGet, "/api/user", user.Token, nil).Code != http.StatusUnauthorized {
		t.Fatal("old user token accepted after row ID reuse")
	}
}

func TestGuestExpiryIPAndInvalidInput(t *testing.T) {
	service, handler := testRegistration(t)
	token := guestToken(t, service, handler)
	other := httptest.NewRequest(http.MethodPost, "/api/register", nil)
	other.RemoteAddr = "192.0.2.1:1234"
	other.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, other)
	if rec.Code != http.StatusUnauthorized {
		t.Fatal("other IP accepted")
	}
	if requestTest(t, handler, http.MethodPost, "/api/register", token, nil).Code != http.StatusUnauthorized {
		t.Fatal("guest not consumed")
	}
	token = guestToken(t, service, handler)
	if requestTest(t, handler, http.MethodPost, "/api/register", token, map[string]string{"name": "a", "image": "bad"}).Code != http.StatusBadRequest {
		t.Fatal("bad image accepted")
	}
	if requestTest(t, handler, http.MethodPost, "/api/register", token, nil).Code != http.StatusUnauthorized {
		t.Fatal("bad image did not consume guest")
	}
	token = guestToken(t, service, handler)
	service.model = failingModel{}
	if requestTest(t, handler, http.MethodPost, "/api/register", token, map[string]string{"name": "a", "image": testImage(t, 0)}).Code != http.StatusServiceUnavailable {
		t.Fatal("model failure accepted")
	}
	if requestTest(t, handler, http.MethodPost, "/api/register", token, nil).Code != http.StatusUnauthorized {
		t.Fatal("model failure did not consume guest")
	}
	service.mu.Lock()
	service.challenges["expired"] = challenge{"123456", "127.0.0.1", time.Now().Add(-time.Second)}
	service.mu.Unlock()
	if requestTest(t, handler, http.MethodPost, "/api/guest", "", map[string]string{"captcha_id": "expired", "answer": "123456"}).Code != http.StatusUnauthorized {
		t.Fatal("expired captcha accepted")
	}
}

func TestConcurrentGuestConsumption(t *testing.T) {
	service, handler := testRegistration(t)
	token := guestToken(t, service, handler)
	req := httptest.NewRequest(http.MethodPost, "/api/register", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	const workers = 10
	result := make(chan bool, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result <- service.consumeGuest(req, "127.0.0.1")
		}()
	}
	wg.Wait()
	close(result)
	success := 0
	for ok := range result {
		if ok {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("guest accepted %d times", success)
	}
}

func TestConcurrentDuplicateRegistration(t *testing.T) {
	service, _ := testRegistration(t)
	vector := make([]float32, face.Dimension)
	vector[0] = 1
	const workers = 4
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.saveUser(context.Background(), "same", "credential", vector)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if err != errDuplicate {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("registered %d duplicate users", success)
	}
}

func TestExistingUsersWithoutVectorsFailClosed(t *testing.T) {
	service, _ := testRegistration(t)
	if _, err := service.db.Exec("INSERT INTO users (name) VALUES ('old')"); err != nil {
		t.Fatal(err)
	}
	vector := make([]float32, face.Dimension)
	vector[0] = 1
	_, err := service.saveUser(context.Background(), "new", "credential", vector)
	if err != errUnverifiedUsers {
		t.Fatalf("expected unverified-user failure, got %v", err)
	}
}
