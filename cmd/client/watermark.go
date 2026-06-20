package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/shape"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

var (
	watermarkMu        sync.Mutex
	watermarkActive    bool
	watermarkTriggerCh = make(chan struct{}, 1)
)

func findDisplay() string {
	if disp := os.Getenv("DISPLAY"); disp != "" {
		return disp
	}
	if files, err := filepath.Glob("/tmp/.X11-unix/X*"); err == nil && len(files) > 0 {
		name := filepath.Base(files[0])
		if len(name) > 1 && name[0] == 'X' {
			return ":" + name[1:]
		}
	}
	return ":0"
}

func stealXAuthorityFromProc() string {
	files, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}

	var backupXauth string

	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		name := f.Name()
		if len(name) == 0 || name[0] < '0' || name[0] > '9' {
			continue
		}

		environPath := filepath.Join("/proc", name, "environ")
		envBytes, err := os.ReadFile(environPath)
		if err != nil {
			continue
		}

		envList := strings.Split(string(envBytes), "\x00")
		hasDisplay0 := false
		xauthVal := ""
		isTargetUserProcess := false

		for _, env := range envList {
			if strings.HasPrefix(env, "DISPLAY=:0") {
				hasDisplay0 = true
			}
			if strings.HasPrefix(env, "XAUTHORITY=") {
				xauthVal = strings.TrimPrefix(env, "XAUTHORITY=")
			}
			// Identify normal player process (non-root GUI process)
			if strings.HasPrefix(env, "USER=") {
				u := strings.TrimPrefix(env, "USER=")
				if u != "root" && u != "gdm" && u != "sddm" && u != "lightdm" {
					isTargetUserProcess = true
				}
			}
			if strings.HasPrefix(env, "XDG_RUNTIME_DIR=/run/user/") {
				if !strings.HasSuffix(env, "/0") {
					isTargetUserProcess = true
				}
			}
		}

		if hasDisplay0 && xauthVal != "" {
			if _, err := os.Stat(xauthVal); err == nil {
				if isTargetUserProcess {
					log.Printf("[watermark] stole target user XAUTHORITY from PID %s: %s", name, xauthVal)
					return xauthVal
				}
				backupXauth = xauthVal
			}
		}
	}

	if backupXauth != "" {
		log.Printf("[watermark] stole backup XAUTHORITY: %s", backupXauth)
		return backupXauth
	}

	return ""
}

func findXAuthority() string {
	// 0. 优先从 /proc 进程中窃取活动 GUI 进程的 XAUTHORITY (支持 root 降权运行与 X11 凭证窃取)
	if stolen := stealXAuthorityFromProc(); stolen != "" {
		return stolen
	}

	if xauth := os.Getenv("XAUTHORITY"); xauth != "" {
		if _, err := os.Stat(xauth); err == nil {
			return xauth
		}
	}

	if files, err := filepath.Glob("/home/*/.Xauthority"); err == nil {
		for _, f := range files {
			if _, err := os.Stat(f); err == nil {
				return f
			}
		}
	}

	if files, err := filepath.Glob("/run/user/*/xauth_*"); err == nil {
		for _, f := range files {
			if _, err := os.Stat(f); err == nil {
				return f
			}
		}
	}

	if os.Geteuid() == 0 {
		cmd := exec.Command("loginctl", "list-sessions", "--no-legend")
		if out, err := cmd.Output(); err == nil {
			lines := strings.Split(string(out), "\n")
			for _, line := range lines {
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					username := fields[2]
					if username != "" && username != "root" {
						xauthPath := filepath.Join("/home", username, ".Xauthority")
						if _, err := os.Stat(xauthPath); err == nil {
							return xauthPath
						}
					}
				}
			}
		}
	}

	if _, err := os.Stat("/root/.Xauthority"); err == nil {
		return "/root/.Xauthority"
	}

	return ""
}

func getFontFace(size float64) (font.Face, error) {
	paths := []string{
		// WenQuanYi MicroHei / ZenHei (very common on Debian/Ubuntu for Chinese)
		"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
		"/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc",
		// Noto Sans CJK (Default modern Debian Chinese fonts)
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Bold.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Bold.ttc",
		"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/opentype/noto/NotoSansSC-Bold.otf",
		"/usr/share/fonts/opentype/noto/NotoSansSC-Regular.otf",
		// Droid Sans Fallback (legacy fallback font)
		"/usr/share/fonts/truetype/droid/DroidSansFallback.ttf",
		// Default Latin fonts
		"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf",
		"/usr/share/fonts/truetype/freefont/FreeSansBold.ttf",
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			var f *sfnt.Font
			// 1. Try parsing as collection (TTC/OTC) first
			coll, errColl := sfnt.ParseCollection(data)
			if errColl == nil && coll.NumFonts() > 0 {
				f, err = coll.Font(0)
			} else {
				// 2. Fall back to parsing as single font (TTF/OTF)
				f, err = opentype.Parse(data)
			}

			if err == nil && f != nil {
				face, errFace := opentype.NewFace(f, &opentype.FaceOptions{
					Size:    size,
					DPI:     72,
					Hinting: font.HintingFull,
				})
				if errFace == nil {
					log.Printf("[watermark] successfully loaded font: %s", p)
					return face, nil
				}
			}
		}
	}
	log.Println("[watermark] WARNING: no system font found, falling back to built-in basicfont (Chinese characters will render as boxes).")
	return basicfont.Face7x13, nil
}

