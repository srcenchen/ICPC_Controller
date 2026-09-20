package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/image/draw"
)

var (
	screenCaptureEnabled   bool
	screenCaptureEnabledMu sync.RWMutex
)

func setScreenCaptureEnabled(enabled bool) {
	screenCaptureEnabledMu.Lock()
	defer screenCaptureEnabledMu.Unlock()
	screenCaptureEnabled = enabled
	log.Printf("[screen-monitor] capture enabled state changed to: %v", enabled)
}

func isScreenCaptureEnabled() bool {
	screenCaptureEnabledMu.RLock()
	defer screenCaptureEnabledMu.RUnlock()
	return screenCaptureEnabled
}

// prepareX11Env lives in x11env.go (shared with the watermark path).

// captureScreen captures the active display screen.
// A single X11 connection is shared across all frames and viewers. Opening a
// new xgb connection per frame (the previous behaviour) was needless overhead
// at 4 FPS; the connection is reopened lazily if it drops.
var (
	captureMu   sync.Mutex
	captureConn *xgb.Conn
	captureRoot xproto.Window
	captureW    uint16
	captureH    uint16
	captureRGBA *image.RGBA // reused backing buffer
)

func ensureCaptureConn() error {
	if captureConn != nil {
		return nil
	}
	prepareX11Env()
	c, err := xgb.NewConn()
	if err != nil {
		return fmt.Errorf("xgb conn: %w", err)
	}
	setup := xproto.Setup(c)
	if setup == nil || len(setup.Roots) == 0 {
		c.Close()
		return fmt.Errorf("xgb setup roots is empty")
	}
	screen := setup.DefaultScreen(c)
	captureConn = c
	captureRoot = screen.Root
	captureW = screen.WidthInPixels
	captureH = screen.HeightInPixels
	return nil
}

func resetCaptureConn() {
	if captureConn != nil {
		captureConn.Close()
		captureConn = nil
	}
}

// captureScreenLocked grabs the root window over the shared connection. The
// RGBA backing buffer is reused between calls to avoid per-frame allocation.
// Caller must hold captureMu and fully consume the result before releasing it,
// since the buffer is overwritten on the next call.
func captureScreenLocked() (image.Image, error) {
	if err := ensureCaptureConn(); err != nil {
		return nil, err
	}

	width, height := captureW, captureH
	xImg, err := xproto.GetImage(captureConn, xproto.ImageFormatZPixmap, xproto.Drawable(captureRoot),
		0, 0, width, height, 0xffffffff).Reply()
	if err != nil {
		// Connection likely died (X restart / session change); drop it so the
		// next call reconnects instead of failing forever.
		resetCaptureConn()
		return nil, fmt.Errorf("xproto.GetImage: %w", err)
	}

	data := xImg.Data
	expectedLen := int(width) * int(height) * 4
	if len(data) < expectedLen {
		return nil, fmt.Errorf("xproto.GetImage returned data length %d, expected at least %d", len(data), expectedLen)
	}

	// Swap Blue and Red channels to convert BGRA to RGBA, and force opaque alpha.
	for i := 0; i < expectedLen; i += 4 {
		data[i], data[i+2] = data[i+2], data[i]
		data[i+3] = 255
	}

	// Reuse the backing image unless the resolution changed.
	if captureRGBA == nil || captureRGBA.Rect.Dx() != int(width) || captureRGBA.Rect.Dy() != int(height) {
		captureRGBA = &image.RGBA{
			Stride: 4 * int(width),
			Rect:   image.Rect(0, 0, int(width), int(height)),
		}
	}
	captureRGBA.Pix = data[:expectedLen]
	return captureRGBA, nil
}

// resizeImage scales down the image to fit max width.
func resizeImage(src image.Image, width, height int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.NearestNeighbor.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

// captureScreenJPEG captures the screen, resizes it depending on resolution mode, and encodes to JPEG.
// The whole capture+encode runs under captureMu so concurrent viewers can't
// race on the shared backing buffer.
func captureScreenJPEG(highRes bool) ([]byte, error) {
	captureMu.Lock()
	defer captureMu.Unlock()

	img, err := captureScreenLocked()
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	var quality int
	if highRes {
		// High Resolution: scale up to 1920 width, high quality (80)
		if w > 1920 {
			h = (h * 1920) / w
			w = 1920
			img = resizeImage(img, w, h)
		}
		quality = 80
	} else {
		// Low Resolution (grid thumbnail): scale down to 480 width to save CPU and bandwidth
		if w > 480 {
			h = (h * 480) / w
			w = 480
			img = resizeImage(img, w, h)
		}
		quality = 50
	}

	var buf bytes.Buffer
	err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	if err != nil {
		return nil, fmt.Errorf("jpeg encode: %w", err)
	}
	return buf.Bytes(), nil
}

// handleScreenStream streams the client's screen as an MJPEG feed.
func handleScreenStream(w http.ResponseWriter, r *http.Request) {
	// Enable CORS so the admin panel can load this stream directly from client IP
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if !isScreenCaptureEnabled() {
		http.Error(w, "Screen monitor is disabled by server admin", http.StatusForbidden)
		return
	}

	// Read resolution preference: high resolution if ?hd=1
	highRes := r.URL.Query().Get("hd") == "1"
	single := r.URL.Query().Get("single") == "1"

	if single {
		imgData, err := captureScreenJPEG(highRes)
		if err != nil {
			http.Error(w, fmt.Sprintf("capture screen: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(imgData)))
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Write(imgData)
		return
	}

	// Hijack the connection to bypass any http server WriteTimeout issues
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Webserver does not support hijacking", http.StatusInternalServerError)
		return
	}
	conn, bufrw, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Write custom HTTP multipart headers
	bufrw.WriteString("HTTP/1.1 200 OK\r\n")
	bufrw.WriteString("Content-Type: multipart/x-mixed-replace; boundary=frame\r\n")
	bufrw.WriteString("Cache-Control: no-cache, private, max-age=0, no-store, must-revalidate\r\n")
	bufrw.WriteString("Connection: keep-alive\r\n")
	bufrw.WriteString("Pragma: no-cache\r\n")
	bufrw.WriteString("Expires: 0\r\n\r\n")
	bufrw.Flush()

	// Capture and send frames at ~4 FPS (every 250ms) to ensure low latency and low CPU overhead
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !isScreenCaptureEnabled() {
				return
			}
			imgData, err := captureScreenJPEG(highRes)
			if err != nil {
				// Wait and try again (e.g. if X11 is temporarily unavailable or locked)
				continue
			}

			// Write boundary and MIME header
			bufrw.WriteString("--frame\r\n")
			bufrw.WriteString("Content-Type: image/jpeg\r\n")
			bufrw.WriteString(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(imgData)))
			_, err = bufrw.Write(imgData)
			if err != nil {
				return // Browser disconnected
			}
			bufrw.WriteString("\r\n")
			err = bufrw.Flush()
			if err != nil {
				return // Browser disconnected
			}
		}
	}
}
