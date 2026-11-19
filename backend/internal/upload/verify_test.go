package upload

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"os/exec"
	"testing"
)

func pngImage(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestVerifyRasterPayload(t *testing.T) {
	data := pngImage(t)
	for _, test := range []struct {
		name, contentType string
		data              []byte
		size              int64
		want              error
	}{
		{"genuine PNG", "image/png", data, int64(len(data)), nil},
		{"spoofed format", "image/jpeg", data, int64(len(data)), ErrInvalidMedia},
		{"truncated PNG", "image/png", data[:40], 40, ErrInvalidMedia},
		{"forged MIME", "image/png", []byte("<svg/>"), 6, ErrInvalidMedia},
		{"wrong size", "image/png", data, int64(len(data) - 1), ErrInvalidMedia},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifyMedia(context.Background(), "", "avatar", test.contentType, test.size, bytes.NewReader(test.data))
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

func TestVerifyMP4WithFFprobe(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is required for this integration test")
	}
	// A generated 16x16 H.264 clip contains two frames. Run real parsing
	// against a second payload retaining only the original ftyp box.
	file := "testdata/validation.mp4"
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyMedia(context.Background(), ffprobe, "video", "video/mp4", int64(len(data)), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	fake := data[:32]
	if !hasMP4Brand(fake) {
		t.Fatal("test payload does not retain the plausible container header")
	}
	if err := verifyMedia(context.Background(), ffprobe, "video", "video/mp4", int64(len(fake)), bytes.NewReader(fake)); !errors.Is(err, ErrInvalidMedia) {
		t.Fatalf("header-only clip should be rejected, got %v", err)
	}
}
