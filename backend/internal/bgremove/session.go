package bgremove

import (
	"fmt"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// inputSize is the fixed square resolution isnet-general-use was trained on.
// Images are resized to this before inference and the output mask is resized
// back to the original size.
const inputSize = 1024

var (
	runtimeOnce sync.Once
	runtimeErr  error
)

// candidateLibraryPaths are checked, in order, when ONNXRUNTIME_LIB_PATH is
// not set. They cover Arch's onnxruntime-cpu package and the most common
// distro layouts.
var candidateLibraryPaths = []string{
	"/usr/lib/libonnxruntime.so",
	"/usr/lib64/libonnxruntime.so",
	"/usr/local/lib/libonnxruntime.so",
	"/usr/lib/x86_64-linux-gnu/libonnxruntime.so",
}

func resolveLibraryPath() (string, error) {
	if path := os.Getenv("ONNXRUNTIME_LIB_PATH"); path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("ONNXRUNTIME_LIB_PATH is set to %s but it does not exist: %w", path, err)
		}
		return path, nil
	}
	for _, path := range candidateLibraryPaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("libonnxruntime.so not found; install onnxruntime-cpu or set ONNXRUNTIME_LIB_PATH")
}

// initRuntime initializes the onnxruntime environment once per process. It is
// never torn down: the daemon that uses this package runs for the process
// lifetime, and onnxruntime has no notion of per-session re-init.
func initRuntime() error {
	runtimeOnce.Do(func() {
		if ort.IsInitialized() {
			return
		}
		path, err := resolveLibraryPath()
		if err != nil {
			runtimeErr = err
			return
		}
		ort.SetSharedLibraryPath(path)
		runtimeErr = ort.InitializeEnvironment()
	})
	return runtimeErr
}

// Session holds a loaded model and the fixed-shape tensors inference reuses.
// It is not safe for concurrent use; callers running several removals at once
// should use one Session per goroutine or guard it with a mutex.
type Session struct {
	ort    *ort.AdvancedSession
	input  *ort.Tensor[float32]
	output *ort.Tensor[float32]
}

// NewSession loads the isnet-general-use model at modelPath. Use EnsureModel
// to obtain modelPath, downloading it on first run.
func NewSession(modelPath string) (*Session, error) {
	if err := initRuntime(); err != nil {
		return nil, err
	}
	inputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, inputSize, inputSize))
	if err != nil {
		return nil, fmt.Errorf("allocate input tensor: %w", err)
	}
	outputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 1, inputSize, inputSize))
	if err != nil {
		inputTensor.Destroy()
		return nil, fmt.Errorf("allocate output tensor: %w", err)
	}
	session, err := ort.NewAdvancedSession(modelPath,
		[]string{"input_image"}, []string{"output_image"},
		[]ort.Value{inputTensor}, []ort.Value{outputTensor}, nil)
	if err != nil {
		inputTensor.Destroy()
		outputTensor.Destroy()
		return nil, fmt.Errorf("load background removal model: %w", err)
	}
	return &Session{ort: session, input: inputTensor, output: outputTensor}, nil
}

// Close releases the session and its tensors.
func (s *Session) Close() {
	s.ort.Destroy()
	s.input.Destroy()
	s.output.Destroy()
}