func getWatermarkData() (string, string, string, string) {
	state.mu.Lock()
	defer state.mu.Unlock()

	status := "在线"
	if state.send == nil {
		status = "离线"
	}
	name := state.studentName
	num := state.studentNum
	if state.checkinStatus == 0 {
		name = "未签到"
		num = ""
	} else if state.checkinStatus == 2 {
		name = "已签退"
		num = ""
	}

	userStr := ""
	if num != "" {
		userStr = fmt.Sprintf("选手: %s %s", name, num)
	} else {
		userStr = fmt.Sprintf("选手: %s", name)
	}

	host := fmt.Sprintf("主机: %s", state.hostname)
	ip := fmt.Sprintf("IP: %s", state.ipAddr)

	return host, ip, userStr, status
}

func drawTextOutline(img *image.RGBA, face font.Face, text string, lineX, lineY int) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.Black,
		Face: face,
	}
	// 4-way offsets for thinner/lighter outline
	offsets := [][2]int{
		{-1, 0}, {1, 0}, {0, -1}, {0, 1},
	}
	for _, off := range offsets {
		dx, dy := off[0], off[1]
		d.Dot = fixed.Point26_6{
			X: fixed.Int26_6((lineX + dx) << 6),
			Y: fixed.Int26_6((lineY + dy) << 6),
		}
		d.DrawString(text)
	}
}

func drawSingleLine(img *image.RGBA, face font.Face, text string, srcColor image.Image, w, lineY int) {
	d := &font.Drawer{
		Dst:  img,
		Face: face,
	}
	width := d.MeasureString(text).Ceil()
	lineX := w - 5 - width

	// Draw 4-way outline in Black
	drawTextOutline(img, face, text, lineX, lineY)

	// Draw text in target color
	d.Src = srcColor
	d.Dot = fixed.Point26_6{
		X: fixed.Int26_6(lineX << 6),
		Y: fixed.Int26_6(lineY << 6),
	}
	d.DrawString(text)
}

func drawOutlineText(img *image.RGBA, face font.Face, host, ip, userStr, statusVal string, w, startY int) {
	lines := []string{host, ip, userStr}

	// Draw the first 3 lines (all white text)
	for i, line := range lines {
		lineY := startY + i*18
		drawSingleLine(img, face, line, image.White, w, lineY)
	}

	// Draw the 4th line (状态: [statusVal])
	lineY := startY + 3*18

	// Determine status color
	var statusColor image.Image
	if statusVal == "在线" || statusVal == "Online" {
		statusColor = &image.Uniform{color.RGBA{0, 220, 0, 255}} // Bright green
	} else {
		statusColor = &image.Uniform{color.RGBA{255, 50, 50, 255}} // Bright red
	}

	// Draw status value right-aligned
	d := &font.Drawer{
		Dst:  img,
		Face: face,
	}

	statusWidth := d.MeasureString(statusVal).Ceil()
	lineXVal := w - 5 - statusWidth

	// Draw status value outline (4-way)
	drawTextOutline(img, face, statusVal, lineXVal, lineY)
	// Draw status value text in Green/Red
	d.Src = statusColor
	d.Dot = fixed.Point26_6{
		X: fixed.Int26_6(lineXVal << 6),
		Y: fixed.Int26_6(lineY << 6),
	}
	d.DrawString(statusVal)

	// Draw "状态: " right-aligned to the left of status value
	prefixText := "状态: "
	prefixWidth := d.MeasureString(prefixText).Ceil()
	lineXPrefix := lineXVal - prefixWidth

	// Draw prefix outline (4-way)
	drawTextOutline(img, face, prefixText, lineXPrefix, lineY)
	// Draw prefix text in White
	d.Src = image.White
	d.Dot = fixed.Point26_6{
		X: fixed.Int26_6(lineXPrefix << 6),
		Y: fixed.Int26_6(lineY << 6),
	}
	d.DrawString(prefixText)
}

