package rpc

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"regexp"
	"time"
)

func write(w http.ResponseWriter, status int, response Response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	received := time.Now()
	fail := func(status int, code, message string) {
		write(w, status, result(code, message, nil))
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	_, origin := r.Header["Origin"]
	if err != nil || !net.ParseIP(ip).IsLoopback() || r.Host != s.host || origin {
		fail(403, "forbidden", "只允许本机原生客户端访问")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.discovery.Token)) != 1 {
		fail(401, "unauthorized", "本机认证失败")
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/api/rpc" || r.URL.RawQuery != "" {
		fail(400, "invalid_request", "请求地址或方法无效")
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		fail(415, "unsupported_media_type", "请求必须使用 JSON")
		return
	}
	s.mu.Lock()
	now := time.Now()
	s.rateTokens += now.Sub(s.rateAt).Seconds() * 20
	if s.rateTokens > 40 {
		s.rateTokens = 40
	}
	s.rateAt = now
	allowed := s.rateTokens >= 1
	if allowed {
		s.rateTokens--
	}
	s.mu.Unlock()
	if !allowed {
		fail(429, "rate_limited", "请求过于频繁")
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		fail(429, "rate_limited", "同时请求过多")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		fail(413, "message_too_large", "请求超出大小限制")
		return
	}
	var request Request
	if strict(raw, &request) != nil || !requestID.MatchString(request.RequestID) || request.Method == "" || len(request.Params) == 0 || string(request.Params) == "null" {
		fail(400, "invalid_request", "JSON 请求无效")
		return
	}
	write(w, 200, s.registry.Dispatch(request, received))
}

var requestID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func result(code, message string, data any) Response {
	return Response{Code: code, Message: message, Data: data}
}
func strict(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("extra JSON")
	}
	return nil
}
