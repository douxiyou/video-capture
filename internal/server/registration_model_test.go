//go:build cgo

package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"douxiyou.com/video-capture/internal/face"
)

func TestRegistrationWithONNX(t *testing.T) {
	library := os.Getenv("TEST_ONNX_LIBRARY")
	if library == "" {
		t.Skip("set TEST_ONNX_LIBRARY to test the ONNX model")
	}
	model, err := face.Open(library, filepath.Join("..", "..", "mobile_facenet-onnx-float", "mobile_facenet.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := model.Close(); err != nil {
			t.Error(err)
		}
	}()
	service, handler := testRegistration(t)
	service.model = model
	img := testImage(t, 0)
	for i, status := range []int{http.StatusCreated, http.StatusConflict} {
		token := guestToken(t, service, handler)
		name := []string{"one", "two"}[i]
		response := requestTest(t, handler, http.MethodPost, "/api/register", token, map[string]string{"name": name, "image": img})
		if response.Code != status {
			t.Fatalf("request %d: status %d, body %s", i, response.Code, response.Body.String())
		}
	}
}
