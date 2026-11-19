package upload

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	_ "golang.org/x/image/webp"
)

const (
	maxVideoBytes   = 200 << 20
	maxCoverBytes   = 10 << 20
	maxAvatarBytes  = 5 << 20
	maxRasterPixels = 8 * 1024 * 1024
	maxVideoPixels  = 16 * 1024 * 1024
)

func validMediaDeclaration(kind, contentType string, size int64) bool {
	if size <= 0 {
		return false
	}
	switch kind {
	case "video":
		return contentType == "video/mp4" && size <= maxVideoBytes
	case "cover", "avatar":
		limit := int64(maxCoverBytes)
		if kind == "avatar" {
			limit = maxAvatarBytes
		}
		return size <= limit && (contentType == "image/jpeg" || contentType == "image/png" || contentType == "image/webp")
	default:
		return false
	}
}

func verifyMedia(ctx context.Context, ffprobe, kind, contentType string, size int64, body io.Reader) error {
	if !validMediaDeclaration(kind, contentType, size) {
		return ErrInvalidMedia
	}
	if kind == "video" {
		return verifyMP4(ctx, ffprobe, size, body)
	}
	data, err := io.ReadAll(io.LimitReader(body, size+1))
	if err != nil {
		return fmt.Errorf("read raster bytes: %w", err)
	}
	if int64(len(data)) != size {
		return ErrInvalidMedia
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || "image/"+format != contentType || !validDimensions(cfg.Width, cfg.Height, maxRasterPixels) {
		return ErrInvalidMedia
	}
	// Decode pixel data too. A plausible header alone also matches truncated and
	// fabricated images that a browser cannot render.
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return ErrInvalidMedia
	}
	return nil
}

func validDimensions(width, height, pixelLimit int) bool {
	return width > 0 && height > 0 && width <= 8192 && height <= 8192 && width*height <= pixelLimit
}

func verifyMP4(ctx context.Context, ffprobe string, size int64, body io.Reader) error {
	// The ftyp box distinguishes ISO MP4 from other formats that share the
	// same FFmpeg demuxer. A short read is retried if the transport failed.
	first, err := io.ReadAll(io.LimitReader(body, min(size, 64<<10)))
	if err != nil {
		return fmt.Errorf("read MP4 header: %w", err)
	}
	if !hasMP4Brand(first) {
		return ErrInvalidMedia
	}
	file, err := os.CreateTemp("", "feedsystem-probe-*.mp4")
	if err != nil {
		return fmt.Errorf("create verification file: %w", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(first); err != nil {
		return fmt.Errorf("write verification header: %w", err)
	}
	remaining := size - int64(len(first))
	count, err := io.Copy(file, io.LimitReader(body, remaining+1))
	if err != nil {
		return fmt.Errorf("read MP4 bytes: %w", err)
	}
	if count != remaining {
		return ErrInvalidMedia
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close verification file: %w", err)
	}

	// Restrict FFmpeg to the temporary file and MOV/MP4 demuxer. MOV external
	// data references are explicitly disabled. Decode a bounded packet sample
	// to require a real video frame and exclude album cover streams.
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, ffprobe,
		"-v", "error", "-protocol_whitelist", "file", "-format_whitelist", "mov",
		"-enable_drefs", "0", "-use_absolute_path", "0", "-max_alloc", "67108864", "-max_streams", "16",
		"-max_pixels", strconv.Itoa(maxVideoPixels),
		"-threads", "1", "-select_streams", "V:0", "-count_frames", "-read_intervals", "%+#128",
		"-show_entries", "stream=codec_type,width,height,nb_read_frames", "-of", "json", file.Name())
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if probeCtx.Err() != nil {
			return ErrInvalidMedia
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return ErrInvalidMedia
		}
		return fmt.Errorf("start ffprobe: %w", err)
	}
	var response struct {
		Streams []struct {
			Type   string `json:"codec_type"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Frames string `json:"nb_read_frames"`
		} `json:"streams"`
	}
	if json.Unmarshal(out, &response) != nil || len(response.Streams) != 1 {
		return ErrInvalidMedia
	}
	video := response.Streams[0]
	frames, _ := strconv.Atoi(video.Frames)
	if video.Type != "video" || frames < 1 || !validDimensions(video.Width, video.Height, maxVideoPixels) {
		return ErrInvalidMedia
	}
	return nil
}

func hasMP4Brand(data []byte) bool {
	for len(data) >= 8 {
		size := uint64(binary.BigEndian.Uint32(data))
		headerBytes := uint64(8)
		if size == 1 {
			if len(data) < 16 {
				return false
			}
			size = binary.BigEndian.Uint64(data[8:])
			headerBytes = 16
		}
		if size < headerBytes || size > uint64(len(data)) {
			return false
		}
		switch string(data[4:8]) {
		case "free", "skip", "wide":
			data = data[size:]
		case "ftyp":
			brands := data[headerBytes:size]
			if len(brands) < 8 || len(brands)%4 != 0 || string(brands[:4]) == "qt  " {
				return false
			}
			if isMP4Brand(string(brands[:4])) {
				return true
			}
			for brands = brands[8:]; len(brands) >= 4; brands = brands[4:] {
				if isMP4Brand(string(brands[:4])) {
					return true
				}
			}
			return false
		default:
			return false
		}
	}
	return false
}

func isMP4Brand(brand string) bool {
	switch brand {
	case "isom", "iso2", "iso3", "iso4", "iso5", "iso6", "iso7", "iso8", "iso9",
		"mp41", "mp42", "avc1", "hvc1", "hev1", "M4V ", "MSNV", "dash", "cmfc", "cmfs":
		return true
	default:
		return false
	}
}
