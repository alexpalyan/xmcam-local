package main

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func staticHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/WebStyle_") {
		parts := strings.SplitN(path[1:], "/", 2)
		if len(parts) > 1 {
			path = "/" + parts[1]
		}
	}

	filePath := filepath.Join("Web", path)

	if strings.HasSuffix(path, ".html") || path == "/" {
		if path == "/" {
			filePath = filepath.Join("Web", "index.html")
		}
		content, err := os.ReadFile(filePath)
		if err == nil {
			shimScript := `<script type="text/javascript" src="/player_shim.js"></script>`
			if !bytes.Contains(content, []byte("/player_shim.js")) {
				content = bytes.Replace(content, []byte("</head>"), []byte(shimScript+"</head>"), 1)
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if _, err := w.Write(content); err != nil {
				log.Printf("write html: %v", err)
			}
			return
		}
	}

	if strings.HasSuffix(path, ".js") && path != "/player_shim.js" && path != "/jmuxer.min.js" {
		content, err := os.ReadFile(filePath)
		if err == nil {
			shimContent, shimErr := os.ReadFile(filepath.Join("Web", "player_shim.js"))
			if shimErr == nil {
				if !bytes.Contains(content, []byte("[xmcam-local]")) {
					content = append(content, []byte("\n\n;/* xmcam-local shim injection */\n")...)
					content = append(content, shimContent...)
				}
			}
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			if _, err := w.Write(content); err != nil {
				log.Printf("write js: %v", err)
			}
			return
		}
	}

	r.URL.Path = path
	http.FileServer(http.Dir("Web")).ServeHTTP(w, r)
}
