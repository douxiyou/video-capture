//go:build cgo

package face

import (
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestModelIntegration(t *testing.T) {
	library := os.Getenv("TEST_ONNX_LIBRARY")
	if library == "" {
		t.Skip("set TEST_ONNX_LIBRARY to test the ONNX model")
	}
	model, err := Open(library, filepath.Join("..", "..", "mobile_facenet-onnx-float", "mobile_facenet.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := model.Close(); err != nil {
			t.Error(err)
		}
	}()
	img := image.NewRGBA(image.Rect(0, 0, 112, 112))
	for y := 0; y < 112; y++ {
		for x := 0; x < 112; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 2), G: uint8(y * 2), B: uint8(x + y), A: 255})
		}
	}
	vector, err := model.Embed(img)
	if err != nil {
		t.Fatal(err)
	}
	if len(vector) != Dimension {
		t.Fatalf("dimension = %d", len(vector))
	}
	var sum float64
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatal("non-finite output")
		}
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		t.Fatal("zero output")
	}
}
