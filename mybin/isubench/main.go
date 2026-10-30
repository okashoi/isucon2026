// isubench: 公式ベンチ1回分の通信を録画し、別バージョンのアプリへ再生する。
// 正常版と比べてレスポンスの食い違いが増えていないかと、CPU 秒が下がったかを出す。
//
//	isubench record -listen 127.0.0.1:18080 -upstream http://127.0.0.1:8080 -out record.jsonl
//	isubench replay -in record.jsonl -target https://127.0.0.1 -baseline baseline.json   # 正常版
//	isubench replay -in record.jsonl -target https://127.0.0.1 -against baseline.json    # 候補
package main

import (
	"fmt"
	"os"
)

// Record は1リクエスト分の録画。t_ms は最初のリクエストからの経過時間。
// *_b64 が true の本文は base64 で保存してあり、load で元のバイト列に戻す。
type Record struct {
	TMs        float64           `json:"t_ms"`
	DurMs      float64           `json:"dur_ms"`
	Method     string            `json:"method"`
	Host       string            `json:"host"`
	URI        string            `json:"uri"`
	ReqHeader  map[string]string `json:"req_header"`
	ReqBody    string            `json:"req_body"`
	ReqB64     bool              `json:"req_b64,omitempty"`
	Status     int               `json:"status"`
	RespHeader map[string]string `json:"resp_header"`
	RespBody   string            `json:"resp_body"`
	RespB64    bool              `json:"resp_b64,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: isubench record|replay|check [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "record":
		record(os.Args[2:])
	case "replay":
		replay(os.Args[2:])
	case "check":
		check(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}
}
