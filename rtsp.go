package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph265"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/pion/rtp"
)

var annexbHeader = []byte{0, 0, 0, 1}
var h264Header = []byte{1}
var h265Header = []byte{2}

type StreamManager struct {
	mu        sync.Mutex
	streamers map[string]*RTSPStreamer
}

var globalManager *StreamManager
var managerOnce sync.Once

func GetStreamManager() *StreamManager {
	managerOnce.Do(func() {
		globalManager = &StreamManager{
			streamers: make(map[string]*RTSPStreamer),
		}
	})
	return globalManager
}

func (m *StreamManager) GetStreamer(cameraIP, user, password string, channel, streamType int) *RTSPStreamer {
	m.mu.Lock()
	defer m.mu.Unlock()

	chNum := channel
	if chNum < 0 || chNum >= 16 {
		chNum = 0
	}

	key := fmt.Sprintf("%s_ch%d_str%d", cameraIP, chNum, streamType)
	streamer, exists := m.streamers[key]
	if !exists {
		streamer = &RTSPStreamer{
			cameraIP:   cameraIP,
			username:   user,
			password:   password,
			channel:    chNum,
			streamType: streamType,
			key:        key,
			clients:    make(map[chan []byte]bool),
		}
		m.streamers[key] = streamer
		streamer.Start()
	} else {
		streamer.username = user
		streamer.password = password
	}
	return streamer
}

func (m *StreamManager) StopStreamer(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if streamer, ok := m.streamers[key]; ok {
		log.Printf("[RTSP] Stopping streamer for key %s", key)
		streamer.Stop()
		delete(m.streamers, key)
	}
}

type RTSPStreamer struct {
	mu         sync.Mutex
	cameraIP   string
	username   string
	password   string
	channel    int
	streamType int
	key        string
	cancelFunc context.CancelFunc
	running    bool
	clients    map[chan []byte]bool
}

func (s *RTSPStreamer) AddClient() chan []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan []byte, 500)
	s.clients[ch] = true
	return ch
}

func (s *RTSPStreamer) RemoveClient(ch chan []byte) {
	s.mu.Lock()
	if _, ok := s.clients[ch]; ok {
		delete(s.clients, ch)
		close(ch)
	}
	count := len(s.clients)
	s.mu.Unlock()

	if count == 0 {
		GetStreamManager().StopStreamer(s.key)
	}
}

func (s *RTSPStreamer) broadcast(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for ch := range s.clients {
		select {
		case ch <- data:
		default:
			// Buffer full, drop frame to maintain real-time low latency
		}
	}
}

func (s *RTSPStreamer) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFunc = cancel
	s.mu.Unlock()

	go s.run(ctx)
}

func (s *RTSPStreamer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancelFunc != nil {
		s.cancelFunc()
		s.cancelFunc = nil
	}
	s.running = false
}

