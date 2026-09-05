package main

import (
	"bytes"
	"image/png"
	"strings"
	"testing"

	"github.com/skip2/go-qrcode"
)

func TestQRDoesNotOverflowTerminal(t *testing.T) {
	code, err := qrcode.New("kagero://pair?data="+strings.Repeat("abc123XYZ", 80), qrcode.Medium)
	if err != nil {
		t.Fatal(err)
	}
	modules := len(code.Bitmap())
	rows := (modules+1)/2 + 10
	if qrFitsTerminal(code, modules+2, rows, false) {
		t.Fatal("large invitation should use image preview by default")
	}
	if !qrFitsTerminal(code, modules+2, rows, true) {
		t.Fatal("explicit terminal QR should fit the exact available space")
	}
	if qrFitsTerminal(code, modules+1, rows, true) || qrFitsTerminal(code, modules+2, rows-1, true) {
		t.Fatal("QR must not wrap or scroll instructions off screen")
	}
	data, err := code.PNG(qrImageSize(code))
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width > 480 || config.Width != config.Height || config.Width%modules != 0 {
		t.Fatal("QR preview must be compact, square and aligned to whole module pixels", config, err)
	}
}
