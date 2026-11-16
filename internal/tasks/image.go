package tasks

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/emersion/go-ical"
)

// MaxImageBytes caps one pasted image (decoded), which keeps the object under
// calendar.MaxObjectSize once base64 inflates it.
const MaxImageBytes = 512 << 10

var ErrBadImage = errors.New("tasks: not a PNG, JPEG or WebP image")

// ImageType is the MIME type data's magic bytes name, or "".
func ImageType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// Image is an inline ATTACH the server can show: base64 binary with a type its bytes confirm.
type Image struct {
	Name, Type string
	Data       []byte
}

// AddImage appends img to the task as ATTACH;ENCODING=BASE64;VALUE=BINARY;FMTTYPE;FILENAME.
func AddImage(cal *ical.Calendar, img Image) error {
	m, err := Master(cal)
	if err != nil {
		return err
	}
	if len(img.Data) == 0 || len(img.Data) > MaxImageBytes || ImageType(img.Data) == "" {
		return ErrBadImage
	}
	p := ical.NewProp("ATTACH")
	p.Params.Set("ENCODING", "BASE64")
	p.Params.Set(ical.ParamValue, "BINARY")
	p.Params.Set("FMTTYPE", ImageType(img.Data))
	if img.Name != "" {
		p.Params.Set("FILENAME", img.Name)
	}
	p.Value = base64.StdEncoding.EncodeToString(img.Data)
	m.Props.Add(p)
	return nil
}

// Images are the task's inline images whose bytes are a PNG, JPEG or WebP, in order; the type
// is always the one the bytes name, never the stored FMTTYPE.
func Images(cal *ical.Calendar) []Image {
	m, err := Master(cal)
	if err != nil {
		return nil
	}
	var out []Image
	for _, p := range m.Props["ATTACH"] {
		if !strings.EqualFold(p.Params.Get("ENCODING"), "BASE64") {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(p.Value)
		if err != nil || len(data) > MaxImageBytes || ImageType(data) == "" {
			continue
		}
		out = append(out, Image{Name: p.Params.Get("FILENAME"), Type: ImageType(data), Data: data})
	}
	return out
}
