package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockUploader struct {
	err         error
	contentType string
	key         string
	data        []byte
	hook        func()
}

func (u *mockUploader) Put(_ context.Context, key, contentType string, data []byte) error {
	u.key, u.contentType, u.data = key, contentType, data
	if u.hook != nil {
		u.hook()
	}
	return u.err
}

func uploadUser(t *testing.T, s *registrationService) string {
	t.Helper()
	vector := make([]float32, 128)
	vector[0] = 1
	id, err := s.saveUser(context.Background(), "upload-user", "upload-credential", vector)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.issueUserToken(rec, http.StatusOK, id, "upload-user", "upload-credential")
	var response struct {
		Token string `json:"user_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Token
}

func multipartImage(t *testing.T, filename string, data []byte, expression string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("expression", expression); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}

func uploadRequest(handler http.Handler, token string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/images", body)
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestUploadImageSuccessAndAuthorization(t *testing.T) {
	service, _ := testRegistration(t)
	mock := &mockUploader{}
	handler := newRouter(service, mock)
	token := uploadUser(t, service)
	image, err := base64.StdEncoding.DecodeString(testImage(t, 0))
	if err != nil {
		t.Fatal(err)
	}
	body, ct := multipartImage(t, "face.png", image, "微笑")
	rec := uploadRequest(handler, token, body, ct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(mock.key, "images/1/") || !strings.HasSuffix(mock.key, ".png") || mock.contentType != "image/png" || !bytes.Equal(mock.data, image) {
		t.Fatalf("unexpected OSS upload: %s %s", mock.key, mock.contentType)
	}
	var record struct {
		Path       string `db:"file_path"`
		Status     string `db:"status"`
		Name       string `db:"original_filename"`
		Expression string `db:"expression"`
		Size       int    `db:"size_bytes"`
	}
	if err := service.db.Get(&record, "SELECT file_path, status, original_filename, expression, size_bytes FROM image_uploads"); err != nil {
		t.Fatal(err)
	}
	if record.Path != mock.key || record.Status != "uploaded" || record.Name != "face.png" || record.Expression != "微笑" || record.Size != len(image) {
		t.Fatalf("unexpected record: %+v", record)
	}
	guest := guestToken(t, service, handler)
	for _, invalid := range []string{"", guest, "bad"} {
		body, ct = multipartImage(t, "face.png", image, "")
		if rec := uploadRequest(handler, invalid, body, ct); rec.Code != http.StatusUnauthorized {
			t.Fatalf("unauthorized upload status %d", rec.Code)
		}
	}
	if _, err := service.db.Exec("DELETE FROM image_uploads"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec("DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	body, ct = multipartImage(t, "face.png", image, "")
	if rec := uploadRequest(handler, token, body, ct); rec.Code != http.StatusUnauthorized {
		t.Fatalf("deleted user status: %d", rec.Code)
	}
}

func TestUploadFailuresRemainTraceable(t *testing.T) {
	for _, finalizationFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "oss", true: "database"}[finalizationFailure], func(t *testing.T) {
			service, _ := testRegistration(t)
			mock := &mockUploader{err: errors.New("network error")}
			if finalizationFailure {
				mock.err = nil
				mock.hook = func() {
					if _, err := service.db.Exec("CREATE TRIGGER reject_update BEFORE UPDATE ON image_uploads BEGIN SELECT RAISE(FAIL, 'test'); END"); err != nil {
						t.Error(err)
					}
				}
			}
			handler := newRouter(service, mock)
			token := uploadUser(t, service)
			image, _ := base64.StdEncoding.DecodeString(testImage(t, 0))
			body, ct := multipartImage(t, "face.png", image, "")
			rec := uploadRequest(handler, token, body, ct)
			want := http.StatusBadGateway
			if finalizationFailure {
				want = http.StatusServiceUnavailable
			}
			if rec.Code != want {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			var status, path string
			if err := service.db.QueryRow("SELECT status, file_path FROM image_uploads").Scan(&status, &path); err != nil {
				t.Fatal(err)
			}
			if status != "pending" || path != mock.key {
				t.Fatalf("status %q path %q", status, path)
			}
		})
	}
}

func TestUploadValidation(t *testing.T) {
	service, _ := testRegistration(t)
	mock := &mockUploader{}
	handler := newRouter(service, mock)
	token := uploadUser(t, service)
	image, _ := base64.StdEncoding.DecodeString(testImage(t, 0))
	tests := []struct {
		filename   string
		data       []byte
		expression string
	}{
		{"text.png", []byte("not an image"), ""},
		{"truncated.png", image[:len(image)/2], ""},
		{"large.png", bytes.Repeat([]byte{1}, maxImageBytes+1), ""},
		{"image.png", image, strings.Repeat("x", 31)},
	}

	for _, tt := range tests {
		body, ct := multipartImage(t, tt.filename, tt.data, tt.expression)
		if rec := uploadRequest(handler, token, body, ct); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid input status %d: %s", rec.Code, rec.Body.String())
		}
	}
	if mock.key != "" {
		t.Fatal("invalid input reached OSS")
	}

	var count int
	if err := service.db.Get(&count, "SELECT COUNT(*) FROM image_uploads"); err != nil || count != 0 {
		t.Fatalf("invalid upload saved: %d, %v", count, err)
	}
}

func TestUploadRouteRequiresConfigurationAndReservation(t *testing.T) {
	service, handler := testRegistration(t)
	if rec := uploadRequest(handler, "", nil, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled route status: %d", rec.Code)
	}

	mock := &mockUploader{}
	handler = newRouter(service, mock)
	token := uploadUser(t, service)
	if _, err := service.db.Exec("CREATE TRIGGER reject_insert BEFORE INSERT ON image_uploads BEGIN SELECT RAISE(FAIL, 'test'); END"); err != nil {
		t.Fatal(err)
	}
	image, _ := base64.StdEncoding.DecodeString(testImage(t, 0))
	body, ct := multipartImage(t, "face.png", image, "")
	if rec := uploadRequest(handler, token, body, ct); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("reservation failure status: %d", rec.Code)
	}
	if mock.key != "" {
		t.Fatal("OSS upload occurred without a database record")
	}
}

func TestUploadSizeLimit(t *testing.T) {
	service, _ := testRegistration(t)
	mock := &mockUploader{}
	handler := newRouter(service, mock)
	token := uploadUser(t, service)
	image, _ := base64.StdEncoding.DecodeString(testImage(t, 0))
	padded := make([]byte, maxImageBytes)
	copy(padded, image)
	body, ct := multipartImage(t, "boundary.png", padded, "")
	if rec := uploadRequest(handler, token, body, ct); rec.Code != http.StatusCreated {
		t.Fatalf("5 MiB image rejected: %d %s", rec.Code, rec.Body.String())
	}
	body, ct = multipartImage(t, "too-large.png", append(padded, 0), "")
	if rec := uploadRequest(handler, token, body, ct); rec.Code != http.StatusBadRequest {
		t.Fatalf("image over 5 MiB accepted: %d", rec.Code)
	}
}
