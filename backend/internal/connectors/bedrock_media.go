package connectors

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/mydisha/keirouter/backend/internal/core"
	"github.com/mydisha/keirouter/backend/internal/httputil"
)

// Bedrock takes media as raw bytes (or an S3 location), never as a URL the
// service fetches itself. Inline base64 data passes through; http(s) URLs are
// downloaded here under the outbound SSRF policy and a size cap.

const (
	// bedrockMaxMediaBytes caps one downloaded attachment. Bedrock's own
	// limits are lower (images 3.75 MB, documents 4.5 MB); anything between
	// is left for the service to reject with its precise message.
	bedrockMaxMediaBytes = 25 << 20
	bedrockMediaTimeout  = 30 * time.Second
	bedrockMaxRedirects  = 5
)

// bedrockMedia is one attachment resolved for a Bedrock request: base64 data
// or an S3 URI, plus the best-known MIME type.
type bedrockMedia struct {
	Data  string
	S3URI string
	MIME  string
	// Name is the file name hint (URL path base), used for format detection.
	Name string
}

// resolveBedrockMedia turns a canonical media payload into bytes or an S3
// reference.
func resolveBedrockMedia(ctx context.Context, provider, model string, m *core.MediaPayload) (*bedrockMedia, error) {
	if m == nil {
		return nil, bedrockBadRequest(provider, model, "attachment has no content")
	}
	if m.Data != "" {
		return &bedrockMedia{Data: m.Data, MIME: m.MIMEType}, nil
	}
	u := strings.TrimSpace(m.URL)
	switch {
	case u == "":
		return nil, bedrockBadRequest(provider, model, "attachment has no content")
	case strings.HasPrefix(u, "s3://"):
		return &bedrockMedia{S3URI: u, MIME: m.MIMEType, Name: path.Base(u)}, nil
	case strings.HasPrefix(u, "data:"):
		rest := strings.TrimPrefix(u, "data:")
		meta, data, ok := strings.Cut(rest, ",")
		if !ok || !strings.HasSuffix(meta, ";base64") {
			return nil, bedrockBadRequest(provider, model, "attachment data URL must be base64-encoded")
		}
		return &bedrockMedia{Data: data, MIME: bedrockFirstNonEmpty(m.MIMEType, strings.TrimSuffix(meta, ";base64"))}, nil
	}
	raw, contentType, err := fetchBedrockMedia(ctx, provider, model, u)
	if err != nil {
		return nil, err
	}
	mt := m.MIMEType
	if mt == "" && contentType != "application/octet-stream" && contentType != "binary/octet-stream" {
		mt = contentType
	}
	if mt == "" {
		mt = http.DetectContentType(raw)
	}
	name := ""
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		name = path.Base(u[:i])
	} else {
		name = path.Base(u)
	}
	return &bedrockMedia{Data: base64.StdEncoding.EncodeToString(raw), MIME: mt, Name: name}, nil
}

// fetchBedrockMedia downloads a remote attachment through the account's
// egress route. Every redirect hop is re-checked against the SSRF policy.
func fetchBedrockMedia(ctx context.Context, provider, model, rawURL string) ([]byte, string, error) {
	if err := httputil.ValidateOutboundURL(rawURL); err != nil {
		return nil, "", bedrockBadRequest(provider, model, "attachment URL rejected: "+err.Error())
	}
	client := &http.Client{
		Transport: proxyClient(ctx).Transport,
		Timeout:   bedrockMediaTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= bedrockMaxRedirects {
				return errors.New("too many redirects")
			}
			return httputil.ValidateOutboundURL(req.URL.String())
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", bedrockBadRequest(provider, model, "invalid attachment URL")
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", bedrockBadRequest(provider, model, "download attachment: "+err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", bedrockBadRequest(provider, model, fmt.Sprintf("download attachment: HTTP %d", resp.StatusCode))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, bedrockMaxMediaBytes+1))
	if err != nil {
		return nil, "", bedrockBadRequest(provider, model, "download attachment: "+err.Error())
	}
	if len(b) > bedrockMaxMediaBytes {
		return nil, "", bedrockBadRequest(provider, model, fmt.Sprintf("attachment exceeds %d bytes", bedrockMaxMediaBytes))
	}
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return b, ct, nil
}

// sniffMIME detects the type of base64 data from its first bytes.
func (m *bedrockMedia) sniffMIME() string {
	if m.Data == "" {
		return ""
	}
	head := m.Data
	if len(head) > 1024 {
		head = head[:1024]
	}
	head = head[:len(head)/4*4]
	raw, err := base64.StdEncoding.DecodeString(head)
	if err != nil || len(raw) == 0 {
		return ""
	}
	ct, _, _ := mime.ParseMediaType(http.DetectContentType(raw))
	return ct
}

// kind returns "image", "document" or "video" for the attachment.
func (m *bedrockMedia) kind(fallback string) string {
	mt := strings.ToLower(m.MIME)
	switch {
	case strings.HasPrefix(mt, "image/"):
		return "image"
	case strings.HasPrefix(mt, "video/"):
		return "video"
	case mt != "":
		return "document"
	}
	return fallback
}

var bedrockImageFormats = map[string]string{
	"image/png": "png", "image/jpeg": "jpeg", "image/jpg": "jpeg", "image/pjpeg": "jpeg",
	"image/gif": "gif", "image/webp": "webp",
	"png": "png", "jpeg": "jpeg", "jpg": "jpeg", "gif": "gif", "webp": "webp",
}

var bedrockDocumentFormats = map[string]string{
	"application/pdf": "pdf", "text/csv": "csv", "application/msword": "doc",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"application/vnd.ms-excel": "xls",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "xlsx",
	"text/html": "html", "text/plain": "txt", "text/markdown": "md", "text/x-markdown": "md",
	"pdf": "pdf", "csv": "csv", "doc": "doc", "docx": "docx", "xls": "xls", "xlsx": "xlsx",
	"html": "html", "htm": "html", "txt": "txt", "md": "md", "markdown": "md",
}

var bedrockVideoFormats = map[string]string{
	"video/mp4": "mp4", "video/quicktime": "mov", "video/x-matroska": "mkv", "video/webm": "webm",
	"video/x-flv": "flv", "video/mpeg": "mpeg", "video/x-ms-wmv": "wmv", "video/3gpp": "three_gp",
	"mp4": "mp4", "mov": "mov", "mkv": "mkv", "webm": "webm", "flv": "flv", "mpeg": "mpeg",
	"mpg": "mpg", "wmv": "wmv", "3gp": "three_gp",
}

// format resolves the Bedrock format token for the attachment from its MIME
// type, file extension or (for inline data) its magic bytes.
func (m *bedrockMedia) format(table map[string]string) string {
	mt := strings.ToLower(strings.TrimSpace(m.MIME))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	if f, ok := table[mt]; ok {
		return f
	}
	if ext := strings.TrimPrefix(strings.ToLower(path.Ext(m.Name)), "."); ext != "" {
		if f, ok := table[ext]; ok {
			return f
		}
	}
	if sniffed := m.sniffMIME(); sniffed != "" {
		if f, ok := table[sniffed]; ok {
			return f
		}
	}
	return ""
}

func bedrockBadRequest(provider, model, msg string) *core.ProviderError {
	return &core.ProviderError{Kind: core.ErrBadRequest, Scope: core.FailureScopeRequest, Provider: provider, Model: model,
		StatusCode: http.StatusBadRequest, Message: msg}
}
