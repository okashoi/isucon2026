package isureal

import (
	"log"
	"net/http"
	"os"
	"time"
)

func notifyPproteinCollect() {
	url := os.Getenv("PPROTEIN_WEBHOOK_URL")
	if url == "" {
		return
	}
	go func(url string) {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			log.Printf("pprotein webhook failed: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			log.Printf("pprotein webhook returned non-2xx: %d", resp.StatusCode)
		}
	}(url)
}
