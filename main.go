package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
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

const tipPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACklEQVR42mP8/w8AAwAB/6+kzrEAAAAASUVUUIDc="

// Credentials are captured from the client's login form at runtime (never
// hardcoded). They are empty until the browser sends them over the WebSocket.
var (
	currentAuthMu   sync.Mutex
	currentUsername = ""
	currentPassword = ""
)

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
	case 33:
		resp = map[string]any{"Ret": retOK, "PrintLogEn": true, "MsgID": msgID}
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

	var wsWriteMu sync.Mutex

	writeJSON := func(v any) error {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		return wsjson.Write(r.Context(), conn, v)
	}

	writeBinary := func(data []byte) error {
		wsWriteMu.Lock()
		defer wsWriteMu.Unlock()
		return conn.Write(r.Context(), websocket.MessageBinary, data)
	}

	cameraIP := "192.168.88.65"
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Hostname() != "" {
			cameraIP = u.Hostname()
		}
	}

	// Multi-window support: one WebSocket connection can stream several channels
	// at once (grid view). Each channel has its own RTSP streamer + client channel,
	// and every binary frame is prefixed with a channel byte so the browser can
	// route it to the right grid cell. Wire format: [channel][codec][annexb...].
	type chanStream struct {
		streamer *RTSPStreamer
		clientCh chan []byte
	}
	streams := make(map[int]*chanStream)
	var streamsMu sync.Mutex
	lastMultiCount := 1

	stopChannel := func(ch int) {
		streamsMu.Lock()
		cs := streams[ch]
		delete(streams, ch)
		streamsMu.Unlock()
		if cs != nil {
			cs.streamer.RemoveClient(cs.clientCh)
		}
	}

	stopAll := func() {
		streamsMu.Lock()
		all := streams
		streams = make(map[int]*chanStream)
		streamsMu.Unlock()
		for _, cs := range all {
			cs.streamer.RemoveClient(cs.clientCh)
		}
	}

	startChannel := func(ch, stream int) {
		stopChannel(ch) // replace any existing stream on this channel

		currentAuthMu.Lock()
		usr := currentUsername
		pwd := currentPassword
		currentAuthMu.Unlock()

		if usr == "" || pwd == "" {
			log.Printf("[Preview] No credentials yet (waiting for client login); skipping Channel %d", ch)
			return
		}

		log.Printf("[Preview] Start Camera %s Channel %d Stream %d", cameraIP, ch, stream)

		st := GetStreamManager().GetStreamer(cameraIP, usr, pwd, ch, stream)
		clientCh := st.AddClient()

		streamsMu.Lock()
		streams[ch] = &chanStream{streamer: st, clientCh: clientCh}
		streamsMu.Unlock()

		chByte := byte(ch & 0xFF)
		streamByte := byte(stream & 0xFF)
		go func(clientCh chan []byte, ctx context.Context) {
			for {
				select {
				case frame, ok := <-clientCh:
					if !ok {
						return
					}
					// Wire format: [channel][stream][codec][annexb...]
					buf := make([]byte, 0, len(frame)+2)
					buf = append(buf, chByte, streamByte)
					buf = append(buf, frame...)
					if err := writeBinary(buf); err != nil {
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(clientCh, r.Context())
	}

	// On session close (tab closed, logout, navigation) stop all streams and wipe
	// the captured credentials from memory so they don't outlive the live session.
	defer func() {
		stopAll()
		currentAuthMu.Lock()
		currentUsername = ""
		currentPassword = ""
		currentAuthMu.Unlock()
		log.Printf("[Auth] Session closed; cleared credentials from memory")
	}()

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
		case mainType == 888: // Intercepted password from login form
			if usr, ok := msg["User"].(string); ok && usr != "" {
				currentAuthMu.Lock()
				currentUsername = usr
				currentAuthMu.Unlock()
			}
			if pwd, ok := msg["Password"].(string); ok && pwd != "" {
				currentAuthMu.Lock()
				currentPassword = pwd
				currentAuthMu.Unlock()
				log.Printf("[Auth] Intercepted credentials for user '%s'", currentUsername)
			}
			resp = map[string]any{"Ret": retOK, "MsgID": msgID}

		case mainType == 8: // LOGIN from plugin SDK
			if dev, ok := msg["Device"].(map[string]any); ok {
				currentAuthMu.Lock()
				if usr, ok := dev["User"].(string); ok && usr != "" {
					currentUsername = usr
				}
				if pwd, ok := dev["Password"].(string); ok && pwd != "" {
					currentPassword = pwd
				}
				log.Printf("[Auth] Login message credentials for user '%s'", currentUsername)
				currentAuthMu.Unlock()

				if ip, ok := dev["IP"].(string); ok && ip != "" {
					cameraIP = ip
				}
			}
			resp = map[string]any{
				"Ret": retOK, "MsgID": msgID,
				"ChannelNum": 16, "DeviceType": "HVR", "SoftWareVersion": "1.0.0.1",
			}

		case mainType == 4: // PREVIEW CONTROL (distinguished by SubType)
			subType := getInt(msg, "SubType")
			channel := getInt(msg, "Channel")
			stream := getInt(msg, "Stream")

			switch subType {
			case 0: // START / SWITCH preview
				switch {
				case channel >= 256: // multi-open: open (channel-256) channels, 0..N-1
					count := channel - 256
					if count < 1 {
						count = 1
					}
					if count > 64 {
						count = 64
					}
					lastMultiCount = count
					log.Printf("[Preview] Multi-open %d channels, stream %d", count, stream)
					stopAll()
					for i := 0; i < count; i++ {
						startChannel(i, stream)
					}
				case channel < 0: // switch-all: reopen the last multi set with new stream
					log.Printf("[Preview] Switch-all %d channels to stream %d", lastMultiCount, stream)
					stopAll()
					for i := 0; i < lastMultiCount; i++ {
						startChannel(i, stream)
					}
				default: // single channel
					startChannel(channel, stream)
				}

			case 1: // STOP preview
				if channel >= 256 || channel < 0 {
					log.Printf("[Preview] Stop all")
					stopAll()
				} else {
					log.Printf("[Preview] Stop channel %d", channel)
					stopChannel(channel)
				}

			default:
				// Sound(8)/Capture(9)/Record(10)/Color(3)/FullScreen(5)/Status(17):
				// UI-side operations that must NOT restart the RTSP stream.
				log.Printf("[Preview] Ignoring MainType 4 SubType %d (no stream change)", subType)
			}

			resp = map[string]any{"Ret": retOK, "MsgID": msgID, "Name": name, "Chn": channel, "Stream": stream}

		case mainType == 5 && getInt(msg, "SubType") < 33: // legacy STOP PREVIEW (not playback)
			stopAll()
			resp = map[string]any{"Ret": retOK, "MsgID": msgID, "Name": name}

		case mainType == 3: // SPLIT MODE CHANGE (grid layout handled client-side)
			splitMode := getInt(msg, "SubType")
			log.Printf("[Layout] Split mode changed to %d", splitMode)
			resp = map[string]any{"Ret": retOK, "MsgID": msgID, "Name": name}

		case mainType == 38: // SDK remote search
			resp = map[string]any{"Ret": retNotSupported, "MsgID": msgID, "Msg": "NotSupported"}
		case mainType == 6 && name == "OPLocalSearch":
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
		if err := writeJSON(resp); err != nil {
			break
		}
	}
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
