package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	listenAddr      = "127.0.0.1:54455"
	listenPort      = 54455
	retOK           = 100
	retNotSupported = 1020
)

// tipPNG is a 1×1 transparent PNG served at /Cfg/tip.png to signal plugin presence to the XM Web UI.
const tipPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACklEQVR42mP8/w8AAwAB/6+kzrEAAAAASUVORK5CYII="

var mockSystemFunction = map[string]any{
	"AlarmFunction":     map[string]bool{"AlarmConfig": true, "BlindDetect": true, "LossDetect": true, "MotionDetect": true},
	"CommFunction":      map[string]bool{"CommRS232": true, "CommRS485": true},
	"EncodeFunction":    map[string]bool{"CombineStream": true, "SnapStream": true},
	"NetServerFunction": map[string]bool{"NetCommon": true, "NetDHCP": true, "NetDNS": true, "NetNTP": true},
	"OtherFunction":     map[string]any{"SupportModifyFrontcfg": true, "SupportIntelligent": true},
}

var mockGeneral = map[string]any{
	"LocalNo": 0, "OverWrite": "OverWrite", "AutoLogout": 10, "MachineName": "XM-Camera",
}

var mockLocation = map[string]any{
	"DateFormat": "YYMMDD", "DateSeparator": "-", "TimeFormat": "24Hour", "Language": "English", "VideoFormat": "PAL",
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getInt(m map[string]any, key string) int {
	if val, ok := m[key].(float64); ok {
		return int(val)
	}
	return 0
}

func cmdWebLocalCtrlHandler(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	mainType := getInt(req, "MainType")
	msgID := getInt(req, "MsgID")

	log.Printf("Control: MainType=%v", mainType)
	w.Header().Set("Content-Type", "application/json")

	var resp any
	switch mainType {
	case 1:
		resp = map[string]any{"Ret": retOK, "Version": "5.0.0.1", "AutoPreviewNum": 8, "MsgID": msgID}
	case 99:
		resp = map[string]any{"Ret": retOK, "port": listenPort, "MsgID": msgID}
	default:
		resp = map[string]any{"Ret": retOK, "MsgID": msgID}
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func videoPlayHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	for {
		var msg map[string]any
		if err := wsjson.Read(r.Context(), conn, &msg); err != nil {
			break
		}

		mainType := getInt(msg, "MainType")
		msgID := getInt(msg, "MsgID")
		name, _ := msg["Name"].(string)

		log.Printf("WS: MainType=%v Name=%s", mainType, name)

		var resp any
		switch {
		case mainType == 8: // LOGIN
			resp = map[string]any{
				"Ret": retOK, "MsgID": msgID,
				"ChannelNum": 16, "DeviceType": "HVR", "SoftWareVersion": "1.0.0.1",
			}
		case mainType == 38: // SDK remote search — return 1020 to fall back to CGI
			resp = map[string]any{"Ret": retNotSupported, "MsgID": msgID, "Msg": "NotSupported"}
		case mainType == 6 && name == "OPLocalSearch": // local search — same fallback
			resp = map[string]any{"Ret": retNotSupported, "MsgID": msgID, "Name": name, "Msg": "NotSupported"}
		case name == "SystemFunction":
			resp = map[string]any{"Ret": retOK, "MsgID": msgID, "Name": name, "SystemFunction": mockSystemFunction}
		case name == "General.General":
			resp = map[string]any{"Ret": retOK, "MsgID": msgID, "Name": name, "General.General": mockGeneral}
		case name == "General.Location":
			resp = map[string]any{"Ret": retOK, "MsgID": msgID, "Name": name, "General.Location": mockLocation}
		case name == "OPTimeQuery":
			resp = map[string]any{"Ret": retOK, "MsgID": msgID, "Name": name, "OPTimeQuery": time.Now().Format("2006-01-02 15:04:05")}
		default:
			resp = map[string]any{"Ret": retOK, "MsgID": msgID, "Name": name}
		}
		if err := wsjson.Write(r.Context(), conn, resp); err != nil {
			break
		}
	}
}

func staticHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/WebStyle_") {
		parts := strings.SplitN(path[1:], "/", 2)
		if len(parts) > 1 {
			path = "/" + parts[1]
		}
	}
	r.URL.Path = path
	http.FileServer(http.Dir("Web")).ServeHTTP(w, r)
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/Cfg/tip.png", func(w http.ResponseWriter, r *http.Request) {
		data, _ := base64.StdEncoding.DecodeString(tipPNG)
		w.Header().Set("Content-Type", "image/png")
		if _, err := w.Write(data); err != nil {
			log.Printf("write tip.png: %v", err)
		}
	})
	mux.HandleFunc("/Cmd-WebLocalCtrl", cmdWebLocalCtrlHandler)
	mux.HandleFunc("/VideoPlay", videoPlayHandler)
	mux.HandleFunc("/", staticHandler)

	if _, err := os.Stat("Web"); os.IsNotExist(err) {
		log.Fatal("Web/ directory not found. Run ./extract_web_assets.sh first.")
	}

	log.Printf("Starting xmcam-local on %s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, corsMiddleware(mux)))
}
