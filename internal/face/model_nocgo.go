//go:build !cgo

package face

import (
	"errors"
	"image"
)

const Dimension = 128
const ModelID = "mobile_facenet_onnx_float_v1"

type Model struct{}

func Open(_, _ string) (*Model, error) {
	return nil, errors.New("face registration requires a CGO-enabled build and ONNX Runtime")
}

func (m *Model) Embed(image.Image) ([]float32, error) {
	return nil, errors.New("face registration requires CGO")
}

func (m *Model) Close() error { return nil }
