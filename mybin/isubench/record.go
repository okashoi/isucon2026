package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// 再生時に nginx が付け直すか、Go のクライアントが自分で決めるヘッダ。
var dropReqHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Connection": true, "Accept-Encoding": true,
	"X-Forwarded-For": true, "X-Forwarded-Proto": true, "X-Forwarded-Host": true, "X-Real-Ip": true,
}

var keepRespHeaders = []string{"Set-Cookie", "Content-Type", "Location"}

func reqHeaders(h http.Header) map[string]string {
	m := map[string]string{}
	for k, v := range h {
		if !dropReqHeaders[k] {
			m[k] = strings.Join(v, ", ")
		}
	}
	return m
}

func respHeaders(h http.Header) map[string]string {
	m := map[string]string{}
	for _, k := range keepRespHeaders {
		if v := h.Values(k); len(v) > 0 {
			m[k] = strings.Join(v, "\n")
		}
	}
	return m
}

// encodeBody は UTF-8 でない本文 (画像など) を base64 にする。JSON の文字列は不正な UTF-8 を壊すため。
func encodeBody(b []byte) (string, bool) {
	if utf8.Valid(b) {
		return string(b), false
	}
	return base64.StdEncoding.EncodeToString(b), true
}

type ctxKey struct{}

// newRecorder は upstream への中継をしながら、1リクエストずつ ch に録画を送る。
func newRecorder(upstream *url.URL, skip *regexp.Regexp, ch chan<- Record) http.Handler {
	var startOnce sync.Once
	var start time.Time

	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.FlushInterval = -1
	proxy.Transport = &http.Transport{MaxIdleConnsPerHost: 1024}
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		// アプリに圧縮させると、録画した本文と再生時の (自動で展開される) 本文が比べられなくなる。
		r.Header.Del("Accept-Encoding")
	}
	proxy.ModifyResponse = func(res *http.Response) error {
		rec := res.Request.Context().Value(ctxKey{}).(*Record)
		rec.Status = res.StatusCode
		rec.RespHeader = respHeaders(res.Header)
		if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
			return nil // ponytail: SSE の本文は録らない。SSE が主役の問題では行単位の記録が要る
		}
		b, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			return err
		}
		rec.RespBody, rec.RespB64 = encodeBody(b)
		res.Body = io.NopCloser(bytes.NewReader(b))
		return nil
	}

	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { start = time.Now() })
		t0 := time.Now()
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		rec := &Record{
			TMs:       float64(t0.Sub(start).Microseconds()) / 1000,
			Method:    r.Method,
			Host:      r.Host,
			URI:       r.URL.RequestURI(),
			ReqHeader: reqHeaders(r.Header),
		}
		rec.ReqBody, rec.ReqB64 = encodeBody(body)
		proxy.ServeHTTP(rw, r.WithContext(context.WithValue(r.Context(), ctxKey{}, rec)))
		rec.DurMs = float64(time.Since(t0).Microseconds()) / 1000
		if !skip.MatchString(r.URL.Path) {
			ch <- *rec
		}
	})
}

func record(args []string) {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:18080", "")
	upstream := fs.String("upstream", "http://127.0.0.1:8080", "")
	out := fs.String("out", "record.jsonl", "")
	skip := fs.String("skip", `^/api/internal/`, "録画しないパス")
	fs.Parse(args)

	u, err := url.Parse(*upstream)
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	ch := make(chan Record, 4096)
	done := make(chan struct{})
	go func() {
		enc := json.NewEncoder(w)
		for r := range ch {
			enc.Encode(r)
		}
		w.Flush()
		f.Close()
		close(done)
	}()

	srv := &http.Server{Addr: *listen, Handler: newRecorder(u, regexp.MustCompile(*skip), ch)}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()
	log.Printf("recording %s -> %s into %s", *listen, *upstream, *out)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	close(ch)
	<-done
}
