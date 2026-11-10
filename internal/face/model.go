//go:build cgo

package face

import (
	"errors"
	"fmt"
	"image"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const Dimension = 128
const ModelID = "mobile_facenet_onnx_float_v1"

type Model struct {
	mu      sync.Mutex
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]
	session *ort.AdvancedSession
}

func Open(library, modelPath string) (*Model, error) {
	ort.SetSharedLibraryPath(library)
	if err := ort.InitializeEnvironment(); err != nil {
		return nil, fmt.Errorf("initialize ONNX Runtime: %w", err)
	}
	input, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, 112, 112))
	if err != nil {
		ort.DestroyEnvironment()
		return nil, fmt.Errorf("create model input: %w", err)
	}
	output, err := ort.NewEmptyTensor[float32](ort.NewShape(2, Dimension))
	if err != nil {
		input.Destroy()
		ort.DestroyEnvironment()
		return nil, fmt.Errorf("create model output: %w", err)
	}
	session, err := ort.NewAdvancedSession(modelPath, []string{"img1", "img2"}, []string{"embeddings"}, []ort.Value{input, input}, []ort.Value{output}, nil)
	if err != nil {
		output.Destroy()
		input.Destroy()
		ort.DestroyEnvironment()
		return nil, fmt.Errorf("load face model: %w", err)
	}
	return &Model{input: input, output: output, session: session}, nil
}

func (m *Model) Embed(img image.Image) ([]float32, error) {
	if img.Bounds().Dx() != 112 || img.Bounds().Dy() != 112 {
		return nil, fmt.Errorf("face image must be 112x112")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.input.GetData()
	bounds := img.Bounds()
	for y := 0; y < 112; y++ {
		for x := 0; x < 112; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			pos := y*112 + x
			data[pos], data[112*112+pos], data[2*112*112+pos] = float32(r)/65535, float32(g)/65535, float32(b)/65535
		}
	}
	if err := m.session.Run(); err != nil {
		return nil, fmt.Errorf("run face model: %w", err)
	}
	return append([]float32(nil), m.output.GetData()[:Dimension]...), nil
}

func (m *Model) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return errors.Join(m.session.Destroy(), m.output.Destroy(), m.input.Destroy(), ort.DestroyEnvironment())
}