func convertRGBAToBGRA(img *image.RGBA) []byte {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	data := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			offsetSrc := img.PixOffset(x, y)
			offsetDst := (y*w + x) * 4
			r := img.Pix[offsetSrc]
			g := img.Pix[offsetSrc+1]
			b := img.Pix[offsetSrc+2]
			a := img.Pix[offsetSrc+3]

			if a == 0 {
				data[offsetDst] = 0
				data[offsetDst+1] = 0
				data[offsetDst+2] = 0
				data[offsetDst+3] = 0
			} else {
				data[offsetDst] = byte(uint32(b) * uint32(a) / 255)
				data[offsetDst+1] = byte(uint32(g) * uint32(a) / 255)
				data[offsetDst+2] = byte(uint32(r) * uint32(a) / 255)
				data[offsetDst+3] = a
			}
		}
	}
	return data
}

func runWatermarkLoop() error {
	display := findDisplay()
	xauth := findXAuthority()

	os.Setenv("DISPLAY", display)
	if xauth != "" {
		os.Setenv("XAUTHORITY", xauth)
	}

	conn, err := xgb.NewConn()
	if err != nil {
		return fmt.Errorf("xgb connect: %w", err)
	}
	defer conn.Close()

	err = shape.Init(conn)
	if err != nil {
		log.Printf("[watermark] shape extension init failed: %v", err)
	}

	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)

	ws := int(screen.WidthInPixels)
	hs := int(screen.HeightInPixels)

	w, h := 250, 78
	x := int16(ws - w - 3)
	y := int16(hs - h - 45)

	var visualID xproto.Visualid
	var depth byte = 24
	for _, d := range screen.AllowedDepths {
		if d.Depth == 32 {
			for _, v := range d.Visuals {
				visualID = v.VisualId
				depth = 32
				break
			}
		}
	}
	if depth != 32 {
		visualID = screen.RootVisual
		depth = screen.RootDepth
	}

	var colormap xproto.Colormap
	if depth == 32 {
		colormap, err = xproto.NewColormapId(conn)
		if err == nil {
			xproto.CreateColormap(conn, xproto.ColormapAllocNone, colormap, screen.Root, visualID)
		}
	} else {
		colormap = screen.DefaultColormap
	}

	win, err := xproto.NewWindowId(conn)
	if err != nil {
		return fmt.Errorf("new window id: %w", err)
	}

	var mask uint32 = xproto.CwBackPixel | xproto.CwBorderPixel | xproto.CwOverrideRedirect | xproto.CwEventMask
	values := []uint32{
		0,
		0,
		1,
		xproto.EventMaskExposure,
	}
	if depth == 32 {
		mask |= xproto.CwColormap
		values = append(values, uint32(colormap))
	}

	xproto.CreateWindow(conn, depth, win, screen.Root,
		x, y, uint16(w), uint16(h),
		0,
		xproto.WindowClassInputOutput,
		visualID,
		mask, values)

	if err == nil {
		shape.Rectangles(conn, shape.SoSet, shape.SkInput, 0, win, 0, 0, nil)
	}

	gc, _ := xproto.NewGcontextId(conn)
	xproto.CreateGC(conn, gc, xproto.Drawable(win), 0, nil)

	xproto.MapWindow(conn, win)

	face, err := getFontFace(13)
	if err != nil {
		log.Printf("[watermark] get font face failed: %v", err)
	}

	watermarkMu.Lock()
	watermarkActive = true
	watermarkMu.Unlock()
	defer func() {
		watermarkMu.Lock()
		watermarkActive = false
		watermarkMu.Unlock()
	}()

	activeWinAtom := getAtom(conn, "_NET_ACTIVE_WINDOW")
	wmPidAtom := getAtom(conn, "_NET_WM_PID")

	curW, curH, curX, curY := 0, 0, 0, 0

	for {
		isKiosk := false
		activeWin, errProp := getWindowProperty32(conn, screen.Root, activeWinAtom)
		if errProp == nil && activeWin != 0 {
			pid := getWindowPID(conn, xproto.Window(activeWin), wmPidAtom, screen.Root)
			if pid != 0 {
				cmdline := getProcessCmdline(pid)
				if strings.Contains(cmdline, "firefox") && strings.Contains(cmdline, "--kiosk") {
					isKiosk = true
				}
			}
		}

		xproto.MapWindow(conn, win)
		xproto.ConfigureWindow(conn, win, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove})

		host, ip, userStr, statusVal := getWatermarkData()

		// Calculate target dimensions
		targetW, targetH, targetX, targetY := 250, 78, ws-250-3, hs-78-50
		if isKiosk {
			prefixText := fmt.Sprintf("%s | %s | 状态: ", host, ip)
			d := &font.Drawer{Face: face}
			prefixWidth := d.MeasureString(prefixText).Ceil()
			statusWidth := d.MeasureString(statusVal).Ceil()
			totalWidth := prefixWidth + statusWidth

			targetW = totalWidth + 4 // 2px padding on each side
			targetH = 20
			targetX = ws - targetW - 2
			targetY = hs - targetH - 2
		}

		// Configure window size and position dynamically
		if curW != targetW || curH != targetH || curX != targetX || curY != targetY {
			xproto.ConfigureWindow(conn, win, xproto.ConfigWindowX|xproto.ConfigWindowY|xproto.ConfigWindowWidth|xproto.ConfigWindowHeight, []uint32{
				uint32(targetX),
				uint32(targetY),
				uint32(targetW),
				uint32(targetH),
			})
			curW, curH, curX, curY = targetW, targetH, targetX, targetY
		}

		img := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
		if depth == 32 {
			draw.Draw(img, img.Bounds(), &image.Uniform{color.Transparent}, image.Point{}, draw.Src)
		} else {
			draw.Draw(img, img.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)
		}

		if isKiosk {
			prefixText := fmt.Sprintf("%s | %s | 状态: ", host, ip)
			d := &font.Drawer{
				Dst:  img,
				Face: face,
			}
			prefixWidth := d.MeasureString(prefixText).Ceil()
			statusWidth := d.MeasureString(statusVal).Ceil()
			totalWidth := prefixWidth + statusWidth

			lineXPrefix := targetW - 2 - totalWidth
			lineXStatus := lineXPrefix + prefixWidth
			lineY := 15

			// Draw prefix outline & text
			drawTextOutline(img, face, prefixText, lineXPrefix, lineY)
			d.Src = image.White
			d.Dot = fixed.Point26_6{
				X: fixed.Int26_6(lineXPrefix << 6),
				Y: fixed.Int26_6(lineY << 6),
			}
			d.DrawString(prefixText)

			// Draw status outline & text (Green/Red)
			var statusColor image.Image
			if statusVal == "在线" || statusVal == "Online" {
				statusColor = &image.Uniform{color.RGBA{0, 220, 0, 255}}
			} else {
				statusColor = &image.Uniform{color.RGBA{255, 50, 50, 255}}
			}
			drawTextOutline(img, face, statusVal, lineXStatus, lineY)
			d.Src = statusColor
			d.Dot = fixed.Point26_6{
				X: fixed.Int26_6(lineXStatus << 6),
				Y: fixed.Int26_6(lineY << 6),
			}
			d.DrawString(statusVal)
		} else {
			drawOutlineText(img, face, host, ip, userStr, statusVal, targetW, 15)
		}

		bgraData := convertRGBAToBGRA(img)

		xproto.PutImage(conn, xproto.ImageFormatZPixmap, xproto.Drawable(win), gc,
			uint16(targetW), uint16(targetH), 0, 0, 0, depth, bgraData)

		select {
		case <-watermarkTriggerCh:
		case <-time.After(3 * time.Second):
		}
	}
}

