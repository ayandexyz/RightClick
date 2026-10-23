package bgremove

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"sync"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// isnetMean and isnetStd are the normalization constants isnet-general-use
// was trained with (mirroring rembg's DisSession.predict).
const isnetMean, isnetStd = 0.5, 1.0

// Remove runs background removal on img and returns an image the same size
// as img, with the background's alpha set to zero. The foreground's original
// colors are preserved; only the alpha channel changes.
func (s *Session) Remove(img image.Image) (*image.NRGBA, error) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("image has no pixels")
	}

	resized := image.NewRGBA(image.Rect(0, 0, inputSize, inputSize))
	xdraw.CatmullRom.Scale(resized, resized.Bounds(), img, bounds, xdraw.Over, nil)

	fillInputTensor(s.input.GetData(), resized)

	if err := s.ort.Run(); err != nil {
		return nil, fmt.Errorf("background removal inference: %w", err)
	}

	mask := maskFromOutput(s.output.GetData())
	fullMask := image.NewGray(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(fullMask, fullMask.Bounds(), mask, mask.Bounds(), xdraw.Over, nil)

	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, out.Bounds(), img, bounds.Min, draw.Src)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := out.PixOffset(x, y)
			out.Pix[i+3] = fullMask.GrayAt(x, y).Y
		}
	}
	return out, nil
}

// fillInputTensor writes img's pixels into dst as a normalized, channel-first
// [1,3,inputSize,inputSize] tensor. img must already be inputSize x inputSize.
func fillInputTensor(dst []float32, img *image.RGBA) {
	const plane = inputSize * inputSize
	maxVal := 1e-6
	// Pass 1: rembg normalizes by the image's own brightest channel value
	// rather than a fixed 255, so this must match it exactly.
	for y := 0; y < inputSize; y++ {
		rowStart := y * img.Stride
		for x := 0; x < inputSize; x++ {
			i := rowStart + x*4
			for c := 0; c < 3; c++ {
				if v := float64(img.Pix[i+c]); v > maxVal {
					maxVal = v
				}
			}
		}
	}
	for y := 0; y < inputSize; y++ {
		rowStart := y * img.Stride
		for x := 0; x < inputSize; x++ {
			i := rowStart + x*4
			for c := 0; c < 3; c++ {
				v := float64(img.Pix[i+c]) / maxVal
				dst[c*plane+y*inputSize+x] = float32((v - isnetMean) / isnetStd)
			}
		}
	}
}

// maskFromOutput turns the model's raw [1,1,inputSize,inputSize] output into
// an 8-bit grayscale mask, min-max normalized the same way rembg does.
func maskFromOutput(data []float32) *image.Gray {
	min32, max32 := data[0], data[0]
	for _, v := range data {
		if v < min32 {
			min32 = v
		}
		if v > max32 {
			max32 = v
		}
	}
	spread := float64(max32 - min32)
	if spread == 0 {
		spread = 1e-6
	}
	mask := image.NewGray(image.Rect(0, 0, inputSize, inputSize))
	for y := 0; y < inputSize; y++ {
		for x := 0; x < inputSize; x++ {
			v := (float64(data[y*inputSize+x]) - float64(min32)) / spread
			if v < 0 {
				v = 0
			} else if v > 1 {
				v = 1
			}
			mask.SetGray(x, y, grayFrom(v))
		}
	}
	return mask
}

func grayFrom(v float64) color.Gray {
	return color.Gray{Y: uint8(v*255 + 0.5)}
}

// RemoveFile decodes src, removes its background, and writes the result to
// dst as a PNG (the only common format that keeps the transparent alpha
// channel). modelPath should come from EnsureModel.
func RemoveFile(ctx context.Context, modelPath, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	img, _, err := image.Decode(in)
	closeErr := in.Close()
	if err != nil {
		return fmt.Errorf("decode %s: %w", src, err)
	}
	if closeErr != nil {
		return closeErr
	}

	session, err := NewSession(modelPath)
	if err != nil {
		return err
	}
	defer session.Close()

	if err := ctx.Err(); err != nil {
		return err
	}
	out, err := session.Remove(img)
	if err != nil {
		return err
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := png.Encode(f, out); err != nil {
		f.Close()
		return fmt.Errorf("encode %s: %w", dst, err)
	}
	return f.Close()
}

// Remover adapts a lazily-created Session to the jobs.BackgroundRemover
// interface. The model is downloaded and the session loaded on first use, not
// at construction, so a daemon can start instantly regardless of whether the
// model is already cached. Calls are serialized: a Session's tensors are
// reused across runs and are not safe for concurrent use, and onnxruntime
// inference is CPU-heavy enough that serializing it is no real loss.
type Remover struct {
	mu      sync.Mutex
	session *Session
}

// NewRemover returns a Remover with no session yet loaded.
func NewRemover() *Remover { return &Remover{} }

// Available reports whether background removal can run: specifically,
// whether onnxruntime's shared library was found. It does not load the model
// or the session, so it is cheap enough to call for a status check.
func (r *Remover) Available() (string, error) {
	path, err := resolveLibraryPath()
	if err != nil {
		return "", err
	}
	return path, nil
}

// RemoveFile decodes src, removes its background, and writes dst as a PNG.
// The underlying model and session are loaded on the first call and reused by
// every call after.
func (r *Remover) RemoveFile(ctx context.Context, src, dst string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session == nil {
		modelPath, err := EnsureModel(ctx)
		if err != nil {
			return err
		}
		session, err := NewSession(modelPath)
		if err != nil {
			return err
		}
		r.session = session
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	img, _, err := image.Decode(in)
	closeErr := in.Close()
	if err != nil {
		return fmt.Errorf("decode %s: %w", src, err)
	}
	if closeErr != nil {
		return closeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	out, err := r.session.Remove(img)
	if err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := png.Encode(f, out); err != nil {
		f.Close()
		return fmt.Errorf("encode %s: %w", dst, err)
	}
	return f.Close()
}
