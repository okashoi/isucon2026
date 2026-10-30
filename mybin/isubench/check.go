package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
)

// check は保存済みの再生結果 (replay -out) を基準と比べ直す。
func check(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	against := fs.String("against", "baseline.json", "正常版の基準 (replay -baseline で作ったもの)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		log.Fatal("usage: isubench check -against baseline.json result.json")
	}
	base, err := readRuns(*against)
	if err != nil {
		log.Fatal(err)
	}
	b, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	var cand Result
	if err := json.Unmarshal(b, &cand); err != nil {
		log.Fatal(err)
	}
	if !compare(base, cand) {
		os.Exit(1)
	}
}