func (s *RTSPStreamer) run(ctx context.Context) {
	user := s.username
	pwd := s.password
	if user == "" || pwd == "" {
		log.Printf("[RTSP] Missing credentials for Ch %d; not connecting", s.channel+1)
		return
	}

	rtspURL := fmt.Sprintf("rtsp://%s:%s@%s:554/user=%s&password=%s&channel=%d&stream=%d.sdp",
		user, pwd, s.cameraIP, user, pwd, s.channel+1, s.streamType)

	log.Printf("[RTSP] Connecting via TCP to %s (Ch: %d, Stream: %d)...", s.cameraIP, s.channel+1, s.streamType)

	u, err := base.ParseURL(rtspURL)
	if err != nil {
		log.Printf("[RTSP] URL parse error: %v", err)
		return
	}

	protoTCP := gortsplib.ProtocolTCP
	c := gortsplib.Client{
		Scheme:   u.Scheme,
		Host:     u.Host,
		Protocol: &protoTCP,
	}

	if err := c.Start(); err != nil {
		log.Printf("[RTSP] Client start error for Ch %d: %v", s.channel+1, err)
		return
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		log.Printf("[RTSP] Describe error for Ch %d: %v", s.channel+1, err)
		return
	}

	var formaH264 *format.H264
	var formaH265 *format.H265

	mediH264 := desc.FindFormat(&formaH264)
	mediH265 := desc.FindFormat(&formaH265)

	if mediH264 == nil && mediH265 == nil {
		log.Printf("[RTSP] Neither H.264 nor H.265 format found for Ch %d", s.channel+1)
		return
	}

	if mediH265 != nil {
		log.Printf("[RTSP] Ch %d: Detected H.265 (HEVC) stream format", s.channel+1)
		rtpDec, err := formaH265.CreateDecoder()
		if err != nil {
			log.Printf("[RTSP] H.265 decoder create error: %v", err)
			return
		}

		_, err = c.Setup(desc.BaseURL, mediH265, 0, 0)
		if err != nil {
			log.Printf("[RTSP] Setup H.265 error: %v", err)
			return
		}

		var lastVPS, lastSPS, lastPPS []byte
		if len(formaH265.VPS) > 0 {
			lastVPS = formaH265.VPS
		}
		if len(formaH265.SPS) > 0 {
			lastSPS = formaH265.SPS
		}
		if len(formaH265.PPS) > 0 {
			lastPPS = formaH265.PPS
		}

		c.OnPacketRTP(mediH265, formaH265, func(pkt *rtp.Packet) {
			au, err := rtpDec.Decode(pkt)
			if err != nil {
				if err != rtph265.ErrNonStartingPacketAndNoPrevious && err != rtph265.ErrMorePacketsNeeded {
				}
				return
			}

			hasIDR := false
			for _, nal := range au {
				if len(nal) == 0 {
					continue
				}
				nalType := (nal[0] >> 1) & 0x3F
				if nalType == 32 {
					lastVPS = nal
				}
				if nalType == 33 {
					lastSPS = nal
				}
				if nalType == 34 {
					lastPPS = nal
				}
				if nalType >= 19 && nalType <= 21 {
					hasIDR = true
				}
			}

			var frameBuffer []byte
			frameBuffer = append(frameBuffer, h265Header...)

			if hasIDR || h265.IsRandomAccess(au) {
				if len(lastVPS) > 0 {
					frameBuffer = append(frameBuffer, annexbHeader...)
					frameBuffer = append(frameBuffer, lastVPS...)
				}
				if len(lastSPS) > 0 {
					frameBuffer = append(frameBuffer, annexbHeader...)
					frameBuffer = append(frameBuffer, lastSPS...)
				}
				if len(lastPPS) > 0 {
					frameBuffer = append(frameBuffer, annexbHeader...)
					frameBuffer = append(frameBuffer, lastPPS...)
				}
			}

			for _, nal := range au {
				if len(nal) == 0 {
					continue
				}
				frameBuffer = append(frameBuffer, annexbHeader...)
				frameBuffer = append(frameBuffer, nal...)
			}

			if len(frameBuffer) > 1 {
				s.broadcast(frameBuffer)
			}
		})
	} else if mediH264 != nil {
		log.Printf("[RTSP] Ch %d: Detected H.264 stream format", s.channel+1)
		rtpDec, err := formaH264.CreateDecoder()
		if err != nil {
			log.Printf("[RTSP] H.264 decoder create error: %v", err)
			return
		}

		_, err = c.Setup(desc.BaseURL, mediH264, 0, 0)
		if err != nil {
			log.Printf("[RTSP] Setup H.264 error: %v", err)
			return
		}

		var lastSPS, lastPPS []byte
		if len(formaH264.SPS) > 0 {
			lastSPS = formaH264.SPS
		}
		if len(formaH264.PPS) > 0 {
			lastPPS = formaH264.PPS
		}

		c.OnPacketRTP(mediH264, formaH264, func(pkt *rtp.Packet) {
			au, err := rtpDec.Decode(pkt)
			if err != nil {
				if err != rtph264.ErrNonStartingPacketAndNoPrevious && err != rtph264.ErrMorePacketsNeeded {
				}
				return
			}

			hasIDR := false
			for _, nal := range au {
				if len(nal) == 0 {
					continue
				}
				nalType := nal[0] & 0x1F
				if nalType == 7 {
					lastSPS = nal
				}
				if nalType == 8 {
					lastPPS = nal
				}
				if nalType == 5 {
					hasIDR = true
				}
			}

			var frameBuffer []byte
			frameBuffer = append(frameBuffer, h264Header...)

			if hasIDR || h264.IsRandomAccess(au) {
				if len(lastSPS) > 0 {
					frameBuffer = append(frameBuffer, annexbHeader...)
					frameBuffer = append(frameBuffer, lastSPS...)
				}
				if len(lastPPS) > 0 {
					frameBuffer = append(frameBuffer, annexbHeader...)
					frameBuffer = append(frameBuffer, lastPPS...)
				}
			}

			for _, nal := range au {
				if len(nal) == 0 {
					continue
				}
				frameBuffer = append(frameBuffer, annexbHeader...)
				frameBuffer = append(frameBuffer, nal...)
			}

			if len(frameBuffer) > 1 {
				s.broadcast(frameBuffer)
			}
		})
	}

	_, err = c.Play(nil)
	if err != nil {
		log.Printf("[RTSP] Play error for Ch %d: %v", s.channel+1, err)
		return
	}

	log.Printf("[RTSP] Playing stream from camera %s (Ch %d, Stream %d)!", s.cameraIP, s.channel+1, s.streamType)

	select {
	case <-ctx.Done():
		log.Printf("[RTSP] Stopping stream for Ch %d...", s.channel+1)
	}
}