func startWatermark() {
	go func() {
		for {
			err := runWatermarkLoop()
			if err != nil {
				log.Printf("[watermark] X11 loop exited: %v", err)
			}
			time.Sleep(5 * time.Second)
		}
	}()
}

func updateWatermark() {
	select {
	case watermarkTriggerCh <- struct{}{}:
	default:
	}
}

func stopWatermark() {
	// Pure Go watermark uses defer conn.Close() inside runWatermarkLoop.
	// When the client exits, the X11 connection automatically drops, which instantly destroys the window.
	// No explicit cleanup command is required.
}

func getAtom(conn *xgb.Conn, name string) xproto.Atom {
	reply, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0
	}
	return reply.Atom
}

func getWindowProperty32(conn *xgb.Conn, win xproto.Window, atom xproto.Atom) (uint32, error) {
	reply, err := xproto.GetProperty(conn, false, win, atom, xproto.AtomAny, 0, 1).Reply()
	if err != nil {
		return 0, err
	}
	if reply.Format != 32 || len(reply.Value) < 4 {
		return 0, fmt.Errorf("property format not 32 or value too short")
	}
	return uint32(reply.Value[0]) | uint32(reply.Value[1])<<8 | uint32(reply.Value[2])<<16 | uint32(reply.Value[3])<<24, nil
}

func getWindowParent(conn *xgb.Conn, win xproto.Window) (xproto.Window, error) {
	reply, err := xproto.QueryTree(conn, win).Reply()
	if err != nil {
		return 0, err
	}
	return reply.Parent, nil
}

func getWindowPID(conn *xgb.Conn, win xproto.Window, wmPidAtom xproto.Atom, root xproto.Window) uint32 {
	current := win
	for current != 0 && current != root {
		pid, err := getWindowProperty32(conn, current, wmPidAtom)
		if err == nil && pid != 0 {
			return pid
		}
		parent, err := getWindowParent(conn, current)
		if err != nil || parent == current {
			break
		}
		current = parent
	}
	return 0
}

func getProcessCmdline(pid uint32) string {
	if pid == 0 {
		return ""
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	return string(data)
}
