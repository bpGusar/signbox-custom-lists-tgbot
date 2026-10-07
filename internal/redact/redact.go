// Package redact strips secrets from log output: the bot token, proxy share
// links and key=value credentials, so a log can be handed over as is.
package redact

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
)

const mask = "<redacted>"

var (
	botTokenRe  = regexp.MustCompile(`\d{5,}:[A-Za-z0-9_-]{30,}`)
	shareLinkRe = regexp.MustCompile(`(?i)\b(vless|vmess|ssr|ss|trojan|socks4a|socks4|socks5h|socks5|socks|hysteria2|hysteria|hy2|tuic|wireguard|wg|anytls)://[^\s"'<>]+`)
	userInfoRe  = regexp.MustCompile(`(?i)\b(https?|ftp)://[^\s/@:"'<>]+:[^\s/@"'<>]+@`)
	keyValueRe  = regexp.MustCompile(`(?i)\b([\w.-]*(?:password|passwd|secret|token|private_key|privatekey|psk|api_key|apikey))(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&"']+)`)
)

// String returns s with every secret it recognizes replaced by a mask.
// literals are exact values known to be secret, such as the configured token.
func String(s string, literals ...string) string {
	for _, lit := range literals {
		if len(lit) >= 8 {
			s = strings.ReplaceAll(s, lit, mask)
		}
	}
	s = botTokenRe.ReplaceAllString(s, mask)
	s = shareLinkRe.ReplaceAllString(s, "${1}://"+mask)
	s = userInfoRe.ReplaceAllString(s, "${1}://"+mask+"@")
	s = keyValueRe.ReplaceAllString(s, "${1}${2}"+mask)
	return s
}

type writer struct {
	mu       sync.Mutex
	w        io.Writer
	literals []string
}

// NewWriter wraps w so everything written through it is redacted first.
func NewWriter(w io.Writer, literals ...string) io.Writer {
	return &writer{w: w, literals: literals}
}

func (r *writer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := io.WriteString(r.w, String(string(p), r.literals...)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// File rewrites the file at path with its secrets redacted. The file is left
// untouched when there is nothing to hide.
func File(path string, literals ...string) error {
	src, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = src.Close() }()

	tmpPath := path + ".redact"
	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpPath) }()

	changed, err := copyRedacted(dst, src, literals)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil || !changed {
		return err
	}
	_ = src.Close()
	return os.Rename(tmpPath, path)
}

func copyRedacted(dst io.Writer, src io.Reader, literals []string) (bool, error) {
	in := bufio.NewReader(src)
	out := bufio.NewWriter(dst)
	changed := false
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			clean := String(string(line), literals...)
			if !bytes.Equal(line, []byte(clean)) {
				changed = true
			}
			if _, werr := out.WriteString(clean); werr != nil {
				return false, werr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, err
		}
	}
	return changed, out.Flush()
}
